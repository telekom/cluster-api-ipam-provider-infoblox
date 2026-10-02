// Package infoblox is responsible for communication with Infoblox instance.
package infoblox

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/go-logr/logr"
	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

//go:generate go run go.uber.org/mock/mockgen -destination=ibmock/client.go -package=ibmock . Client
//go:generate go run go.uber.org/mock/mockgen -source=client.go -exclude_interfaces=Client -destination=ibclientmock/objectmanager.go -package=ibclientmock -mock_names=objectManager=MockObjectManager
//go:generate go run go.uber.org/mock/mockgen -destination=ibclientmock/connector.go -package=ibclientmock -mock_names=IBConnector=MockConnector github.com/infobloxopen/infoblox-go-client/v2 IBConnector

const (
	secretKeyUsername   = "username"
	secretKeyPassowrd   = "password"
	secretKeyClientCert = "clientCert"
	secretKeyClientKey  = "clientKey"

	// requestTimeoutSeconds is in seconds because ibclient multiplies it by time.Second itself.
	requestTimeoutSeconds = 30
)

// Client is a wrapper around the infoblox client that can allocate and release addresses indempotently.
type Client interface {
	// GetOrAllocateAddress allocates an address for a given hostname if none exists, and returns the new or existing address.
	GetOrAllocateAddress(networkView, dnsView string, subnet netip.Prefix, hostname, zone string, logger logr.Logger) (netip.Addr, error)
	// IsAddressAssigned reports whether the host record of a given hostname holds the given address. It never allocates.
	IsAddressAssigned(networkView, hostname string, addr netip.Addr) (bool, error)
	// ReleaseAddress releases an address for a given hostname.
	ReleaseAddress(networkView, dnsView string, subnet netip.Prefix, hostname string, logger logr.Logger) error
	// CheckNetworkViewExists checks if Infoblox network view exists
	CheckNetworkViewExists(view string) (bool, error)
	// CheckDNSViewExists checks if Infoblox DNS view exists
	CheckDNSViewExists(view string) (bool, error)
	// CheckNetworkExists checks if Infoblox network exists
	CheckNetworkExists(view string, subnet netip.Prefix) (bool, error)
	// CheckConnection checks that Infoblox answers a WAPI request with the configured host, version and credentials.
	CheckConnection() error
	GetHostConfig() *HostConfig
}

// objectManager is the subset of ibclient.IBObjectManager the client uses.
type objectManager interface {
	GetNetworkView(name string) (*ibclient.NetworkView, error)
	GetDNSView(name string) (*ibclient.View, error)
	GetNetwork(netview string, cidr string, isIPv6 bool, ea ibclient.EA) (*ibclient.Network, error)
}

type client struct {
	connector ibclient.IBConnector
	objMgr    objectManager
	hc        HostConfig
}

var _ Client = &client{}

// AuthConfig contains authentication parameters to use for authenticating against the API.
type AuthConfig struct {
	Username   string
	Password   string
	ClientCert []byte
	ClientKey  []byte
}

// HostConfig contains host configuration patameters.
type HostConfig struct {
	Host                   string
	Port                   string
	Version                string
	DisableTLSVerification bool
	CustomCAPath           string
	DefaultNetworkView     string
	DefaultDNSView         string
}

// Config is a wrapper config structures.
type Config struct {
	HostConfig
	AuthConfig
}

// NewClient creates a new infoblox client.
func NewClient(config Config) (Client, error) {
	hc := ibclient.HostConfig{
		Host:    config.Host,
		Port:    config.Port,
		Version: config.Version,
	}
	ac := ibclient.AuthConfig{
		Username:   config.Username,
		Password:   config.Password,
		ClientCert: config.ClientCert,
		ClientKey:  config.ClientKey,
	}
	tlsVerify := "true"
	if config.DisableTLSVerification {
		tlsVerify = "false"
	} else if config.CustomCAPath != "" {
		if err := validateCAFile(config.CustomCAPath); err != nil {
			return nil, err
		}
		tlsVerify = config.CustomCAPath
	}
	// ibclient calls log.Fatal on an invalid pair, which would crash the whole controller.
	if config.ClientCert != nil && config.ClientKey != nil {
		if _, err := tls.X509KeyPair(config.ClientCert, config.ClientKey); err != nil {
			return nil, fmt.Errorf("invalid client certificate or key: %w", err)
		}
	}

	rb := &ibclient.WapiRequestBuilder{}
	rq := &ibclient.WapiHttpRequestor{}
	tc := ibclient.NewTransportConfig(tlsVerify, requestTimeoutSeconds, 5)
	// ibclient silently disables verification if it cannot load the CA file.
	if !config.DisableTLSVerification && !tc.SslVerify {
		return nil, fmt.Errorf("failed to enable TLS verification with custom CA file %q", config.CustomCAPath)
	}
	con, err := ibclient.NewConnector(hc, ac, tc, rb, rq)
	if err != nil {
		// does not happen with the current infoblox-go-client
		return nil, err
	}

	objMgr := ibclient.NewObjectManager(con, "cluster-api-ipam-provider-infoblox", "")

	return &client{
		connector: con,
		objMgr:    objMgr,
		hc:        config.HostConfig,
	}, nil
}

