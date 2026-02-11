package infoblox

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/go-logr/logr"
	ibclient "github.com/infobloxopen/infoblox-go-client/v2"
	"k8s.io/utils/ptr"
)

// hostRecordReturnFields is a subset of host record return fields we need when fetching host record objects from infoblox.
var hostRecordReturnFields = []string{"ipv4addrs", "ipv6addrs", "name", "view", "zone", "network_view", "configure_for_dns"}

// Known limitations:
// - Hostname must be a FQDN if DNSZone is configured because in that case we enable DNS for the host record, so Infoblox will return an error if the hostname is not a FQDN.
//   If the Hostname does not match the DNSZone suffix it is automatically assumed and appended to the hostname before record creation.

// getOrEmptyHostRecord returns the host record with the given hostname in the given view, or creates a new host record if no host record with the given hostname exists.
func (c *client) getOrEmptyHostRecord(networkView, dnsView, dnsZone, hostname string) (*ibclient.HostRecord, error) {

	// if DNS is enabled but the hostname does not have the zone as suffix automatically assume it
	if !strings.HasSuffix(hostname, dnsZone) {
		hostname = fmt.Sprintf("%s.%s", hostname, dnsZone)
	}

	params := map[string]string{
		"name":           hostname,
		"_return_fields": strings.Join(hostRecordReturnFields, ","),
	}
	if networkView != "" {
		params["network_view"] = networkView
	}

	var records []ibclient.HostRecord
	err := c.connector.GetObject(ibclient.NewEmptyHostRecord(), "", ibclient.NewQueryParams(false, params), &records)
	if err != nil {
		// since ibclient.NotFoundError has a pointer receiver on it's Error() method, we can't use errors.As() here.
		if _, ok := err.(*ibclient.NotFoundError); !ok {
			return nil, tryParseWapiError(err)
		}
		// not found -> return new preconfigured hr
		hr := ibclient.NewEmptyHostRecord()
		hr.Name = ptr.To(hostname)
		hr.NetworkView = networkView
		hr.EnableDns = ptr.To(false)
		if dnsZone != "" {
			// configure for DNS
			hr.EnableDns = ptr.To(true)
			hr.View = toDNSView(dnsView)
			// hr.DnsName is a read only field and may not be set by the client
		}
		return hr, nil
	}
	if len(records) == 1 {
		return &records[0], nil
	}
	return nil, fmt.Errorf("multiple host records found for hostname %q in network view %q and dns view %q", hostname, networkView, dnsView)
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
		return tryParseWapiError(err)
	}

	logger.Info("Fetching Infoblox host record", "hostname", *hr.Name)
	params := map[string]string{
		"_return_fields": strings.Join(hostRecordReturnFields, ","),
	}
	return c.connector.GetObject(hr, ref, ibclient.NewQueryParams(false, params), hr)
}

