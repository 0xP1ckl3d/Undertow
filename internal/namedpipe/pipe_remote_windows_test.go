//go:build windows

package namedpipe

import (
	"context"
	"io"
	"testing"
	"time"
)

// A UNC loopback connection exercises Windows' SMB path rather than the local
// pipe path used by the agent relay integration test.
func TestRemoteSMBPipeListener(t *testing.T) {
	path := `\\.\pipe\undertow-remote-test-` + time.Now().Format("150405.000000000")
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, err = conn.Write([]byte("ok"))
		done <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	remote := `\\127.0.0.1\pipe\` + path[len(localPrefix):]
	conn, err := Dial(ctx, remote)
	if err != nil {
		t.Fatalf("remote SMB pipe connection %s: %v", remote, err)
	}
	defer conn.Close()
	var got [2]byte
	if _, err := io.ReadFull(conn, got[:]); err != nil || string(got[:]) != "ok" {
		t.Fatalf("remote SMB pipe read: %q, %v", got, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
