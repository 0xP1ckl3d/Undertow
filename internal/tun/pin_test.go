//go:build linux || windows

package tun

import (
	"net/netip"
	"testing"
)

func TestPinLoopbackProxyDoesNotChangeRoutes(t *testing.T) {
	cleanup, err := PinServer(netip.MustParseAddr("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
}
