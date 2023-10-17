package infoblox

import (
	"errors"
	"os"
	"strings"
)

const infobloxTestEnvPrefix = "CAIP_INFOBLOX_TEST_"

func InfobloxConfigFromEnv() (Config, error) {
	config := Config{
		HostConfig: HostConfig{
			Host:                  getInfobloxTestEnvVar("host", ""),
			InsecureSkipTLSVerify: strToBool(getInfobloxTestEnvVar("skip_tls_verify", "false")),
			Version:               getInfobloxTestEnvVar("wapi_version", ""),
		},
		AuthConfig: AuthConfig{
			Username:   getInfobloxTestEnvVar("username", ""),
			Password:   getInfobloxTestEnvVar("password", ""),
			ClientCert: byteArrOrNil(getInfobloxTestEnvVar("clientcert", "")),
			ClientKey:  byteArrOrNil(getInfobloxTestEnvVar("clientkey", "")),
		},
	}
	if config.Host == "" {
		return Config{}, errors.New(infobloxTestEnvPrefix + "HOST is not set")
	}
	return config, nil
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
