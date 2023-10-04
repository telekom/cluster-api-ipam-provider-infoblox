package infoblox

import (
	"net/netip"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Infoblox Client", func() {
	// We don't need to test basic client creation, since it's already tested in BeforeSuite.

	When("network view does exist", func() {
		Context("CheckNetworkExists", func() {
			It("should return true and no error", func() {
				addr, err := netip.ParseAddr("192.168.200.0")
				Expect(err).NotTo(HaveOccurred())
				exists, err := testClient.CheckNetworkExists(testView, netip.PrefixFrom(addr, 28))
				Expect(err).ToNot(HaveOccurred())
				Expect(exists).To(BeTrue())
			})
		})
		Context("CheckNetworkExists", func() {
			It("should return false and no error", func() {
				addr, err := netip.ParseAddr("192.168.222.0")
				Expect(err).NotTo(HaveOccurred())
				exists, err := testClient.CheckNetworkExists(testView, netip.PrefixFrom(addr, 28))
				Expect(err).ToNot(HaveOccurred())
				Expect(exists).To(BeFalse())
			})
		})
	})
})
