//go:build windows && amd64

package pivot

import (
	"context"
	"testing"
	"unsafe"
)

func TestNativeWriteStreamsBeyondFormerOutputCap(t *testing.T) {
	const id = uintptr(0xabcdef)
	var received int
	run := &nativeRun{ctx: context.Background(), write: func(_ byte, data []byte) error {
		received += len(data)
		return nil
	}}
	nativeRuns.Lock()
	nativeRuns.items[id] = run
	nativeRuns.Unlock()
	defer func() {
		nativeRuns.Lock()
		delete(nativeRuns.items, id)
		nativeRuns.Unlock()
	}()
	block := make([]byte, 8<<10)
	for received < 5<<20 {
		if result := nativeWrite(InteractiveOutput, id, uintptr(unsafe.Pointer(&block[0])), uintptr(len(block))); result != 0 {
			t.Fatalf("native write stopped after %d bytes", received)
		}
	}
	if received != 5<<20 {
		t.Fatalf("received %d bytes", received)
	}
}
