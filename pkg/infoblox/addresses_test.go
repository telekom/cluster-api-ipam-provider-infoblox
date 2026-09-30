package infoblox

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/go-logr/logr"
	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox/ibclientmock"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
)

const (
	testHostname    = "machine-1.example.com"
	testNetworkView = "my-view"
	testRef         = "record:host/abc:machine-1.example.com/my-view"
)

var (
	testSubnetV4 = netip.MustParsePrefix("10.0.0.0/24")
	testSubnetV6 = netip.MustParsePrefix("fd00::/64")
)

// newHostRecord returns a host record as Infoblox returns it, holding the given addresses.
func newHostRecord(addrs ...string) ibclient.HostRecord {
	hr := ibclient.NewEmptyHostRecord()
	hr.Ref = testRef
	hr.Name = ptr.To(testHostname)
	hr.NetworkView = testNetworkView
	hr.Zone = "example.com"
	hr.View = ptr.To("default." + testNetworkView)
	for _, addr := range addrs {
		if netip.MustParseAddr(addr).Is4() {
			hr.Ipv4Addrs = append(hr.Ipv4Addrs, *ibclient.NewHostRecordIpv4Addr(addr, "", false, ""))
		} else {
			hr.Ipv6Addrs = append(hr.Ipv6Addrs, *ibclient.NewHostRecordIpv6Addr(addr, "", false, ""))
		}
	}
	return *hr
}

// expectHostRecordLookup expects the lookup of the test host record and answers with the given records and error.
func expectHostRecordLookup(connector *ibclientmock.MockConnector, err error, records ...ibclient.HostRecord) {
	connector.EXPECT().
		GetObject(gomock.AssignableToTypeOf(&ibclient.HostRecord{}), "", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ ibclient.IBObject, _ string, _ *ibclient.QueryParams, res any) error {
			*res.(*[]ibclient.HostRecord) = records
			return err
		})
}

// expectHostRecordFetch expects the fetch of a written host record by its ref and answers with the given addresses.
func expectHostRecordFetch(connector *ibclientmock.MockConnector, addrs ...string) {
	connector.EXPECT().
		GetObject(gomock.Any(), testRef, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ ibclient.IBObject, _ string, _ *ibclient.QueryParams, res any) error {
			*res.(*ibclient.HostRecord) = newHostRecord(addrs...)
			return nil
		})
}

func TestGetAllocatedHostRecordAddrInSubnet(t *testing.T) {
	invalid := newHostRecord()
	invalid.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{
		{Ipv4Addr: nil},
		{Ipv4Addr: ptr.To("not-an-ip")},
		{Ipv4Addr: ptr.To("10.0.0.7")},
	}
	tests := []struct {
		name   string
		record ibclient.HostRecord
		subnet netip.Prefix
		want   netip.Addr
	}{
		{name: "IPv4 address in subnet", record: newHostRecord("10.1.0.5", "10.0.0.5"), subnet: testSubnetV4, want: netip.MustParseAddr("10.0.0.5")},
		{name: "IPv6 address in subnet", record: newHostRecord("10.0.0.5", "fd00::5"), subnet: testSubnetV6, want: netip.MustParseAddr("fd00::5")},
		{name: "no address in subnet", record: newHostRecord("10.1.0.5", "fd01::5"), subnet: testSubnetV4},
		{name: "IPv6 address is ignored for an IPv4 subnet", record: newHostRecord("fd00::5"), subnet: testSubnetV4},
		{name: "empty and invalid addresses are skipped", record: invalid, subnet: testSubnetV4, want: netip.MustParseAddr("10.0.0.7")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(getAllocatedHostRecordAddrInSubnet(&tt.record, tt.subnet)).To(Equal(tt.want))
		})
	}
}

func TestPrepareHostRecordForUpdate(t *testing.T) {
	g := NewWithT(t)
	hr := newHostRecord("10.0.0.5")
	hr.Ipv4Addrs[0].Host = testHostname

	prepareHostRecordForUpdate(&hr)

	g.Expect(hr.Zone).To(BeEmpty())
	g.Expect(hr.NetworkView).To(BeEmpty())
	g.Expect(hr.View).To(BeNil())
	g.Expect(hr.Ipv4Addrs).To(HaveLen(1))
	g.Expect(hr.Ipv4Addrs[0].Host).To(BeEmpty())
	g.Expect(hr.Ipv6Addrs).NotTo(BeNil())
	g.Expect(hr.Ipv6Addrs).To(BeEmpty())
}

func TestToDNSView(t *testing.T) {
	g := NewWithT(t)

	g.Expect(toDNSView("")).To(BeNil())
	g.Expect(toDNSView("default")).To(Equal(ptr.To("default")))
}

