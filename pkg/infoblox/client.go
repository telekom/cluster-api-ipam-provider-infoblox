// Package infoblox is responsible for communication with Infoblox instance.
package infoblox

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/go-logr/logr"
	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

//go:generate mockgen -destination=ibmock/client.go -package=ibmock . Client

const (
	secretKeyUsername   = "username"
	secretKeyPassowrd   = "password"
	secretKeyClientCert = "clientCert"
	secretKeyClientKey  = "clientKey"
)

// Client is a wrapper around the infoblox client that can allocate and release addresses indempotently.
type Client interface {
	// GetOrAllocateAddress allocates an address for a given hostname if none exists, and returns the new or existing address.
	GetOrAllocateAddress(networkView, dnsView string, subnet netip.Prefix, hostname, zone string, logger logr.Logger) (netip.Addr, error)
	// ReleaseAddress releases an address for a given hostname.
	ReleaseAddress(networkView, dnsView string, subnet netip.Prefix, hostname string, logger logr.Logger) error
	// CheckNetworkViewExists checks if Infoblox network view exists
	CheckNetworkViewExists(view string) (bool, error)
	// CheckDNSViewExists checks if Infoblox DNS view exists
	CheckDNSViewExists(view string) (bool, error)
	// CheckNetworkExists checks if Infoblox network exists
	CheckNetworkExists(view string, subnet netip.Prefix) (bool, error)
	GetHostConfig() *HostConfig
}

type client struct {
	connector *ibclient.Connector
	objMgr    ibclient.IBObjectManager
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
		tlsVerify = config.CustomCAPath
	}

	rb := &ibclient.WapiRequestBuilder{}
	rq := &ibclient.WapiHttpRequestor{}
	tc := ibclient.NewTransportConfig(tlsVerify, int(time.Second), 5)
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
