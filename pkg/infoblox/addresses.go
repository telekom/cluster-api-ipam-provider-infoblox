package infoblox

import (
	"errors"
	"fmt"
	"net/netip"

	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
)

// Known limitations:
// - Hostname must be a FQDN. We enable DNS for the host record, so Infoblox will return an error if the hostname is not a FQDN.
// - When DNS is enabled for a host record, Infoblox returns an error: "IBDataConflictError: IB.Data.Conflict:The action is not allowed. A parent was not found".
//   DNS was disabled in tests as a workaround.
// - For some reason Infoblox does not assign network view to the host record. Workarounds were provided in the code with tag: [Issue]

// getOrNewHostRecord returns the host record with the given hostname in the given view, or creates a new host record if no host record with the given hostname exists.
func (c *client) getOrNewHostRecord(view, hostname, zone string) (*ibclient.HostRecord, error) {
	// [Issue] For some reason Infoblox does not assign view to the host record. Empty netview and dnsview is a workaround to find host.
	hostRecord, err := c.objMgr.GetHostRecord("", "", hostname, "", "")
	if err != nil {
		if _, ok := err.(*ibclient.NotFoundError); !ok {
			return nil, err
		}
	}

	if hostRecord == nil {
		hostRecord = ibclient.NewEmptyHostRecord()
		hostRecord.Name = hostname
		hostRecord.NetworkView = view
		hostRecord.Ipv4Addrs = []ibclient.HostRecordIpv4Addr{}
		hostRecord.Ipv6Addrs = []ibclient.HostRecordIpv6Addr{}
		hostRecord.EnableDns = false
		if zone != "" {
			hostRecord.EnableDns = true
			hostRecord.View = toDNSView(view)
			hostRecord.Zone = zone
		}
	}

	return hostRecord, nil
}

// createOrUpdateHostRecord creates or updates a host record and then fetches the updated record.
func (c *client) createOrUpdateHostRecord(hr *ibclient.HostRecord) error {
	ref := ""
	var err error
	if hr.Ref == "" {
		ref, err = c.connector.CreateObject(hr)
	} else {
		prepareHostRecordForUpdate(hr)
		ref, err = c.connector.UpdateObject(hr, hr.Ref)
	}

	if err != nil {
		return err
	}

	return c.connector.GetObject(hr, ref, ibclient.NewQueryParams(false, nil), hr)
}

// getHostRecordAddrInSubnet returns the first IP address in a host record that is in the given subnet.
func getHostRecordAddrInSubnet(hr *ibclient.HostRecord, subnet netip.Prefix) (netip.Addr, bool) {
	if subnet.Addr().Is4() {
		for _, ip := range hr.Ipv4Addrs {
			nip, err := netip.ParseAddr(ip.Ipv4Addr)
			if err != nil {
				// As a working IPAM system, Infoblox should only return valid IP addresses. But just in case it doesn't, we just skip the address.
				continue
			}
			if subnet.Contains(nip) {
				return nip, true
			}
		}
	} else {
		for _, ip := range hr.Ipv6Addrs {
			nip, err := netip.ParseAddr(ip.Ipv6Addr)
			if err != nil {
				// As a working IPAM system, Infoblox should only return valid IP addresses. But just in case it doesn't, we just skip the address.
				continue
			}
			if subnet.Contains(nip) {
				return nip, true
			}
		}
	}
	return netip.Addr{}, false
}

// getOrAllocateAddress returns the IP address of the given hostname in the given subnet. If the hostname does not have an IP address in the subnet, it will allocate one.
func (c *client) GetOrAllocateAddress(view string, subnet netip.Prefix, hostname, zone string) (netip.Addr, error) {
	hr, err := c.getOrNewHostRecord(view, hostname, zone)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to get or create Infoblox host record: %w", err)
	}
	addr, ok := getHostRecordAddrInSubnet(hr, subnet)
	if ok {
		return addr, nil
	}

	if subnet.Addr().Is4() {
		ipr := ibclient.NewHostRecordIpv4Addr(nextAvailableIBFunc(subnet, view), "", false, "")
		hr.Ipv4Addrs = append(hr.Ipv4Addrs, *ipr)
	} else {
		ipr := ibclient.NewHostRecordIpv6Addr(nextAvailableIBFunc(subnet, view), "", false, "")
		hr.Ipv6Addrs = append(hr.Ipv6Addrs, *ipr)
	}

	// [Issue] this is to reassign netview and view as Infoblox is dropping them for me. Without that updating host record by reference will not work
	hr.NetworkView = view
	hr.View = toDNSView(view)

	if err := c.createOrUpdateHostRecord(hr); err != nil {
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
func (c *client) ReleaseAddress(view string, subnet netip.Prefix, hostname string) error {
	hr, err := c.objMgr.GetHostRecord("", "", hostname, "", "")
	if err != nil {
		fmt.Printf("error GetHostRecord: %s\n", err.Error())
		return err
	}

	removed := false
	if subnet.Addr().Is4() {
		for i, ip := range hr.Ipv4Addrs {
			nip, err := netip.ParseAddr(ip.Ipv4Addr)
			if err != nil {
				continue
			}
			if subnet.Contains(nip) {
				hr.Ipv4Addrs = append(hr.Ipv4Addrs[:i], hr.Ipv4Addrs[i+1:]...)
				removed = true
				break
			}
		}
	} else {
		for i, ip := range hr.Ipv6Addrs {
			nip, err := netip.ParseAddr(ip.Ipv6Addr)
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

	if !removed {
		// The address is not in the host record, so we don't need to do anything.
		fmt.Printf("not removed")
		return nil
	}

	// [Issue] this is to reassign netview and view as Infoblox is dropping them for me. Without that updating host record by reference will not work
	hr.NetworkView = view
	hr.View = toDNSView(view)

	if len(hr.Ipv4Addrs) == 0 && len(hr.Ipv6Addrs) == 0 {
		_, err := c.connector.DeleteObject(hr.Ref)
		if err != nil {
			fmt.Printf("error DeleteObject: %s\n", err.Error())
		}
		return err
	}
	prepareHostRecordForUpdate(hr)
	_, err = c.connector.UpdateObject(hr, hr.Ref)
	if err != nil {
		fmt.Printf("error UpdateObject: %s\n", err.Error())
	}
	return err
}

func toDNSView(view string) string {
	if view == "" {
		return ""
	}
	if view == "default" {
		return view
	}
	return "default." + view
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
}
