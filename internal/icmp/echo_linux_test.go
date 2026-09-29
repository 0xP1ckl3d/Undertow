//go:build linux

package icmp

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestLocalPingSocketIntegration(t *testing.T) {
	if os.Getenv("UNDERTOW_ICMP_INTEGRATION") != "1" {
		t.Skip("set UNDERTOW_ICMP_INTEGRATION=1 on a Linux host with ping sockets")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := Echo(ctx, netip.MustParseAddr("127.0.0.1"), []byte("undertow-icmp-test")); err != nil {
		t.Fatal(err)
	}
}
