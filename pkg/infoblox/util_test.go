package infoblox

import (
	"errors"
	"os"
	"strings"
)

const infobloxTestEnvPrefix = "CAIP_INFOBLOX_TEST_"

func InfobloxConfigFromEnv() (HostConfig, AuthConfig, error) {
	hc := HostConfig{
		Host:                  getInfobloxTestEnvVar("host", ""),
		InsecureSkipTLSVerify: strToBool(getInfobloxTestEnvVar("skip_tls_verify", "false")),
		Version:               getInfobloxTestEnvVar("wapi_version", ""),
	}
	if hc.Host == "" {
		return HostConfig{}, AuthConfig{}, errors.New(infobloxTestEnvPrefix + "HOST is not set")
	}
	ac := AuthConfig{
		Username:   getInfobloxTestEnvVar("username", ""),
		Password:   getInfobloxTestEnvVar("password", ""),
		ClientCert: byteArrOrNil(getInfobloxTestEnvVar("clientcert", "")),
		ClientKey:  byteArrOrNil(getInfobloxTestEnvVar("clientkey", "")),
	}
	return hc, ac, nil
}

func getInfobloxTestEnvVar(name, defaultValue string) string {
	val, ok := os.LookupEnv(infobloxTestEnvPrefix + strings.ToUpper(name))
	if !ok || val == "" {
		return defaultValue
	}
	return val
}

func strToBool(s string) bool {
	return strings.ToLower(s) == "true"
}

func byteArrOrNil(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}
