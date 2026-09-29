//go:build windows

package tun

import (
	"errors"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func PinServer(server netip.Addr) (func(), error) {
	if !server.Is4() {
		return nil, errors.New("VPN server must be IPv4")
	}
	rows, err := winipcfg.GetIPForwardTable2(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return nil, err
	}
	best := -1
	for i := range rows {
		prefix := rows[i].DestinationPrefix.Prefix()
		if !prefix.IsValid() || !prefix.Contains(server) {
			continue
		}
		if best < 0 || prefix.Bits() > rows[best].DestinationPrefix.Prefix().Bits() || prefix.Bits() == rows[best].DestinationPrefix.Prefix().Bits() && rows[i].Metric < rows[best].Metric {
			best = i
		}
	}
	if best < 0 {
		return nil, errors.New("no physical route to VPN server")
	}
	if rows[best].DestinationPrefix.Prefix().Bits() == 32 {
		return func() {}, nil
	}
	host := netip.PrefixFrom(server, 32)
	luid := rows[best].InterfaceLUID
	gateway := rows[best].NextHop.Addr()
	if err := luid.AddRoute(host, gateway, 0); err != nil {
		return nil, err
	}
	return func() { _ = luid.DeleteRoute(host, gateway) }, nil
}
