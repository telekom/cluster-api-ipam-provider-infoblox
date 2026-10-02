//go:build infoblox

package infoblox_test

import (
	"errors"
	"net/netip"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
)

var _ = Describe("Infoblox Client", func() {
	// We don't need to test basic client creation, since it's already tested in BeforeSuite.

	Context("CheckNetworkExists", func() {
		It("returns true for an existing network", func() {
			exists, err := testClient.CheckNetworkExists(testView, v4subnet1)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
		})

		It("returns false for a network that does not exist", func() {
			exists, err := testClient.CheckNetworkExists(testView, netip.MustParsePrefix("192.168.222.0/28"))
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		})
	})

	// These pin down how the live WAPI reports missing objects, which the unit tests can only assume.
	Context("CheckNetworkViewExists", func() {
		It("returns true for an existing network view", func() {
			exists, err := testClient.CheckNetworkViewExists(testView)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
		})

		It("returns false for a network view that does not exist", func() {
			exists, err := testClient.CheckNetworkViewExists(testView + "-does-not-exist")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		})
	})

	Context("CheckDNSViewExists", func() {
		It("returns false for a DNS view that does not exist", func() {
			exists, err := testClient.CheckDNSViewExists(testView + "-does-not-exist")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		})

		It("reports a missing name as a request error instead of a missing view", func() {
			exists, err := testClient.CheckDNSViewExists("")
			Expect(exists).To(BeFalse())
			var reqErr infoblox.RequestError
			Expect(errors.As(err, &reqErr)).To(BeTrue(), "expected a RequestError, got %v", err)
			Expect(reqErr.Operation).To(Equal("GetDNSView"))
			Expect(err).To(MatchError(ContainSubstring("DNS view's name is required")))
		})
	})

	Context("error reporting", func() {
		It("reports a failing WAPI request with endpoint, operation and parsed WAPI error", func() {
			_, err := testClient.GetOrAllocateAddress(testView, testView, netip.MustParsePrefix("192.168.222.0/28"),
				"unallocatable."+domain, "", GinkgoLogr)

			var reqErr infoblox.RequestError
			Expect(errors.As(err, &reqErr)).To(BeTrue(), "expected a RequestError, got %v", err)
			Expect(reqErr.Operation).To(Equal("CreateHostRecord"))
			Expect(reqErr.Endpoint).To(HaveSuffix(":" + getInfobloxTestEnvVar("port", "443")))
			var wapiErr infoblox.WapiError
			Expect(errors.As(err, &wapiErr)).To(BeTrue(), "expected a parsed WAPI error, got %v", err)
			Expect(wapiErr.StatusCode).To(BeNumerically(">=", 400), "a network that does not exist is a client error")
			Expect(wapiErr.StatusCode).To(BeNumerically("<", 500), "a network that does not exist is a client error")
			Expect(wapiErr.Message).NotTo(BeEmpty())
		})
	})
})
