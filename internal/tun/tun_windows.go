//go:build windows

package tun

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	_ "embed"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

//go:embed wintun-0.14.1.zip
var wintunArchive []byte

const wintunArchiveSHA256 = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"

// CheckAvailability verifies the bundled driver and any already extracted DLL.
// It does not create an adapter or write a DLL.
func CheckAvailability() error {
	if sum := sha256.Sum256(wintunArchive); hex.EncodeToString(sum[:]) != wintunArchiveSHA256 {
		return errors.New("bundled Wintun archive hash mismatch")
	}
	entryName := "wintun/bin/" + runtime.GOARCH + "/wintun.dll"
	z, err := zip.NewReader(bytes.NewReader(wintunArchive), int64(len(wintunArchive)))
	if err != nil {
		return err
	}
	var bundledDLL []byte
	for _, entry := range z.File {
		if entry.Name == entryName {
			r, err := entry.Open()
			if err != nil {
				return err
			}
			bundledDLL, err = io.ReadAll(io.LimitReader(r, 1<<20))
			_ = r.Close()
			if err != nil {
				return err
			}
			break
		}
	}
	if len(bundledDLL) == 0 {
		return fmt.Errorf("Wintun DLL unavailable for %s", runtime.GOARCH)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if existing, err := os.ReadFile(path); err == nil {
		if sha256.Sum256(existing) != sha256.Sum256(bundledDLL) {
			return fmt.Errorf("existing %s differs from bundled Wintun 0.14.1; inspect the DLL before starting", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	return nil
}

type Device struct {
	name    string
	adapter *wintun.Adapter
	session wintun.Session
	luid    winipcfg.LUID
	address netip.Prefix
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	routes  map[netip.Prefix]bool
}

func ensureWintunDLL() error {
	sum := sha256.Sum256(wintunArchive)
	if hex.EncodeToString(sum[:]) != wintunArchiveSHA256 {
		return errors.New("embedded Wintun archive hash mismatch")
	}
	z, err := zip.NewReader(bytes.NewReader(wintunArchive), int64(len(wintunArchive)))
	if err != nil {
		return err
	}
	entryName := "wintun/bin/" + runtime.GOARCH + "/wintun.dll"
	var dll []byte
	for _, entry := range z.File {
		if entry.Name != entryName {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			return err
		}
		dll, err = io.ReadAll(io.LimitReader(r, 1<<20))
		r.Close()
		if err != nil {
			return err
		}
		break
	}
	if len(dll) == 0 {
		return fmt.Errorf("Wintun DLL unavailable for %s", runtime.GOARCH)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if current, err := os.ReadFile(path); err == nil {
		if sha256.Sum256(current) != sha256.Sum256(dll) {
			return fmt.Errorf("existing %s differs from bundled Wintun 0.14.1", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(exe), "wintun-*.dll")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(dll); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		if current, readErr := os.ReadFile(path); readErr == nil && sha256.Sum256(current) == sha256.Sum256(dll) {
			return nil
		}
		return err
	}
	return nil
}

func Open(name, address string) (*Device, error) {
	prefix, err := netip.ParsePrefix(address)
	if err != nil || !prefix.Addr().Is4() {
		return nil, errors.New("Wintun address must be an IPv4 prefix")
	}
	if err = ensureWintunDLL(); err != nil {
		return nil, err
	}
	adapter, err := wintun.CreateAdapter(name, "Undertow", nil)
	if err != nil {
		return nil, err
	}
	session, err := adapter.StartSession(4 << 20)
	if err != nil {
		adapter.Close()
		return nil, err
	}
	luid := winipcfg.LUID(adapter.LUID())
	if err = luid.AddIPAddress(prefix); err != nil {
		session.End()
		adapter.Close()
		return nil, err
	}
	return &Device{name: name, adapter: adapter, session: session, luid: luid, address: prefix, done: make(chan struct{}), routes: make(map[netip.Prefix]bool)}, nil
}

func (d *Device) Name() string { return d.name }
func (d *Device) Read(p []byte) (int, error) {
	for {
		select {
		case <-d.done:
			return 0, io.EOF
		default:
		}
		packet, err := d.session.ReceivePacket()
		if err == nil {
			n := copy(p, packet)
			d.session.ReleaseReceivePacket(packet)
			if n < len(packet) {
				return n, io.ErrShortBuffer
			}
			return n, nil
		}
		if !errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return 0, err
		}
		_, err = windows.WaitForSingleObject(d.session.ReadWaitEvent(), 1000)
		if err != nil {
			return 0, err
		}
	}
}
func (d *Device) Write(p []byte) (int, error) {
	packet, err := d.session.AllocateSendPacket(len(p))
	if err != nil {
		return 0, err
	}
	copy(packet, p)
	d.session.SendPacket(packet)
	return len(p), nil
}
func (d *Device) AddRoute(raw string) error {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil || !prefix.Addr().Is4() {
		return errors.New("invalid IPv4 route")
	}
	prefix = prefix.Masked()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.routes[prefix] {
		return fmt.Errorf("route %s already owned", prefix)
	}
	if err = d.luid.AddRoute(prefix, netip.IPv4Unspecified(), 0); err != nil {
		return err
	}
	d.routes[prefix] = true
	return nil
}
func (d *Device) DelRoute(raw string) error {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return err
	}
	prefix = prefix.Masked()
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.routes[prefix] {
		return fmt.Errorf("route %s is not owned by undertow", prefix)
	}
	if err = d.luid.DeleteRoute(prefix, netip.IPv4Unspecified()); err != nil {
		return err
	}
	delete(d.routes, prefix)
	return nil
}
func (d *Device) Close() error {
	var result error
	d.once.Do(func() {
		close(d.done)
		d.mu.Lock()
		for prefix := range d.routes {
			_ = d.luid.DeleteRoute(prefix, netip.IPv4Unspecified())
			delete(d.routes, prefix)
		}
		d.mu.Unlock()
		_ = d.luid.DeleteIPAddress(d.address)
		d.session.End()
		result = d.adapter.Close()
	})
	return result
}

func ExistingNetworks() ([]netip.Prefix, error) {
	seen := make(map[netip.Prefix]bool)
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
				seen[p.Masked()] = true
			}
		}
	}
	routes, err := winipcfg.GetIPForwardTable2(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return nil, err
	}
	for _, row := range routes {
		p := row.DestinationPrefix.Prefix()
		if p.IsValid() && p.Addr().Is4() && p.Bits() > 0 {
			seen[p.Masked()] = true
		}
	}
	out := make([]netip.Prefix, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out, nil
}
