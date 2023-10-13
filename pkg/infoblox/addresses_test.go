package infoblox

import (
	"log"
	"net/netip"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const dnsEnabled = false

var _ = Describe("IP Address Management", func() {
	var hostname string

	BeforeEach(func() {
		hostname = "testmachine-1." + domain
	})

	When("no host record exists", func() {
		AfterEach(func() {
			hr, err := testClient.objMgr.GetHostRecord("", "", hostname, "", "")
			if err != nil {
				_, ok := err.(*ibclient.NotFoundError)
				if ok {
					return
				}
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(hr).NotTo(BeNil())
			_, err = testClient.objMgr.DeleteHostRecord(hr.Ref)
			Expect(err).NotTo(HaveOccurred())
		})
		Context("IPv4", func() {
			It("creates a new host record and allocates an IP", func() {
				addr, err := testClient.GetOrAllocateAddress(testView, v4subnet1, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(v4subnet1.Contains(addr)).To(BeTrue())
			})
		})
		Context("IPv6", func() {
			It("creates a new host record and allocates an IP", func() {
				addr, err := testClient.GetOrAllocateAddress(testView, v6subnet1, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(v6subnet1.Contains(addr)).To(BeTrue())
			})
		})
	})

	When("a host record with one address exists", func() {
		var hostRecord *ibclient.HostRecord
		var hrDeleted bool
		BeforeEach(func() {
			hrDeleted = false
		})
		AfterEach(func() {
			if !hrDeleted {
				_, err := testClient.objMgr.DeleteHostRecord(hostRecord.Ref)
				Expect(err).NotTo(HaveOccurred())
			} else {
				_, err := testClient.objMgr.GetHostRecordByRef(hostRecord.Ref)
				Expect(err).To(HaveOccurred())
				_, ok := err.(*ibclient.NotFoundError)
				if !ok {
					logger := log.Default()
					logger.Printf("Not not found error: %s\n", err.Error())
				}
				Expect(ok).To(BeTrue())
			}
		})

		Context("IPv4 record", func() {
			BeforeEach(func() {
				var err error
				hostRecord, err = testClient.objMgr.CreateHostRecord(dnsEnabled, false, hostname, testView, toDNSView(testView), v4subnet1.String(), "", "", "", "", "", false, 0, "", ibclient.EA{}, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})

			It("returns the existing IP if the subnet is the same", func() {
				addr, err := testClient.GetOrAllocateAddress(testView, v4subnet1, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(addr.String()).To(Equal(hostRecord.Ipv4Addrs[0].Ipv4Addr))
			})

			It("allocates another IP if the subnet is different", func() {
				Expect(testView).To(Equal(defaultView))
				addr, err := testClient.GetOrAllocateAddress(testView, v4subnet2, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(v4subnet2.Contains(addr)).To(BeTrue())
			})

			It("allocates an IPv6 address if the subnet is IPv6", func() {
				addr, err := testClient.GetOrAllocateAddress(testView, v6subnet1, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(v6subnet1.Contains(addr)).To(BeTrue())
			})

			It("deletes the host record when releasing the address", func() {
				err := testClient.ReleaseAddress(testView, v4subnet1, hostname)
				Expect(err).NotTo(HaveOccurred())
				hrDeleted = true
			})

			It("doesnt change the host record when releasing an address in a different subnet", func() {
				err := testClient.ReleaseAddress(testView, v4subnet2, hostname)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("IPv6 record", func() {
			BeforeEach(func() {
				var err error
				hostRecord, err = testClient.objMgr.CreateHostRecord(dnsEnabled, false, hostname, testView, toDNSView(testView), "", v6subnet1.String(), "", "", "", "", false, 0, "", ibclient.EA{}, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})

			It("returns the existing IP if the subnet is the same", func() {
				addr, err := testClient.GetOrAllocateAddress(testView, v6subnet1, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(addr).To(Equal(netip.MustParseAddr(hostRecord.Ipv6Addrs[0].Ipv6Addr)))
			})

			It("allocates another IP if the subnet is different", func() {
				addr, err := testClient.GetOrAllocateAddress(testView, v6subnet2, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(v6subnet2.Contains(addr)).To(BeTrue())
			})

			It("allocates an IPv4 address if the subnet is IPv4", func() {
				addr, err := testClient.GetOrAllocateAddress(testView, v4subnet1, hostname, "")
				Expect(err).NotTo(HaveOccurred())
				Expect(v4subnet1.Contains(addr)).To(BeTrue())
			})

			It("deletes the host record when releasing the address", func() {
				err := testClient.ReleaseAddress(testView, v6subnet1, hostname)
				Expect(err).NotTo(HaveOccurred())
				hrDeleted = true
			})

			It("doesnt change the host record when releasing an address in a different subnet", func() {
				err := testClient.ReleaseAddress(testView, v6subnet2, hostname)
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	When("a host record with multiple addresses exists", func() {
		var hostRecord *ibclient.HostRecord
		AfterEach(func() {
			_, err := testClient.objMgr.DeleteHostRecord(hostRecord.Ref)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("IPv4 record", func() {
			BeforeEach(func() {
				var err error
				hostRecord = ibclient.NewEmptyHostRecord()
				hostRecord.Name = hostname
				hostRecord.NetworkView = testView
				hostRecord.View = toDNSView(testView)
				hostRecord.EnableDns = dnsEnabled
				hostRecord.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{
					*ibclient.NewHostRecordIpv4Addr(nextAvailableIBFunc(v4subnet1, testView), "", false, ""),
					*ibclient.NewHostRecordIpv4Addr(nextAvailableIBFunc(v4subnet2, testView), "", false, ""),
				}
				hostRecord.Ipv6Addrs = []ibclient.HostRecordIpv6Addr{}
				ref, err := testClient.connector.CreateObject(hostRecord)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
				hostRecord, err = testClient.objMgr.GetHostRecordByRef(ref)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})

			It("keeps the host record when releasing an address", func() {
				err := testClient.ReleaseAddress(testView, v4subnet1, hostname)
				Expect(err).NotTo(HaveOccurred())

				hostRecord, err := testClient.objMgr.GetHostRecordByRef(hostRecord.Ref)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})
		})

		Context("IPv6 record", func() {
			BeforeEach(func() {
				var err error
				hostRecord = ibclient.NewEmptyHostRecord()
				hostRecord.Name = hostname
				hostRecord.NetworkView = testView
				hostRecord.View = toDNSView(testView)
				hostRecord.EnableDns = dnsEnabled
				hostRecord.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{}
				hostRecord.Ipv6Addrs = []ibclient.HostRecordIpv6Addr{
					*ibclient.NewHostRecordIpv6Addr(nextAvailableIBFunc(v6subnet1, testView), "", false, ""),
					*ibclient.NewHostRecordIpv6Addr(nextAvailableIBFunc(v6subnet2, testView), "", false, ""),
				}
				ref, err := testClient.connector.CreateObject(hostRecord)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
				hostRecord, err = testClient.objMgr.GetHostRecordByRef(ref)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})

			It("keeps the host record when releasing an address", func() {
				err := testClient.ReleaseAddress(testView, v6subnet1, hostname)
				Expect(err).NotTo(HaveOccurred())

				hostRecord, err := testClient.objMgr.GetHostRecordByRef(hostRecord.Ref)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})
		})

		Context("Mixed record", func() {
			BeforeEach(func() {
				var err error
				hostRecord = ibclient.NewEmptyHostRecord()
				hostRecord.Name = hostname
				hostRecord.NetworkView = testView
				hostRecord.View = toDNSView(testView)
				hostRecord.EnableDns = dnsEnabled
				hostRecord.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{
					*ibclient.NewHostRecordIpv4Addr(nextAvailableIBFunc(v4subnet1, testView), "", false, ""),
				}
				hostRecord.Ipv6Addrs = []ibclient.HostRecordIpv6Addr{
					*ibclient.NewHostRecordIpv6Addr(nextAvailableIBFunc(v6subnet1, testView), "", false, ""),
				}
				ref, err := testClient.connector.CreateObject(hostRecord)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
				hostRecord, err = testClient.objMgr.GetHostRecordByRef(ref)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})

			It("keeps the host record when releasing a v4 address", func() {
				err := testClient.ReleaseAddress(testView, v4subnet1, hostname)
				Expect(err).NotTo(HaveOccurred())

				hostRecord, err := testClient.objMgr.GetHostRecordByRef(hostRecord.Ref)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})

			It("keeps the host record when releasing a v6 address", func() {
				err := testClient.ReleaseAddress(testView, v6subnet1, hostname)
				Expect(err).NotTo(HaveOccurred())

				hostRecord, err := testClient.objMgr.GetHostRecordByRef(hostRecord.Ref)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostRecord).NotTo(BeNil())
			})
		})
	})
})
