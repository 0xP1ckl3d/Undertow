//go:build linux

package control

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func platformNetworkRoutes() ([]NetworkRoute, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	interfaces, _ := net.Interfaces()
	names := make(map[int]string, len(interfaces))
	for _, iface := range interfaces {
		names[iface.Index] = iface.Name
	}
	result := make([]NetworkRoute, 0, len(routes))
	for _, route := range routes {
		if route.Table != 0 && route.Table != unix.RT_TABLE_MAIN {
			continue
		}
		prefix := netip.PrefixFrom(netip.IPv4Unspecified(), 0)
		if route.Dst != nil {
			parsed, err := netip.ParsePrefix(route.Dst.String())
			if err != nil || !parsed.Addr().Is4() {
				continue
			}
			prefix = parsed.Masked()
		}
		gateway := ""
		if route.Gw != nil {
			if addr, ok := netip.AddrFromSlice(route.Gw.To4()); ok && !addr.IsUnspecified() {
				gateway = addr.String()
			}
		}
		typeName := "unicast"
		if route.Type != 0 && route.Type != unix.RTN_UNICAST {
			typeName = fmt.Sprintf("type-%d", route.Type)
		}
		source := fmt.Sprintf("protocol-%d", route.Protocol)
		switch int(route.Protocol) {
		case unix.RTPROT_KERNEL:
			source = "kernel"
		case unix.RTPROT_BOOT:
			source = "boot"
		case unix.RTPROT_STATIC:
			source = "static"
		case unix.RTPROT_DHCP:
			source = "dhcp"
		}
		result = append(result, NetworkRoute{Prefix: prefix.String(), Gateway: gateway, Interface: names[route.LinkIndex], Type: typeName, Source: source, Direct: gateway == ""})
	}
	return result, nil
}
