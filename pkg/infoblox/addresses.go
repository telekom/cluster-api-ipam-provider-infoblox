package infoblox

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/go-logr/logr"
	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	"k8s.io/utils/ptr"
)

// Known limitations:
// - Hostname must be a FQDN. We enable DNS for the host record, so Infoblox will return an error if the hostname is not a FQDN.
// - For some reason Infoblox does not assign network view to the host record. Workarounds were provided in the code with tag: [Issue].

// getOrNewHostRecord returns the host record with the given hostname in the given view, or creates a new host record if no host record with the given hostname exists.
func (c *client) getOrNewHostRecord(networkView, dnsView, hostname, zone string) (*ibclient.HostRecord, error) {
	// [Issue] For some reason Infoblox does not assign view to the host record. Empty netview and dnsview is a workaround to find host.
	hostRecord, err := c.objMgr.GetHostRecord("", "", hostname, "", "")
	if err != nil {
		// since ibclient.NotFoundError has a pointer receiver on it's Error() method, we can't use errors.As() here.
		if _, ok := err.(*ibclient.NotFoundError); !ok {
			return nil, err
		}
	}

	if hostRecord == nil {
		hostRecord = ibclient.NewEmptyHostRecord()
		hostRecord.Name = &hostname
		hostRecord.NetworkView = networkView
		hostRecord.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{}
		hostRecord.Ipv6Addrs = []ibclient.HostRecordIpv6Addr{}
		hostRecord.EnableDns = ptr.To(false)
		if zone != "" {
			hostRecord.EnableDns = ptr.To(true)
			hostRecord.View = toDNSView(dnsView)
		}
	}

	return hostRecord, nil
}

// createOrUpdateHostRecord creates or updates a host record and then fetches the updated record.
func (c *client) createOrUpdateHostRecord(hr *ibclient.HostRecord, logger logr.Logger) error {
	ref := ""
	var err error
	if hr.Ref == "" {
		logger.Info("Creating Infoblox host record", "hostname", *hr.Name)
		ref, err = c.connector.CreateObject(hr)
	} else {
		prepareHostRecordForUpdate(hr)
		logger.Info("Updating Infoblox host record", "hostname", *hr.Name)
		ref, err = c.connector.UpdateObject(hr, hr.Ref)
	}

	if err != nil {
		return err
	}

	logger.Info("Fetching Infoblox host record", "hostname", *hr.Name)
	return c.connector.GetObject(hr, ref, ibclient.NewQueryParams(false, nil), hr)
}

// getHostRecordAddrInSubnet returns the first IP address in a host record that is in the given subnet.
func getHostRecordAddrInSubnet(hr *ibclient.HostRecord, subnet netip.Prefix) (netip.Addr, bool) {
	if subnet.Addr().Is4() {
		for _, ip := range hr.Ipv4Addrs {
			if ip.Ipv4Addr != nil {
				nip, err := netip.ParseAddr(*ip.Ipv4Addr)
				if err != nil {
					// As a working IPAM system, Infoblox should only return valid IP addresses. But just in case it doesn't, we just skip the address.
					continue
				}
				if subnet.Contains(nip) {
					return nip, true
				}
			}
		}
	} else {
		for _, ip := range hr.Ipv6Addrs {
			if ip.Ipv6Addr != nil {
				nip, err := netip.ParseAddr(*ip.Ipv6Addr)
				if err != nil {
					// As a working IPAM system, Infoblox should only return valid IP addresses. But just in case it doesn't, we just skip the address.
					continue
				}
				if subnet.Contains(nip) {
					return nip, true
				}
			}
		}
	}
	return netip.Addr{}, false
}

