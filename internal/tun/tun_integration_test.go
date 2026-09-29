//go:build linux || windows

package tun

import (
	"os"
	"testing"
	"time"
)

// Run with UNDERTOW_TUN_INTEGRATION=1 on an isolated elevated test host.
// It creates a virtual interface and an application-owned test route.
func TestVirtualInterfaceLifecycle(t *testing.T) {
	if os.Getenv("UNDERTOW_TUN_INTEGRATION") != "1" {
		t.Skip("requires elevated isolated TUN/Wintun host")
	}
	name := "undertow-test"
	if len(name) > 15 {
		t.Fatal("invalid Linux interface name")
	}
	device, err := Open(name, "198.18.254.1/24")
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	route := "198.18.255.254/32"
	if err := device.AddRoute(route); err != nil {
		t.Fatal(err)
	}
	if err := device.DelRoute(route); err != nil {
		t.Fatal(err)
	}
	if err := device.AddRoute(route); err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
}
