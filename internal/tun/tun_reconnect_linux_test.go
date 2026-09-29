//go:build linux

package tun

import (
	"os"
	"testing"
	"time"
)

func TestClosedTUNUnblocksReaderAndCanReopen(t *testing.T) {
	if os.Getenv("UNDERTOW_TUN_INTEGRATION") != "1" {
		t.Skip("requires elevated isolated TUN host")
	}
	const name = "undertow-rtest"
	device, err := Open(name, "198.18.253.1/24")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = device.Read(make([]byte, 1500)); close(done) }()
	time.Sleep(100 * time.Millisecond)
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TUN reader remained blocked after close")
	}
	reopened, err := Open(name, "198.18.253.1/24")
	if err != nil {
		t.Fatalf("reopen TUN: %v", err)
	}
	defer reopened.Close()
}