// GetOrAllocateAddress returns the IP address of the given hostname in the given subnet. If the hostname does not have an IP address in the subnet, it will allocate one.
func (c *client) GetOrAllocateAddress(networkView, dnsView string, subnet netip.Prefix, hostname, zone string, logger logr.Logger) (netip.Addr, error) {
	hr, err := c.getOrNewHostRecord(networkView, dnsView, hostname, zone)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to get or create Infoblox host record: %w", err)
	}
	addr, ok := getHostRecordAddrInSubnet(hr, subnet)
	if ok {
		return addr, nil
	}

	if subnet.Addr().Is4() {
		ipr := ibclient.NewHostRecordIpv4Addr(nextAvailableIBFunc(subnet, networkView), "", false, "")
		hr.Ipv4Addrs = append(hr.Ipv4Addrs, *ipr)
	} else {
		ipr := ibclient.NewHostRecordIpv6Addr(nextAvailableIBFunc(subnet, networkView), "", false, "")
		hr.Ipv6Addrs = append(hr.Ipv6Addrs, *ipr)
	}

	// [Issue] this is to reassign netview and view as Infoblox is dropping them for me. Without that updating host record by reference will not work
	hr.NetworkView = networkView
	hr.View = toDNSView(dnsView)

	if err := c.createOrUpdateHostRecord(hr, logger); err != nil {
		return netip.Addr{}, fmt.Errorf("failed to create or update Infoblox host record: %w", err)
	}

	addr, ok = getHostRecordAddrInSubnet(hr, subnet)
	if ok {
		return addr, nil
	}
	return netip.Addr{}, errors.New("failed to allocate IP address: Infoblox host record does not contain a matching IP address")
}

func nextAvailableIBFunc(subnet netip.Prefix, view string) string {
	return fmt.Sprintf("func:nextavailableip:%s,%s", subnet.String(), view)
}

// ReleaseAddress releases the IP address of the given hostname in the given subnet.
func (c *client) ReleaseAddress(networkView, dnsView string, subnet netip.Prefix, hostname string, logger logr.Logger) error {
	hr, err := c.objMgr.GetHostRecord("", "", hostname, "", "")
	if err != nil {
		return err
	}

	removed := false
	if subnet.Addr().Is4() {
		for i, ip := range hr.Ipv4Addrs {
			if ip.Ipv4Addr != nil {
				nip, err := netip.ParseAddr(*ip.Ipv4Addr)
				if err != nil {
					continue
				}
				if subnet.Contains(nip) {
					hr.Ipv4Addrs = append(hr.Ipv4Addrs[:i], hr.Ipv4Addrs[i+1:]...)
					removed = true
					break
				}
			}
		}
	} else {
		for i, ip := range hr.Ipv6Addrs {
			if ip.Ipv6Addr != nil {
				nip, err := netip.ParseAddr(*ip.Ipv6Addr)
				if err != nil {
					continue
				}
				if subnet.Contains(nip) {
					hr.Ipv6Addrs = append(hr.Ipv6Addrs[:i], hr.Ipv6Addrs[i+1:]...)
					removed = true
					break
				}
			}
		}
	}

	if !removed {
		// The address is not in the host record, so we don't need to do anything.
		return nil
	}

	// [Issue] this is to reassign netview and view as Infoblox is dropping them for me. Without that updating host record by reference will not work
	hr.NetworkView = networkView
	hr.View = toDNSView(dnsView)

	if len(hr.Ipv4Addrs) == 0 && len(hr.Ipv6Addrs) == 0 {
		logger.Info("Deleting Infoblox host record", "hostname", hostname)
		if _, err := c.connector.DeleteObject(hr.Ref); err != nil {
			return fmt.Errorf("failed to delete Infoblox host record: %w", err)
		}
		return nil
	}
	prepareHostRecordForUpdate(hr)
	logger.Info("Updating Infoblox host record", "hostname", hostname)
	if _, err = c.connector.UpdateObject(hr, hr.Ref); err != nil {
		return fmt.Errorf("failed to update Infoblox host record: %w", err)
	}
	return nil
}

func toDNSView(dnsView string) *string {
	if dnsView == "" {
		return nil
	}
	if dnsView == "default" {
		return &dnsView
	}
	s := "default." + dnsView
	return &s
}

func prepareHostRecordForUpdate(hr *ibclient.HostRecord) {
	// We clear zone and network view because Infoblox will return an error if we try to "update" them.
	hr.Zone = ""
	hr.NetworkView = ""
	// ipv4addrs and ipv6addrs are nil after fetching the host record, but the api requires them to be empty arrays.
	if hr.Ipv4Addrs == nil {
		hr.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{}
	}
	if hr.Ipv6Addrs == nil {
		hr.Ipv6Addrs = []ibclient.HostRecordIpv6Addr{}
	}
	// clear Host field for all addresses
	for i := range hr.Ipv4Addrs {
		hr.Ipv4Addrs[i].Host = ""
	}
	for i := range hr.Ipv6Addrs {
		hr.Ipv6Addrs[i].Host = ""
	}
}
