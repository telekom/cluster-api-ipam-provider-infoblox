package infoblox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibclientmock"
	"go.uber.org/mock/gomock"
)

const testHost = "ib.example.com"

// newMockedClient returns a client whose Infoblox API access goes through fresh mocks.
func newMockedClient(t *testing.T) (*client, *ibclientmock.MockConnector, *ibclientmock.MockObjectManager) {
	t.Helper()
	mockCtrl := gomock.NewController(t)
	connector := ibclientmock.NewMockConnector(mockCtrl)
	objMgr := ibclientmock.NewMockObjectManager(mockCtrl)
	return &client{
		connector: connector,
		objMgr:    objMgr,
		hc:        HostConfig{Host: testHost, Port: "8443"},
	}, connector, objMgr
}

func TestNewClientWiresInfobloxAccess(t *testing.T) {
	g := NewWithT(t)
	hostConfig := HostConfig{Host: testHost, Port: "8443", Version: "2.12", DefaultNetworkView: "my-view"}

	ibClient, err := NewClient(Config{HostConfig: hostConfig, AuthConfig: AuthConfig{Username: "user", Password: "pass"}})

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ibClient.GetHostConfig()).To(Equal(&hostConfig))
	c, ok := ibClient.(*client)
	g.Expect(ok).To(BeTrue())
	g.Expect(c.connector).NotTo(BeNil())
	g.Expect(c.objMgr).NotTo(BeNil())
}

// selfSignedPEM returns a PEM encoded self-signed certificate and its private key.
func selfSignedPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: testHost},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// writeTempFile writes content to a file in a directory removed after the test.
func writeTempFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewClientValidatesTLSConfig(t *testing.T) {
	certPEM, keyPEM := selfSignedPEM(t)
	missingPath := filepath.Join(t.TempDir(), "missing.crt")
	tests := []struct {
		name       string
		hostConfig HostConfig
		authConfig AuthConfig
		wantErr    string
	}{
		{
			name:       "valid custom CA file",
			hostConfig: HostConfig{CustomCAPath: writeTempFile(t, certPEM)},
		},
		{
			name:       "missing custom CA file",
			hostConfig: HostConfig{CustomCAPath: missingPath},
			wantErr:    "failed to read custom CA file: open " + missingPath + ": no such file or directory",
		},
		{
			name:       "custom CA file without certificate",
			hostConfig: HostConfig{CustomCAPath: writeTempFile(t, []byte("not a certificate"))},
			wantErr:    "contains no valid PEM certificate",
		},
		{
			name:       "custom CA file is ignored when TLS verification is disabled",
			hostConfig: HostConfig{CustomCAPath: missingPath, DisableTLSVerification: true},
		},
		{
			name:       "valid client certificate",
			authConfig: AuthConfig{ClientCert: certPEM, ClientKey: keyPEM},
		},
		{
			name:       "invalid client key",
			authConfig: AuthConfig{ClientCert: certPEM, ClientKey: []byte("not a key")},
			wantErr:    "invalid client certificate or key: tls: failed to find any PEM data in key input",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			tt.hostConfig.Host = testHost

			ibClient, err := NewClient(Config{HostConfig: tt.hostConfig, AuthConfig: tt.authConfig})

			if tt.wantErr == "" {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ibClient).NotTo(BeNil())
				return
			}
			g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
			g.Expect(ibClient).To(BeNil())
		})
	}
}

func TestWrapAsRequestError(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		port         string
		wantEndpoint string
	}{
		{name: "configured port", host: testHost, port: "8443", wantEndpoint: "ib.example.com:8443"},
		{name: "default port", host: testHost, wantEndpoint: "ib.example.com:443"},
		{name: "IPv6 host", host: "fd00::1", port: "443", wantEndpoint: "[fd00::1]:443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c := &client{hc: HostConfig{Host: tt.host, Port: tt.port}}

			err := c.wrapAsRequestError(errors.New("boom"), "Op", map[string]string{"k": "v"})

			var reqErr RequestError
			g.Expect(errors.As(err, &reqErr)).To(BeTrue())
			g.Expect(reqErr.Endpoint).To(Equal(tt.wantEndpoint))
			g.Expect(reqErr.Operation).To(Equal("Op"))
			g.Expect(reqErr.Params).To(Equal(map[string]string{"k": "v"}))
		})
	}
}

func TestWrapAsRequestErrorReturnsNilForNil(t *testing.T) {
	g := NewWithT(t)
	c := &client{hc: HostConfig{Host: testHost}}

	g.Expect(c.wrapAsRequestError(nil, "Op", nil)).To(Succeed())
}

