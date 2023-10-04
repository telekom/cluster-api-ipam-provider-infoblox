package infoblox

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
)

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
	GetOrAllocateAddress(view string, subnet netip.Prefix, hostname, zone string) (netip.Addr, error)
	// ReleaseAddress releases an address for a given hostname.
	ReleaseAddress(view string, subnet netip.Prefix, hostname string) error

	CheckNetworkViewExists(view string) (bool, error)

	CheckNetworkExists(view string, subnet netip.Prefix) (bool, error)
}

type client struct {
	connector *ibclient.Connector
	objMgr    ibclient.IBObjectManager
}

var _ Client = &client{}

// AuthConfig contains authentication parameters to use for authenticating against the API.
type AuthConfig struct {
	Username   string
	Password   string
	ClientCert []byte
	ClientKey  []byte
}

type HostConfig struct {
	Host                  string
	Version               string
	InsecureSkipTLSVerify bool
}

type Config struct {
	HostConfig
	AuthConfig
}

// NewClient creates a new infoblox client.
func NewClient(config Config) (Client, error) {
	hc := ibclient.HostConfig{
		Version: config.Version,
	}
	hostParts := strings.SplitN(config.Host, ":", 2)
	hc.Host = hostParts[0]
	if len(hostParts) == 2 {
		hc.Port = hostParts[1]
	} else {
		hc.Port = "443"
	}
	ac := ibclient.AuthConfig{
		Username:   config.Username,
		Password:   config.Password,
		ClientCert: config.ClientCert,
		ClientKey:  config.ClientKey,
	}
	tlsVerify := "true"
	if config.InsecureSkipTLSVerify {
		tlsVerify = "false"
	}
	rb := &ibclient.WapiRequestBuilder{}
	rq := &ibclient.WapiHttpRequestor{}
	tc := ibclient.NewTransportConfig(tlsVerify, 1, 5)
	con, err := ibclient.NewConnector(hc, ac, tc, rb, rq)
	if err != nil {
		// does not happen with the current infoblox-go-client
		return nil, err
	}
	objMgr := ibclient.NewObjectManager(con, "cluster-api-ipam-provider-infoblox", "")
	// _, err = objMgr.GetGridInfo()
	// if err != nil {
	// 	return nil, err
	// }
	return &client{
		connector: con,
		objMgr:    objMgr,
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
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *client) CheckNetworkExists(view string, subnet netip.Prefix) (bool, error) {
	_, err := c.objMgr.GetNetwork(view, subnet.String(), subnet.Addr().Is6(), ibclient.EA{})
	fmt.Println("CheckNetworkExists - getnetwork")
	if err != nil {
		fmt.Printf("error: %s\n", err.Error())
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	fmt.Println("no error")
	return true, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.HasSuffix(err.Error(), "not found")
}