func validateCAFile(path string) error {
	pemData, err := os.ReadFile(path) //nolint:gosec // the admin-configured path is read by ibclient anyway, its content is never exposed
	if err != nil {
		return fmt.Errorf("failed to read custom CA file: %w", err)
	}
	if !x509.NewCertPool().AppendCertsFromPEM(pemData) {
		return fmt.Errorf("custom CA file %q contains no valid PEM certificate", path)
	}
	return nil
}

// AuthConfigFromSecretData creates a AuthConfig from the contents of a secret.
// The secret must contain either username/password or clientCert/clientKey values. The former is used if both are present.
func AuthConfigFromSecretData(data map[string][]byte) (AuthConfig, error) {
	config := AuthConfig{
		Username:   string(data[secretKeyUsername]),
		Password:   string(data[secretKeyPassowrd]),
		ClientCert: data[secretKeyClientCert],
		ClientKey:  data[secretKeyClientKey],
	}
	if (config.Username != "" && config.Password != "") ||
		(len(config.ClientCert) > 0 && len(config.ClientKey) > 0) {
		return config, nil
	}
	return AuthConfig{}, errors.New("no usable pair of credentials found. provide either username/password or clientCert/clientKey")
}

func (c *client) CheckNetworkViewExists(view string) (bool, error) {
	_, err := c.objMgr.GetNetworkView(view)
	// GetNetworkView returns this untyped error if GetObject yields an empty result without error (e.g. a "null" body).
	if err != nil && err.Error() == fmt.Sprintf("network view '%s' not found", view) {
		return false, nil
	}
	return c.handleExistsResult(err, "GetNetworkView", map[string]string{"view": view})
}

func (c *client) CheckDNSViewExists(view string) (bool, error) {
	_, err := c.objMgr.GetDNSView(view)
	return c.handleExistsResult(err, "GetDNSView", map[string]string{"view": view})
}

func (c *client) CheckNetworkExists(view string, subnet netip.Prefix) (bool, error) {
	_, err := c.objMgr.GetNetwork(view, subnet.String(), subnet.Addr().Is6(), ibclient.EA{})
	return c.handleExistsResult(err, "GetNetwork", map[string]string{
		"view":   view,
		"subnet": subnet.String(),
	})
}

func (c *client) GetHostConfig() *HostConfig {
	return &c.hc
}

// schemaRequest is the WAPI schema, its empty object type makes ibclient request /wapi/v<version>/.
type schemaRequest struct {
	ibclient.IBBase
}

func (*schemaRequest) ObjectType() string {
	return ""
}

func (c *client) CheckConnection() error {
	query := ibclient.NewQueryParams(false, map[string]string{"_schema": "1"})
	err := c.connector.GetObject(&schemaRequest{}, "", query, &map[string]any{})
	return c.wrapAsRequestError(err, "GetSchema", nil)
}

// handleExistsResult returns true if the object exists,
// false if it does not exist,
// and an error if there was an error checking for existence.
func (c *client) handleExistsResult(err error, operation string, params map[string]string) (bool, error) {
	if err == nil {
		return true, nil
	}
	if IsNotFoundError(err) {
		return false, nil
	}
	return false, c.wrapAsRequestError(err, operation, params)
}

func (c *client) wrapAsRequestError(err error, operation string, params map[string]string) error {
	if err == nil {
		return nil
	}
	port := c.hc.Port
	if port == "" {
		port = "443"
	}
	return newRequestError(err, net.JoinHostPort(c.hc.Host, port), operation, params)
}