func TestCheckNetworkViewExists(t *testing.T) {
	tests := []struct {
		name       string
		lookupErr  error
		wantExists bool
		wantErr    bool
	}{
		{name: "exists", wantExists: true},
		{name: "typed not found error", lookupErr: ibclient.NewNotFoundError("not found")},
		// GetNetworkView returns this untyped error for an empty result without an error.
		{name: "untyped not found error for the view", lookupErr: errors.New("network view 'my-view' not found")},
		{name: "untyped not found error for another view", lookupErr: errors.New("network view 'other' not found"), wantErr: true},
		{name: "other error", lookupErr: errors.New("boom"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, _, objMgr := newMockedClient(t)
			objMgr.EXPECT().GetNetworkView("my-view").Return(&ibclient.NetworkView{}, tt.lookupErr)

			exists, err := c.CheckNetworkViewExists("my-view")

			g.Expect(exists).To(Equal(tt.wantExists))
			if !tt.wantErr {
				g.Expect(err).NotTo(HaveOccurred())
				return
			}
			var reqErr RequestError
			g.Expect(errors.As(err, &reqErr)).To(BeTrue())
			g.Expect(reqErr.Operation).To(Equal("GetNetworkView"))
			g.Expect(reqErr.Params).To(Equal(map[string]string{"view": "my-view"}))
			g.Expect(errors.Is(err, tt.lookupErr)).To(BeTrue())
		})
	}
}

func TestCheckDNSViewExists(t *testing.T) {
	tests := []struct {
		name       string
		lookupErr  error
		wantExists bool
		wantErr    bool
	}{
		{name: "exists", wantExists: true},
		{name: "typed not found error", lookupErr: ibclient.NewNotFoundError("DNS view with name 'my-view' not found")},
		{name: "other error", lookupErr: errors.New("boom"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, _, objMgr := newMockedClient(t)
			objMgr.EXPECT().GetDNSView("my-view").Return(&ibclient.View{}, tt.lookupErr)

			exists, err := c.CheckDNSViewExists("my-view")

			g.Expect(exists).To(Equal(tt.wantExists))
			if !tt.wantErr {
				g.Expect(err).NotTo(HaveOccurred())
				return
			}
			var reqErr RequestError
			g.Expect(errors.As(err, &reqErr)).To(BeTrue())
			g.Expect(reqErr.Operation).To(Equal("GetDNSView"))
			g.Expect(reqErr.Params).To(Equal(map[string]string{"view": "my-view"}))
		})
	}
}

func TestCheckNetworkExists(t *testing.T) {
	tests := []struct {
		name       string
		subnet     netip.Prefix
		lookupErr  error
		wantExists bool
		wantErr    bool
	}{
		{name: "IPv4 network exists", subnet: netip.MustParsePrefix("10.0.0.0/24"), wantExists: true},
		{name: "IPv6 network exists", subnet: netip.MustParsePrefix("fd00::/64"), wantExists: true},
		{name: "typed not found error", subnet: netip.MustParsePrefix("10.0.0.0/24"), lookupErr: ibclient.NewNotFoundError("not found")},
		{name: "other error", subnet: netip.MustParsePrefix("10.0.0.0/24"), lookupErr: errors.New("boom"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, _, objMgr := newMockedClient(t)
			objMgr.EXPECT().GetNetwork("my-view", tt.subnet.String(), tt.subnet.Addr().Is6(), gomock.Any()).
				Return(&ibclient.Network{}, tt.lookupErr)

			exists, err := c.CheckNetworkExists("my-view", tt.subnet)

			g.Expect(exists).To(Equal(tt.wantExists))
			if !tt.wantErr {
				g.Expect(err).NotTo(HaveOccurred())
				return
			}
			var reqErr RequestError
			g.Expect(errors.As(err, &reqErr)).To(BeTrue())
			g.Expect(reqErr.Operation).To(Equal("GetNetwork"))
			g.Expect(reqErr.Params).To(Equal(map[string]string{"view": "my-view", "subnet": tt.subnet.String()}))
		})
	}
}

func TestAuthConfigFromSecretData(t *testing.T) {
	tests := []struct {
		name    string
		data    map[string][]byte
		want    AuthConfig
		wantErr bool
	}{
		{
			name: "username and password",
			data: map[string][]byte{"username": []byte("user"), "password": []byte("pass")},
			want: AuthConfig{Username: "user", Password: "pass"},
		},
		{
			name: "client certificate and key",
			data: map[string][]byte{"clientCert": []byte("cert"), "clientKey": []byte("key")},
			want: AuthConfig{ClientCert: []byte("cert"), ClientKey: []byte("key")},
		},
		{
			name: "both pairs",
			data: map[string][]byte{
				"username": []byte("user"), "password": []byte("pass"),
				"clientCert": []byte("cert"), "clientKey": []byte("key"),
			},
			want: AuthConfig{Username: "user", Password: "pass", ClientCert: []byte("cert"), ClientKey: []byte("key")},
		},
		{name: "username without password", data: map[string][]byte{"username": []byte("user")}, wantErr: true},
		{name: "client certificate without key", data: map[string][]byte{"clientCert": []byte("cert")}, wantErr: true},
		{name: "no data", data: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			got, err := AuthConfigFromSecretData(tt.data)

			if tt.wantErr {
				g.Expect(err).To(MatchError("no usable pair of credentials found. provide either username/password or clientCert/clientKey"))
				g.Expect(got).To(Equal(AuthConfig{}))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}
