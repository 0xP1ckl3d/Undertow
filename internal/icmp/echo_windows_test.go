//go:build windows

package icmp

import (
	"context"
	"net/netip"
	"testing"
)

func TestUnprivilegedWindowsLoopbackEcho(t *testing.T) {
	if _, err := Echo(context.Background(), netip.MustParseAddr("127.0.0.1"), []byte("undertow-echo")); err != nil {
		t.Fatal(err)
	}
}
