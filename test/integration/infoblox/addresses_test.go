//go:build integration

package infoblox_test

import (
	"net/netip"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/pkg/infoblox"
	"k8s.io/utils/ptr"
)

const dnsEnabled = true

// addressesOf returns all addresses of a host record, parsed so that IPv6 notations compare equal.
func addressesOf(hr *ibclient.HostRecord) []netip.Addr {
	var addrs []netip.Addr
	for _, a := range hr.Ipv4Addrs {
		if a.Ipv4Addr != nil {
			addrs = append(addrs, netip.MustParseAddr(*a.Ipv4Addr))
		}
	}
	for _, a := range hr.Ipv6Addrs {
		if a.Ipv6Addr != nil {
			addrs = append(addrs, netip.MustParseAddr(*a.Ipv6Addr))
		}
	}
	return addrs
}

// inSubnet matches an address that is part of the given subnet.
func inSubnet(subnet netip.Prefix) OmegaMatcher {
	return Satisfy(subnet.Contains)
}

// getHostRecord reads a host record straight from Infoblox.
func getHostRecord(ref string) *ibclient.HostRecord {
	hr, err := ibObjMgr.GetHostRecordByRef(ref)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, hr).NotTo(BeNil())
	return hr
}

// expectHostRecordGone asserts that Infoblox reports the host record as not found.
func expectHostRecordGone(ref string) {
	_, err := ibObjMgr.GetHostRecordByRef(ref)
	ExpectWithOffset(1, infoblox.IsNotFoundError(err)).To(BeTrue(), "expected the host record to be gone, got %v", err)
}

// createHostRecord creates a host record with the next available address in each subnet.
func createHostRecord(hostname string, subnets ...netip.Prefix) *ibclient.HostRecord {
	hr := ibclient.NewEmptyHostRecord()
	hr.Name = ptr.To(hostname)
	hr.NetworkView = testView
	hr.View = ptr.To(testView)
	hr.EnableDns = ptr.To(dnsEnabled)
	hr.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{}
	hr.Ipv6Addrs = []ibclient.HostRecordIpv6Addr{}
	for _, subnet := range subnets {
		if subnet.Addr().Is4() {
			hr.Ipv4Addrs = append(hr.Ipv4Addrs, *ibclient.NewHostRecordIpv4Addr(nextAvailableIP(subnet, testView), "", false, ""))
		} else {
			hr.Ipv6Addrs = append(hr.Ipv6Addrs, *ibclient.NewHostRecordIpv6Addr(nextAvailableIP(subnet, testView), "", false, ""))
		}
	}
	ref, err := ibConnector.CreateObject(hr)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	created := getHostRecord(ref)
	ExpectWithOffset(1, addressesOf(created)).To(HaveLen(len(subnets)))
	return created
}