// getHostRecordsByAddr returns all host records that have the given IP address assigned.
func (c *client) getHostRecordsByAddr(addr netip.Addr, networkView string) ([]ibclient.HostRecord, error) {
	params := map[string]string{
		"_return_fields": strings.Join(hostRecordReturnFields, ","),
	}
	if addr.Is4() {
		params["ipv4addr"] = addr.String()
	} else {
		params["ipv6addr"] = addr.String()
	}
	if networkView != "" {
		params["network_view"] = networkView
	}

	var results []ibclient.HostRecord
	err := c.connector.GetObject(ibclient.NewEmptyHostRecord(), "", ibclient.NewQueryParams(false, params), &results)
	if err != nil {
		if _, ok := err.(*ibclient.NotFoundError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to search host records by address %s: %w", addr, tryParseWapiError(err))
	}
	return results, nil
}

// getAllocatedHostRecordAddrInSubnet returns the first IP address in a host record that is in the given subnet.
func getAllocatedHostRecordAddrInSubnet(hr *ibclient.HostRecord, subnet netip.Prefix) netip.Addr {
	if subnet.Addr().Is4() {
		for _, ip := range hr.Ipv4Addrs {
			if ip.Ipv4Addr != nil {
				nip, err := netip.ParseAddr(*ip.Ipv4Addr)
				if err != nil {
					// As a working IPAM system, Infoblox should only return valid IP addresses. But just in case it doesn't, we just skip the address.
					continue
				}
				if subnet.Contains(nip) {
					return nip
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
					return nip
				}
			}
		}
	}
	return netip.Addr{}
}

// GetOrAllocateAddress returns the IP address of the given hostname in the given subnet.
//
// If the hostname does not have an IP address in the subnet, it will allocate one.
//
// If the 'desiredAddr' parameter is specified this exact address will be allocated for the host if possible. If not, an error is returned.
func (c *client) GetOrAllocateAddress(networkView, dnsView string, subnet netip.Prefix, desiredAddr netip.Addr, hostname, dnsZone string, logger logr.Logger) (netip.Addr, error) {
	hr, err := c.getOrEmptyHostRecord(networkView, dnsView, dnsZone, hostname)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to get or create Infoblox host record: %w", err)
	}

	allocatedAddr := getAllocatedHostRecordAddrInSubnet(hr, subnet)
	if allocatedAddr.IsValid() {
		if desiredAddr.IsValid() && desiredAddr != allocatedAddr {
			return allocatedAddr, fmt.Errorf("allocated address %q does not match desired address %q", allocatedAddr, desiredAddr)
		}
		return allocatedAddr, nil
	}

	var addrRequest string
	if desiredAddr.IsValid() {
		existingHRs, err := c.getHostRecordsByAddr(desiredAddr, networkView)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("failed to check if desired address %q is already allocated: %w", desiredAddr, err)
		}
		if len(existingHRs) > 0 {
			return netip.Addr{}, fmt.Errorf("desired address %q is already allocated by %d other host record(s)", desiredAddr, len(existingHRs))
		}
		addrRequest = desiredAddr.String()
	} else {
		addrRequest = nextAvailableIPInfobloxFunc(subnet, networkView)
	}

	if subnet.Addr().Is4() {
		ipr := ibclient.NewHostRecordIpv4Addr(addrRequest, "", false, "")
		hr.Ipv4Addrs = append(hr.Ipv4Addrs, *ipr)
	} else {
		ipr := ibclient.NewHostRecordIpv6Addr(addrRequest, "", false, "")
		hr.Ipv6Addrs = append(hr.Ipv6Addrs, *ipr)
	}

	if err := c.createOrUpdateHostRecord(hr, logger); err != nil {
		return netip.Addr{}, fmt.Errorf("failed to create or update Infoblox host record: %w", err)
	}

	allocatedAddr = getAllocatedHostRecordAddrInSubnet(hr, subnet)
	if !allocatedAddr.IsValid() {
		return netip.Addr{}, errors.New("failed to allocate IP address: Infoblox host record does not contain a matching IP address")
	}
	return allocatedAddr, nil
}

func nextAvailableIPInfobloxFunc(subnet netip.Prefix, view string) string {
	return fmt.Sprintf("func:nextavailableip:%s,%s", subnet.String(), view)
}

// ReleaseAddress releases the IP address of the given hostname in the given subnet.
func (c *client) ReleaseAddress(networkView, dnsView string, subnet netip.Prefix, hostname string, logger logr.Logger) error {
	hr, err := c.getOrEmptyHostRecord(networkView, dnsView, "", hostname)
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

	if len(hr.Ipv4Addrs) == 0 && len(hr.Ipv6Addrs) == 0 {
		logger.Info("Deleting Infoblox host record", "hostname", hostname)
		if _, err := c.connector.DeleteObject(hr.Ref); err != nil {
			return fmt.Errorf("failed to delete Infoblox host record: %w", tryParseWapiError(err))
		}
		return nil
	}
	prepareHostRecordForUpdate(hr)
	logger.Info("Updating Infoblox host record", "hostname", hostname)
	if _, err = c.connector.UpdateObject(hr, hr.Ref); err != nil {
		return fmt.Errorf("failed to update Infoblox host record: %w", tryParseWapiError(err))
	}
	return nil
}

func toDNSView(dnsView string) *string {
	if dnsView == "" {
		return nil
	}
	return &dnsView
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
