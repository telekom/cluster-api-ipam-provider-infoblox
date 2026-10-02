//go:build infoblox

package infoblox_test

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
)

const infobloxTestEnvPrefix = "CAIP_INFOBLOX_TEST_"

func configFromEnv() (infoblox.Config, error) {
	host := getInfobloxTestEnvVar("host", "")
	if host == "" {
		return infoblox.Config{}, errors.New(infobloxTestEnvPrefix + "HOST is not set")
	}
	config := infoblox.Config{
		HostConfig: infoblox.HostConfig{
			Host:                   host,
			Port:                   getInfobloxTestEnvVar("port", "443"),
			DisableTLSVerification: strToBool(getInfobloxTestEnvVar("skip_tls_verify", "false")),
			Version:                getInfobloxTestEnvVar("wapi_version", ""),
		},
		AuthConfig: infoblox.AuthConfig{
			Username:   getInfobloxTestEnvVar("username", ""),
			Password:   getInfobloxTestEnvVar("password", ""),
			ClientCert: byteArrOrNil(getInfobloxTestEnvVar("clientcert", "")),
			ClientKey:  byteArrOrNil(getInfobloxTestEnvVar("clientkey", "")),
		},
	}
	return config, nil
}

// newIBConnector connects to the instance directly, for fixtures the client under test does not offer.
func newIBConnector(config infoblox.Config) (*ibclient.Connector, error) {
	tlsVerify := "true"
	if config.DisableTLSVerification {
		tlsVerify = "false"
	}
	return ibclient.NewConnector(
		ibclient.HostConfig{Host: config.Host, Port: config.Port, Version: config.Version},
		ibclient.AuthConfig{
			Username:   config.Username,
			Password:   config.Password,
			ClientCert: config.ClientCert,
			ClientKey:  config.ClientKey,
		},
		ibclient.NewTransportConfig(tlsVerify, 60, 5),
		&ibclient.WapiRequestBuilder{},
		&ibclient.WapiHttpRequestor{},
	)
}

func nextAvailableIP(subnet netip.Prefix, view string) string {
	return fmt.Sprintf("func:nextavailableip:%s,%s", subnet.String(), view)
}

func getInfobloxTestEnvVar(name, defaultValue string) string {
	val, ok := os.LookupEnv(infobloxTestEnvPrefix + strings.ToUpper(name))
	if !ok || val == "" {
		return defaultValue
	}
	return val
}

func strToBool(s string) bool {
	return strings.EqualFold(s, "true")
}

func byteArrOrNil(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}
