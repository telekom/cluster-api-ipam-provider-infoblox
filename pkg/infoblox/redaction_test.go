package infoblox

import (
	"fmt"
	"strings"
	"testing"
)

func TestAuthConfigStringRedactsCredentials(t *testing.T) {
	config := AuthConfig{
		Username:   "user",
		Password:   "pass",
		ClientCert: []byte("cert"),
		ClientKey:  []byte("key"),
	}

	for _, formatted := range []string{config.String(), fmt.Sprintf("%#v", config)} {
		for _, secret := range []string{"pass", "cert", "key"} {
			if strings.Contains(formatted, secret) {
				t.Fatalf("formatted AuthConfig leaked %q: %s", secret, formatted)
			}
		}
		if !strings.Contains(formatted, "user") {
			t.Fatalf("formatted AuthConfig did not retain the username: %s", formatted)
		}
		if !strings.Contains(formatted, "<redacted>") {
			t.Fatalf("formatted AuthConfig did not show redaction marker: %s", formatted)
		}
	}
}

func TestConfigStringRedactsEmbeddedCredentials(t *testing.T) {
	config := Config{
		HostConfig: HostConfig{
			Host:    "infoblox.example.com",
			Version: "2.13.1",
		},
		AuthConfig: AuthConfig{
			Username:  "user",
			Password:  "pass",
			ClientKey: []byte("key"),
		},
	}

	for _, formatted := range []string{config.String(), fmt.Sprintf("%#v", config)} {
		for _, secret := range []string{"pass", "key"} {
			if strings.Contains(formatted, secret) {
				t.Fatalf("formatted Config leaked %q: %s", secret, formatted)
			}
		}
		if !strings.Contains(formatted, "user") {
			t.Fatalf("formatted Config did not retain the username: %s", formatted)
		}
		if !strings.Contains(formatted, "infoblox.example.com") {
			t.Fatalf("formatted Config should retain non-sensitive host settings: %s", formatted)
		}
		if !strings.Contains(formatted, "<redacted>") {
			t.Fatalf("formatted Config did not show redaction marker: %s", formatted)
		}
	}
}