func TestNextAvailableIBFunc(t *testing.T) {
	g := NewWithT(t)

	g.Expect(nextAvailableIBFunc(testSubnetV4, testNetworkView)).To(Equal("func:nextavailableip:10.0.0.0/24,my-view"))
}

func TestGetOrAllocateAddressReturnsExistingAddress(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	expectHostRecordLookup(connector, nil, newHostRecord("10.0.0.5"))

	addr, err := c.GetOrAllocateAddress(testNetworkView, "", testSubnetV4, testHostname, "", logr.Discard())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(addr).To(Equal(netip.MustParseAddr("10.0.0.5")))
}

func TestGetOrAllocateAddressCreatesHostRecord(t *testing.T) {
	tests := []struct {
		name      string
		lookupErr error
		dnsZone   string
		subnet    netip.Prefix
		allocated string
		wantDNS   bool
	}{
		{name: "typed not found error", lookupErr: ibclient.NewNotFoundError("not found"), subnet: testSubnetV4, allocated: "10.0.0.9"},
		{name: "empty result without error", subnet: testSubnetV4, allocated: "10.0.0.9"},
		{name: "IPv6 subnet", subnet: testSubnetV6, allocated: "fd00::9"},
		{name: "DNS zone enables DNS", dnsZone: "example.com", subnet: testSubnetV4, allocated: "10.0.0.9", wantDNS: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, connector, _ := newMockedClient(t)
			expectHostRecordLookup(connector, tt.lookupErr)
			connector.EXPECT().CreateObject(gomock.Any()).DoAndReturn(func(obj ibclient.IBObject) (string, error) {
				hr := obj.(*ibclient.HostRecord)
				g.Expect(hr.Name).To(Equal(ptr.To(testHostname)))
				g.Expect(hr.NetworkView).To(Equal(testNetworkView))
				g.Expect(hr.EnableDns).To(Equal(ptr.To(tt.wantDNS)))
				nextAvailable := nextAvailableIBFunc(tt.subnet, testNetworkView)
				if tt.subnet.Addr().Is4() {
					g.Expect(hr.Ipv4Addrs).To(ConsistOf(HaveField("Ipv4Addr", Equal(&nextAvailable))))
				} else {
					g.Expect(hr.Ipv6Addrs).To(ConsistOf(HaveField("Ipv6Addr", Equal(&nextAvailable))))
				}
				if tt.wantDNS {
					g.Expect(hr.View).To(Equal(ptr.To("dns-view")))
				}
				return testRef, nil
			})
			expectHostRecordFetch(connector, tt.allocated)

			addr, err := c.GetOrAllocateAddress(testNetworkView, "dns-view", tt.subnet, testHostname, tt.dnsZone, logr.Discard())

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(addr).To(Equal(netip.MustParseAddr(tt.allocated)))
		})
	}
}

func TestGetOrAllocateAddressAddsAddressToExistingHostRecord(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	expectHostRecordLookup(connector, nil, newHostRecord("fd00::5"))
	connector.EXPECT().UpdateObject(gomock.Any(), testRef).DoAndReturn(func(obj ibclient.IBObject, _ string) (string, error) {
		hr := obj.(*ibclient.HostRecord)
		g.Expect(hr.NetworkView).To(BeEmpty(), "Infoblox rejects updates of the network view")
		g.Expect(hr.Zone).To(BeEmpty())
		g.Expect(hr.View).To(BeNil())
		g.Expect(hr.Ipv4Addrs).To(HaveLen(1))
		g.Expect(hr.Ipv6Addrs).To(HaveLen(1))
		return testRef, nil
	})
	expectHostRecordFetch(connector, "fd00::5", "10.0.0.9")

	addr, err := c.GetOrAllocateAddress(testNetworkView, "", testSubnetV4, testHostname, "", logr.Discard())

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(addr).To(Equal(netip.MustParseAddr("10.0.0.9")))
}

func TestGetOrAllocateAddressFailsForMultipleHostRecords(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	expectHostRecordLookup(connector, nil, newHostRecord("10.0.0.5"), newHostRecord("10.0.0.6"))

	_, err := c.GetOrAllocateAddress(testNetworkView, "", testSubnetV4, testHostname, "", logr.Discard())

	g.Expect(err).To(MatchError(ContainSubstring(`multiple (2) host records found for hostname "machine-1.example.com" in network view "my-view"`)))
}

