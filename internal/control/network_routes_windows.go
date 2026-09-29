//go:build windows

package control

import (
	"fmt"
	"net"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func platformNetworkRoutes() ([]NetworkRoute, error) {
	routes, err := winipcfg.GetIPForwardTable2(winipcfg.AddressFamily(windows.AF_INET))
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
		prefix := route.DestinationPrefix.Prefix()
		if !prefix.IsValid() || !prefix.Addr().Is4() {
			continue
		}
		gateway := ""
		if addr := route.NextHop.Addr(); addr.IsValid() && addr.Is4() && !addr.IsUnspecified() {
			gateway = addr.String()
		}
		source := fmt.Sprintf("protocol-%d", route.Protocol)
		switch route.Protocol {
		case winipcfg.RouteProtocolLocal:
			source = "local"
		case winipcfg.RouteProtocolNetMgmt:
			source = "manual"
		case winipcfg.RouteProtocolDHCP:
			source = "dhcp"
		}
		result = append(result, NetworkRoute{Prefix: prefix.Masked().String(), Gateway: gateway, Interface: names[int(route.InterfaceIndex)], Type: "unicast", Source: source, Direct: gateway == ""})
	}
	return result, nil
}
