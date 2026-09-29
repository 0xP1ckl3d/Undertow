//go:build linux

package tun

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type Device struct {
	file   *os.File
	name   string
	link   netlink.Link
	mu     sync.Mutex
	routes map[string]*netlink.Route
}

// Open creates a non-persistent TUN interface. Closing the descriptor removes
// the interface; explicit routes are removed first if still owned by it.
func Open(name, address string) (*Device, error) {
	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		file.Close()
		return nil, err
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err = unix.IoctlIfreq(int(file.Fd()), unix.TUNSETIFF, ifr); err != nil {
		file.Close()
		return nil, err
	}
	link, err := netlink.LinkByName(ifr.Name())
	if err != nil {
		file.Close()
		return nil, err
	}
	addr, err := netlink.ParseAddr(address)
	if err != nil {
		file.Close()
		return nil, err
	}
	if err = netlink.AddrAdd(link, addr); err != nil {
		file.Close()
		return nil, err
	}
	if err = netlink.LinkSetMTU(link, 1400); err != nil {
		file.Close()
		return nil, err
	}
	if err = netlink.LinkSetUp(link); err != nil {
		file.Close()
		return nil, err
	}
	return &Device{file: file, name: ifr.Name(), link: link, routes: make(map[string]*netlink.Route)}, nil
}

func (d *Device) Name() string { return d.name }

// Poll before each read so closing the TUN can release a blocked reader before
// a VPN reconnect tries to create an interface with the same name.
func (d *Device) Read(p []byte) (int, error) {
	fd := int(d.file.Fd())
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return 0, unix.EBADF
		}
		return unix.Read(fd, p)
	}
}
func (d *Device) Write(p []byte) (int, error) { return unix.Write(int(d.file.Fd()), p) }

func (d *Device) AddRoute(prefix string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.routes[prefix]; ok {
		return fmt.Errorf("route %s already owned", prefix)
	}
	_, dst, err := net.ParseCIDR(prefix)
	if err != nil {
		return err
	}
	route := &netlink.Route{LinkIndex: d.link.Attrs().Index, Dst: dst, Scope: netlink.SCOPE_LINK}
	if err = netlink.RouteAdd(route); err != nil {
		return err
	}
	d.routes[prefix] = route
	return nil
}

func (d *Device) DelRoute(prefix string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	route, ok := d.routes[prefix]
	if !ok {
		return fmt.Errorf("route %s is not owned by undertow", prefix)
	}
	if err := netlink.RouteDel(route); err != nil {
		return err
	}
	delete(d.routes, prefix)
	return nil
}

func (d *Device) Close() error {
	d.mu.Lock()
	for prefix, route := range d.routes {
		_ = netlink.RouteDel(route)
		delete(d.routes, prefix)
	}
	d.mu.Unlock()
	return d.file.Close()
}

// ExistingNetworks returns IPv4 interface and non-default route prefixes for
// collision checks before an Undertow interface or route is installed.
func ExistingNetworks() ([]netip.Prefix, error) {
	seen := make(map[netip.Prefix]bool)
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if a.IPNet == nil {
				continue
			}
			if p, err := netip.ParsePrefix(a.IPNet.String()); err == nil {
				seen[p.Masked()] = true
			}
		}
	}
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		if r.Dst == nil {
			continue
		}
		if p, err := netip.ParsePrefix(r.Dst.String()); err == nil && p.Bits() > 0 {
			seen[p.Masked()] = true
		}
	}
	out := make([]netip.Prefix, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out, nil
}
