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

	v4testIBNetwork    *ibclient.Network
	v4subnet1IBNetwork *ibclient.Network
	v4subnet2IBNetwork *ibclient.Network
	v4subnet1          netip.Prefix
	v4subnet2          netip.Prefix

	v6testIBNetwork    *ibclient.Network
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
	testNetwork := netip.MustParsePrefix(getInfobloxTestEnvVar("v4network", ""))

	hostCfg, authCfg, err := InfobloxConfigFromEnv()
	Expect(err).NotTo(HaveOccurred())

	testClient, err = newClient(hostCfg, authCfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(testClient).NotTo(BeNil())

	v4testIBNetwork, _ = allocateV4(testNetwork.String(), 24)
	v4subnet1IBNetwork, v4subnet1 = allocateV4(v4testIBNetwork.Cidr, 28)
	v4subnet2IBNetwork, v4subnet2 = allocateV4(v4testIBNetwork.Cidr, 28)

	v6testIBNetwork, _ = allocateV6(getInfobloxTestEnvVar("v6network", ""), 120)
	v6subnet1IBNetwork, v6subnet1 = allocateV6(v6testIBNetwork.Cidr, 124)
	v6subnet2IBNetwork, v6subnet2 = allocateV6(v6testIBNetwork.Cidr, 124)

	v4testNetworkAddr := strings.Split(v4testIBNetwork.Cidr, "/")[0]
	Expect(v4testNetworkAddr).NotTo(BeEmpty())
	domain = strings.ReplaceAll(strings.ReplaceAll(v4testNetworkAddr, ".", ""), ":", "") + ".capi-ipam.telekom.test"
})

func allocateV4(cidr string, prefix uint) (*ibclient.Network, netip.Prefix) {
	ibNetwork, err := testClient.objMgr.AllocateNetwork(testView, cidr, false, prefix, "", ibclient.EA{})
	Expect(err).NotTo(HaveOccurred())
	Expect(ibNetwork).NotTo(BeNil())
	p, err := netip.ParsePrefix(ibNetwork.Cidr)
	Expect(err).NotTo(HaveOccurred())
	return ibNetwork, p
}

func allocateV6(cidr string, prefix uint) (*ibclient.Network, netip.Prefix) {
	ibNetwork, err := testClient.objMgr.AllocateNetwork(testView, cidr, true, prefix, "", ibclient.EA{})
	Expect(err).NotTo(HaveOccurred())
	Expect(ibNetwork).NotTo(BeNil())
	p, err := netip.ParsePrefix(ibNetwork.Cidr)
	Expect(err).NotTo(HaveOccurred())
	return ibNetwork, p
}

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
