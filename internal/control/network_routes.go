package control

import (
	"net/netip"
	"sort"
)

// NetworkRoute describes one IPv4 route visible on an agent host.
type NetworkRoute struct {
	Prefix    string `json:"prefix"`
	Gateway   string `json:"gateway,omitempty"`
	Interface string `json:"interface,omitempty"`
	Type      string `json:"type,omitempty"`
	Source    string `json:"source,omitempty"`
	Direct    bool   `json:"direct"`
}

func collectNetworkRoutes() ([]NetworkRoute, *NetworkRoute) {
	routes, err := platformNetworkRoutes()
	if err != nil {
		return nil, nil
	}
	seen := make(map[string]bool)
	result := make([]NetworkRoute, 0, len(routes))
	var defaultRoute *NetworkRoute
	for _, route := range routes {
		prefix, err := netip.ParsePrefix(route.Prefix)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		prefix = prefix.Masked()
		route.Prefix = prefix.String()
		if prefix.Bits() == 0 {
			if defaultRoute == nil {
				copy := route
				defaultRoute = &copy
			}
			continue
		}
		if prefix.Bits() >= 31 || prefix.Addr().IsLoopback() || prefix.Addr().IsLinkLocalUnicast() || prefix.Addr().IsMulticast() {
			continue
		}
		key := route.Prefix + "|" + route.Gateway + "|" + route.Interface
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, route)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Direct != result[j].Direct {
			return result[i].Direct
		}
		return result[i].Prefix < result[j].Prefix
	})
	if len(result) > 64 {
		result = result[:64]
	}
	return result, defaultRoute
}

func validNetworkRoute(route NetworkRoute, defaultOnly bool) (NetworkRoute, bool) {
	prefix, err := netip.ParsePrefix(route.Prefix)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || (prefix.Bits() == 0) != defaultOnly {
		return NetworkRoute{}, false
	}
	if route.Gateway != "" {
		gateway, err := netip.ParseAddr(route.Gateway)
		if err != nil || !gateway.Is4() {
			return NetworkRoute{}, false
		}
		route.Direct = false
	}
	if len(route.Interface) > 64 || len(route.Type) > 32 || len(route.Source) > 32 {
		return NetworkRoute{}, false
	}
	for _, value := range []string{route.Interface, route.Type, route.Source} {
		for _, char := range value {
			if char < 32 || char == 127 {
				return NetworkRoute{}, false
			}
		}
	}
	return route, true
}
