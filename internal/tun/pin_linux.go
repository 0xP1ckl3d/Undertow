//go:build linux

package tun

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
)

// PinServer pins the carrier to its current physical route before VPN
// defaults are installed. The returned cleanup removes only this new route.
func PinServer(server netip.Addr) (func(), error) {
	if !server.Is4() {
		return nil, errors.New("VPN server must be IPv4")
	}
	// Loopback already has a more specific route than the VPN's /1 defaults.
	// Adding a host route to the local table collides with the kernel route.
	if server.IsLoopback() {
		return func() {}, nil
	}
	routes, err := netlink.RouteGet(net.IP(server.AsSlice()))
	if err != nil {
		return nil, fmt.Errorf("find server route: %w", err)
	}
	if len(routes) == 0 {
		return nil, errors.New("no physical route to VPN server")
	}
	source := routes[0]
	host := &net.IPNet{IP: net.IP(server.AsSlice()), Mask: net.CIDRMask(32, 32)}
	existing, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	for _, route := range existing {
		if route.Dst != nil && route.Dst.String() == host.String() {
			return func() {}, nil
		}
	}
	pinned := &netlink.Route{LinkIndex: source.LinkIndex, Dst: host, Gw: source.Gw, Scope: source.Scope, Table: source.Table}
	if err := netlink.RouteAdd(pinned); err != nil {
		return nil, err
	}
	return func() { _ = netlink.RouteDel(pinned) }, nil
}
