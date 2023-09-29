package infoblox

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	testClient *client
	testDomain string
	testView   string

	v4testIBNetwork    *ibclient.NetworkContainer
	v4subnet1IBNetwork *ibclient.Network
	v4subnet2IBNetwork *ibclient.Network
	v4subnet1          netip.Prefix
	v4subnet2          netip.Prefix

	v6testIBNetwork    *ibclient.NetworkContainer
	v6subnet1IBNetwork *ibclient.Network
	v6subnet2IBNetwork *ibclient.Network
	v6subnet1          netip.Prefix
	v6subnet2          netip.Prefix

	domain string
)

func TestInfoblox(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Infoblox")
	gomock.Any()
}

var _ = BeforeSuite(func() {
	testView = getInfobloxTestEnvVar("network_view", "default")
	testNetworkIPv4 := netip.MustParsePrefix(getInfobloxTestEnvVar("v4network", "192.168.200.0/24"))
	testNetworkIPv6 := netip.MustParsePrefix(getInfobloxTestEnvVar("v6network", "fdf0:9824:ab5c:6f73:0000:0000:0000:0000/120"))

	config, err := InfobloxConfigFromEnv()
	Expect(err).NotTo(HaveOccurred())

	iClient, err := NewClient(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(iClient).NotTo(BeNil())

	var ok bool
	testClient, ok = iClient.(*client)
	Expect(ok).To(BeTrue())

	exists, err := testClient.CheckNetworkViewExists(testView)
	Expect(err).ToNot(HaveOccurred())

	if !exists {
		nw, err := testClient.objMgr.CreateNetworkView(testView, "", ibclient.EA{})
		Expect(err).ToNot(HaveOccurred())
		Expect(nw).ToNot(BeNil())
	}

	v4testIBNetwork, err := allocateNetworkContainer(testNetworkIPv4.String(), false)
	Expect(err).ToNot(HaveOccurred())
	Expect(v4testIBNetwork).ToNot(BeNil())

	v4subnet1IBNetwork, v4subnet1 = allocateNetwork(v4testIBNetwork.Cidr, 28, false)
	v4subnet2IBNetwork, v4subnet2 = allocateNetwork(v4testIBNetwork.Cidr, 28, false)

	v6testIBNetwork, err = allocateNetworkContainer(testNetworkIPv6.String(), true)
	Expect(err).ToNot(HaveOccurred())
	Expect(v6testIBNetwork).ToNot(BeNil())
	v6subnet1IBNetwork, v6subnet1 = allocateNetwork(v6testIBNetwork.Cidr, 124, true)
	v6subnet2IBNetwork, v6subnet2 = allocateNetwork(v6testIBNetwork.Cidr, 124, true)

	v4testNetworkAddr := strings.Split(v4testIBNetwork.Cidr, "/")[0]
	Expect(v4testNetworkAddr).NotTo(BeEmpty())
	domain = strings.ReplaceAll(strings.ReplaceAll(v4testNetworkAddr, ".", ""), ":", "") + ".capi-ipam.telekom.test"
})

func allocateNetwork(cidr string, prefix uint, isIPv6 bool) (*ibclient.Network, netip.Prefix) {
	ibNetwork, err := testClient.objMgr.AllocateNetwork(testView, cidr, isIPv6, prefix, "", ibclient.EA{})
	Expect(err).NotTo(HaveOccurred())
	Expect(ibNetwork).NotTo(BeNil())
	p, err := netip.ParsePrefix(ibNetwork.Cidr)
	Expect(err).NotTo(HaveOccurred())
	return ibNetwork, p
}

func allocateNetworkContainer(cidr string, isIPv6 bool) (*ibclient.NetworkContainer, error) {
	networkContainer, err := testClient.objMgr.GetNetworkContainer(testView, cidr, isIPv6, ibclient.EA{})

	if networkContainer == nil {
		networkContainer, err = testClient.objMgr.CreateNetworkContainer(testView, cidr, isIPv6, "", ibclient.EA{})
	}

	return networkContainer, err
}

// func allocateV4(cidr string, prefix uint) (*ibclient.Network, netip.Prefix) {
// 	ibNetwork, err := testClient.objMgr.AllocateNetwork(testView, cidr, false, prefix, "", ibclient.EA{})
// 	Expect(err).NotTo(HaveOccurred())
// 	Expect(ibNetwork).NotTo(BeNil())
// 	p, err := netip.ParsePrefix(ibNetwork.Cidr)
// 	Expect(err).NotTo(HaveOccurred())
// 	return ibNetwork, p
// }

// func allocateV6(cidr string, prefix uint) (*ibclient.Network, netip.Prefix) {
// 	ibNetwork, err := testClient.objMgr.AllocateNetwork(testView, cidr, true, prefix, "", ibclient.EA{})
// 	Expect(err).NotTo(HaveOccurred())
// 	Expect(ibNetwork).NotTo(BeNil())
// 	p, err := netip.ParsePrefix(ibNetwork.Cidr)
// 	Expect(err).NotTo(HaveOccurred())
// 	return ibNetwork, p
// }

var _ = AfterSuite(func() {
	// Infoblox turns networks into network containers when creating subnets in them, so we need to delete the network container
	nc, err := testClient.objMgr.GetNetworkContainer(testView, v4testIBNetwork.Cidr, false, ibclient.EA{})
	Expect(err).NotTo(HaveOccurred())
	Expect(nc).NotTo(BeNil())
	_, err = testClient.objMgr.DeleteNetworkContainer(nc.Ref)
	Expect(err).NotTo(HaveOccurred())

	nc, err = testClient.objMgr.GetNetworkContainer(testView, v6testIBNetwork.Cidr, true, ibclient.EA{})
	Expect(err).NotTo(HaveOccurred())
	Expect(nc).NotTo(BeNil())
	_, err = testClient.objMgr.DeleteNetworkContainer(nc.Ref)
	Expect(err).NotTo(HaveOccurred())
})