var _ = Describe("IP Address Management", func() {
	var hostname string
	logger := GinkgoLogr

	BeforeEach(func() {
		hostname = "testmachine-1." + domain
	})

	When("no host record exists", func() {
		AfterEach(func() {
			hr, err := ibObjMgr.GetHostRecord("", "", hostname, "", "")
			if infoblox.IsNotFoundError(err) || (err == nil && hr == nil) {
				return
			}
			Expect(err).NotTo(HaveOccurred())
			_, err = ibObjMgr.DeleteHostRecord(hr.Ref)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("creates a new host record and allocates an IP",
			func(subnet func() netip.Prefix) {
				addr, err := testClient.GetOrAllocateAddress(testView, testView, subnet(), hostname, "", logger)
				Expect(err).NotTo(HaveOccurred())
				Expect(subnet().Contains(addr)).To(BeTrue(), "expected %s to be in %s", addr, subnet())

				hr, err := ibObjMgr.GetHostRecord("", "", hostname, "", "")
				Expect(err).NotTo(HaveOccurred())
				Expect(hr).NotTo(BeNil())
				Expect(addressesOf(hr)).To(ConsistOf(addr))
			},
			Entry("IPv4", func() netip.Prefix { return v4subnet1 }),
			Entry("IPv6", func() netip.Prefix { return v6subnet1 }),
		)

		It("does nothing when releasing an address", func() {
			Expect(testClient.ReleaseAddress(testView, testView, v4subnet1, hostname, logger)).To(Succeed())

			_, err := ibObjMgr.GetHostRecord("", "", hostname, "", "")
			Expect(infoblox.IsNotFoundError(err)).To(BeTrue(), "expected no host record, got %v", err)
		})
	})

	When("a host record with one address exists", func() {
		var hostRecord *ibclient.HostRecord
		var hrDeleted bool
		BeforeEach(func() {
			hostRecord = nil
			hrDeleted = false
		})
		AfterEach(func() {
			if hostRecord != nil && !hrDeleted {
				_, err := ibObjMgr.DeleteHostRecord(hostRecord.Ref)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		DescribeTable("allocating",
			func(recordSubnet, requestSubnet func() netip.Prefix, wantExisting bool) {
				hostRecord = createHostRecord(hostname, recordSubnet())
				existing := addressesOf(hostRecord)

				addr, err := testClient.GetOrAllocateAddress(testView, testView, requestSubnet(), hostname, "", logger)

				Expect(err).NotTo(HaveOccurred())
				if wantExisting {
					Expect(existing).To(ConsistOf(addr))
					Expect(addressesOf(getHostRecord(hostRecord.Ref))).To(ConsistOf(existing))
					return
				}
				Expect(requestSubnet().Contains(addr)).To(BeTrue(), "expected %s to be in %s", addr, requestSubnet())
				Expect(addressesOf(getHostRecord(hostRecord.Ref))).To(ConsistOf(existing[0], addr))
			},
			Entry("returns the existing IPv4 address for the same subnet",
				func() netip.Prefix { return v4subnet1 }, func() netip.Prefix { return v4subnet1 }, true),
			Entry("adds an IPv4 address for a different subnet",
				func() netip.Prefix { return v4subnet1 }, func() netip.Prefix { return v4subnet2 }, false),
			Entry("adds an IPv6 address to an IPv4 record",
				func() netip.Prefix { return v4subnet1 }, func() netip.Prefix { return v6subnet1 }, false),
			Entry("returns the existing IPv6 address for the same subnet",
				func() netip.Prefix { return v6subnet1 }, func() netip.Prefix { return v6subnet1 }, true),
			Entry("adds an IPv6 address for a different subnet",
				func() netip.Prefix { return v6subnet1 }, func() netip.Prefix { return v6subnet2 }, false),
			Entry("adds an IPv4 address to an IPv6 record",
				func() netip.Prefix { return v6subnet1 }, func() netip.Prefix { return v4subnet1 }, false),
		)

		DescribeTable("deletes the host record when releasing its only address",
			func(subnet func() netip.Prefix) {
				hostRecord = createHostRecord(hostname, subnet())

				Expect(testClient.ReleaseAddress(testView, testView, subnet(), hostname, logger)).To(Succeed())

				expectHostRecordGone(hostRecord.Ref)
				hrDeleted = true
			},
			Entry("IPv4", func() netip.Prefix { return v4subnet1 }),
			Entry("IPv6", func() netip.Prefix { return v6subnet1 }),
		)

		DescribeTable("does not change the host record when releasing an address in a different subnet",
			func(recordSubnet, releaseSubnet func() netip.Prefix) {
				hostRecord = createHostRecord(hostname, recordSubnet())

				Expect(testClient.ReleaseAddress(testView, testView, releaseSubnet(), hostname, logger)).To(Succeed())

				Expect(addressesOf(getHostRecord(hostRecord.Ref))).To(ConsistOf(addressesOf(hostRecord)))
			},
			Entry("IPv4", func() netip.Prefix { return v4subnet1 }, func() netip.Prefix { return v4subnet2 }),
			Entry("IPv6", func() netip.Prefix { return v6subnet1 }, func() netip.Prefix { return v6subnet2 }),
		)
	})

	When("a host record with multiple addresses exists", func() {
		var hostRecord *ibclient.HostRecord
		BeforeEach(func() {
			hostRecord = nil
		})
		AfterEach(func() {
			if hostRecord == nil {
				return
			}
			_, err := ibObjMgr.DeleteHostRecord(hostRecord.Ref)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("keeps the host record and only removes the released address",
			func(recordSubnets func() []netip.Prefix, releaseSubnet func() netip.Prefix) {
				hostRecord = createHostRecord(hostname, recordSubnets()...)

				Expect(testClient.ReleaseAddress(testView, testView, releaseSubnet(), hostname, logger)).To(Succeed())

				remaining := addressesOf(getHostRecord(hostRecord.Ref))
				Expect(remaining).To(HaveLen(len(recordSubnets()) - 1))
				Expect(remaining).NotTo(ContainElement(inSubnet(releaseSubnet())))
			},
			Entry("IPv4 record",
				func() []netip.Prefix { return []netip.Prefix{v4subnet1, v4subnet2} }, func() netip.Prefix { return v4subnet1 }),
			Entry("IPv6 record",
				func() []netip.Prefix { return []netip.Prefix{v6subnet1, v6subnet2} }, func() netip.Prefix { return v6subnet1 }),
			Entry("mixed record, releasing IPv4",
				func() []netip.Prefix { return []netip.Prefix{v4subnet1, v6subnet1} }, func() netip.Prefix { return v4subnet1 }),
			Entry("mixed record, releasing IPv6",
				func() []netip.Prefix { return []netip.Prefix{v4subnet1, v6subnet1} }, func() netip.Prefix { return v6subnet1 }),
		)
	})
})
