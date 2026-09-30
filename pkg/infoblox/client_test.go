package infoblox

import (
	"errors"
	"net/netip"
	"testing"

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
				g.Expect(err).To(HaveOccurred())
				g.Expect(got).To(Equal(AuthConfig{}))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}