func TestGetOrAllocateAddressReportsLookupError(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	expectHostRecordLookup(connector, rawWapiError(400, "Bad Request", wapiErrorContents))

	_, err := c.GetOrAllocateAddress(testNetworkView, "", testSubnetV4, testHostname, "", logr.Discard())

	var reqErr RequestError
	g.Expect(errors.As(err, &reqErr)).To(BeTrue())
	g.Expect(reqErr.Operation).To(Equal("GetHostRecord"))
	g.Expect(reqErr.Params).To(Equal(map[string]string{"name": testHostname, "network_view": testNetworkView}),
		"query internals like _return_fields only clutter the message")
	var wapiErr WapiError
	g.Expect(errors.As(err, &wapiErr)).To(BeTrue())
	g.Expect(wapiErr.Code).To(Equal("Client.Ibap.Proto"))
}

func TestReleaseAddressFailsForMultipleHostRecords(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	expectHostRecordLookup(connector, nil, newHostRecord("10.0.0.5"), newHostRecord("10.0.0.6"))

	err := c.ReleaseAddress(testNetworkView, "", testSubnetV4, testHostname, logr.Discard())

	// Deleting or updating one of the duplicates could release an address another claim still uses.
	g.Expect(err).To(MatchError(ContainSubstring(`multiple (2) host records found for hostname "machine-1.example.com" in network view "my-view"`)))
}

func TestGetOrAllocateAddressReportsWriteErrors(t *testing.T) {
	tests := []struct {
		name          string
		existing      []ibclient.HostRecord
		expectWrite   func(connector *ibclientmock.MockConnector)
		wantOperation string
		wantRef       string
	}{
		{
			name: "create fails",
			expectWrite: func(connector *ibclientmock.MockConnector) {
				connector.EXPECT().CreateObject(gomock.Any()).Return("", errors.New("boom"))
			},
			wantOperation: "CreateHostRecord",
		},
		{
			name:     "update fails",
			existing: []ibclient.HostRecord{newHostRecord("fd00::5")},
			expectWrite: func(connector *ibclientmock.MockConnector) {
				connector.EXPECT().UpdateObject(gomock.Any(), testRef).Return("", errors.New("boom"))
			},
			wantOperation: "UpdateHostRecord",
			wantRef:       testRef,
		},
		{
			name: "fetch after create fails",
			expectWrite: func(connector *ibclientmock.MockConnector) {
				connector.EXPECT().CreateObject(gomock.Any()).Return(testRef, nil)
				connector.EXPECT().GetObject(gomock.Any(), testRef, gomock.Any(), gomock.Any()).Return(errors.New("boom"))
			},
			wantOperation: "GetHostRecordByRef",
			wantRef:       testRef,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, connector, _ := newMockedClient(t)
			expectHostRecordLookup(connector, nil, tt.existing...)
			tt.expectWrite(connector)

			_, err := c.GetOrAllocateAddress(testNetworkView, "", testSubnetV4, testHostname, "", logr.Discard())

			var reqErr RequestError
			g.Expect(errors.As(err, &reqErr)).To(BeTrue())
			g.Expect(reqErr.Operation).To(Equal(tt.wantOperation))
			g.Expect(reqErr.Params).To(Equal(map[string]string{"name": testHostname, "ref": tt.wantRef}))
			g.Expect(err).To(MatchError(ContainSubstring("boom")))
		})
	}
}

func TestGetOrAllocateAddressFailsIfNoAddressWasAllocated(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	expectHostRecordLookup(connector, nil)
	connector.EXPECT().CreateObject(gomock.Any()).Return(testRef, nil)
	expectHostRecordFetch(connector, "10.1.0.9")

	_, err := c.GetOrAllocateAddress(testNetworkView, "", testSubnetV4, testHostname, "", logr.Discard())

	g.Expect(err).To(MatchError(ContainSubstring("does not contain a matching IP address")))
}

func TestReleaseAddressDoesNothingWithoutMatchingAddress(t *testing.T) {
	tests := []struct {
		name      string
		lookupErr error
		records   []ibclient.HostRecord
	}{
		{name: "host record not found", lookupErr: ibclient.NewNotFoundError("not found")},
		{name: "empty result without error"},
		{name: "address in another subnet", records: []ibclient.HostRecord{newHostRecord("10.1.0.5")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, connector, _ := newMockedClient(t)
			expectHostRecordLookup(connector, tt.lookupErr, tt.records...)

			g.Expect(c.ReleaseAddress(testNetworkView, "", testSubnetV4, testHostname, logr.Discard())).To(Succeed())
		})
	}
}

