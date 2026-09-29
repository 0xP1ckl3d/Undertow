//go:build !linux && !windows

package tun

import (
	"errors"
	"net"
	"net/netip"
)

type Device struct{}

func Open(name, address string) (*Device, error) {
	return nil, errors.New("proxy TUN is currently supported on Linux only")
}
func (d *Device) Name() string                 { return "" }
func (d *Device) Read(p []byte) (int, error)   { return 0, errors.New("unsupported") }
func (d *Device) Write(p []byte) (int, error)  { return 0, errors.New("unsupported") }
func (d *Device) AddRoute(prefix string) error { return errors.New("unsupported") }
func (d *Device) DelRoute(prefix string) error { return errors.New("unsupported") }
func (d *Device) Close() error                 { return nil }
func ExistingNetworks() ([]netip.Prefix, error) {
	var out []netip.Prefix
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			if p, err := netip.ParsePrefix(addr.String()); err == nil && p.Addr().Is4() {
				out = append(out, p.Masked())
			}
		}
	}
	return out, nil
}
