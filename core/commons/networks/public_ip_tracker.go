package networks

import (
	"encoding/binary"
	"github.com/palantir/stacktrace"
	"net"
)

type FreeIpAddrTracker struct {
	subnet *net.IPNet
	takenIps map[string]bool
}

func NewFreeIpAddrTracker(subnetMask string) (ipAddrTracker *FreeIpAddrTracker, err error) {
	_, ipv4Net, err := net.ParseCIDR(subnetMask)
	if err != nil {
		return nil, stacktrace.Propagate(err, "Failed to parse subnet %s as CIDR.", subnetMask)
	}
	takenIps := map[string]bool{}
	ipAddrTracker = &FreeIpAddrTracker{
		subnet: ipv4Net,
		takenIps: takenIps,
	}
	// remove the zeroth IP - it's only for marking subnet addresses.
	_, err = ipAddrTracker.GetFreeIpAddr()
	if err != nil {
		return nil, stacktrace.Propagate(err, "Failed to remove zeroth IP.")
	}
	return ipAddrTracker, nil
}

func (networkManager FreeIpAddrTracker) GetFreeIpAddr() (ipAddr string, err error){
	// convert IPNet struct mask and address to uint32
	// network is BigEndian
	mask := binary.BigEndian.Uint32(networkManager.subnet.Mask)
	start := binary.BigEndian.Uint32(networkManager.subnet.IP)
	// find the final address
	finish := (start & mask) | (mask ^ 0xffffffff)
	// loop through addresses as uint32
	for i := start; i <= finish; i++ {
		// convert back to net.IP
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, i)
		ipStr := ip.String()
		if !networkManager.takenIps[ipStr] {
			networkManager.takenIps[ipStr] = true
			return ipStr, nil
		}
	}
	return "", stacktrace.NewError("Failed to allocate IpAddr on subnet %v - all taken.", networkManager.subnet)
}