func TestReleaseAddressFailsFor404WithoutWapiBody(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	// A 404 from a proxy must not count as "no host record", otherwise the release reports success and the address leaks.
	expectHostRecordLookup(connector, ibclient.NewNotFoundError(rawWapiError(404, "Not Found", "<html>not found</html>").Error()))

	err := c.ReleaseAddress(testNetworkView, "", testSubnetV4, testHostname, logr.Discard())

	var reqErr RequestError
	g.Expect(errors.As(err, &reqErr)).To(BeTrue())
	g.Expect(reqErr.Operation).To(Equal("GetHostRecord"))
	var httpErr HTTPError
	g.Expect(errors.As(err, &httpErr)).To(BeTrue())
	g.Expect(httpErr.StatusCode).To(Equal(404))
	g.Expect(IsWapiError(err)).To(BeFalse())
	g.Expect(IsNotFoundError(err)).To(BeFalse())
	g.Expect(err).To(MatchError(`failed to get Infoblox host record: infoblox "ib.example.com:8443" GetHostRecord ` +
		`[name="machine-1.example.com" network_view="my-view"]: HTTP error 404 (Not Found)`))
}

func TestReleaseAddressDeletesHostRecordWithoutRemainingAddresses(t *testing.T) {
	g := NewWithT(t)
	c, connector, _ := newMockedClient(t)
	expectHostRecordLookup(connector, nil, newHostRecord("10.0.0.5"))
	connector.EXPECT().DeleteObject(testRef).Return(testRef, nil)

	g.Expect(c.ReleaseAddress(testNetworkView, "", testSubnetV4, testHostname, logr.Discard())).To(Succeed())
}

func TestReleaseAddressKeepsRemainingAddresses(t *testing.T) {
	tests := []struct {
		name   string
		subnet netip.Prefix
		wantV4 int
		wantV6 int
	}{
		{name: "release IPv4 address", subnet: testSubnetV4, wantV4: 1, wantV6: 1},
		{name: "release IPv6 address", subnet: testSubnetV6, wantV4: 2, wantV6: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, connector, _ := newMockedClient(t)
			expectHostRecordLookup(connector, nil, newHostRecord("10.0.0.5", "10.1.0.5", "fd00::5"))
			connector.EXPECT().UpdateObject(gomock.Any(), testRef).DoAndReturn(func(obj ibclient.IBObject, _ string) (string, error) {
				hr := obj.(*ibclient.HostRecord)
				g.Expect(hr.Ipv4Addrs).To(HaveLen(tt.wantV4))
				g.Expect(hr.Ipv6Addrs).To(HaveLen(tt.wantV6))
				g.Expect(hr.Ipv6Addrs).NotTo(BeNil(), "Infoblox rejects null address lists")
				g.Expect(hr.NetworkView).To(BeEmpty())
				return testRef, nil
			})

			g.Expect(c.ReleaseAddress(testNetworkView, "", tt.subnet, testHostname, logr.Discard())).To(Succeed())
		})
	}
}

func TestReleaseAddressReportsErrors(t *testing.T) {
	tests := []struct {
		name          string
		records       []ibclient.HostRecord
		lookupErr     error
		expectWrite   func(connector *ibclientmock.MockConnector)
		wantOperation string
	}{
		{
			name:          "lookup fails",
			lookupErr:     errors.New("boom"),
			wantOperation: "GetHostRecord",
		},
		{
			name:    "delete fails",
			records: []ibclient.HostRecord{newHostRecord("10.0.0.5")},
			expectWrite: func(connector *ibclientmock.MockConnector) {
				connector.EXPECT().DeleteObject(testRef).Return("", errors.New("boom"))
			},
			wantOperation: "DeleteHostRecord",
		},
		{
			name:    "update fails",
			records: []ibclient.HostRecord{newHostRecord("10.0.0.5", "fd00::5")},
			expectWrite: func(connector *ibclientmock.MockConnector) {
				connector.EXPECT().UpdateObject(gomock.Any(), testRef).Return("", errors.New("boom"))
			},
			wantOperation: "UpdateHostRecord",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c, connector, _ := newMockedClient(t)
			expectHostRecordLookup(connector, tt.lookupErr, tt.records...)
			if tt.expectWrite != nil {
				tt.expectWrite(connector)
			}

			err := c.ReleaseAddress(testNetworkView, "", testSubnetV4, testHostname, logr.Discard())

			var reqErr RequestError
			g.Expect(errors.As(err, &reqErr)).To(BeTrue())
			g.Expect(reqErr.Operation).To(Equal(tt.wantOperation))
			g.Expect(reqErr.Params).To(HaveKeyWithValue("name", testHostname))
			g.Expect(err).To(MatchError(ContainSubstring("boom")))
		})
	}
}
