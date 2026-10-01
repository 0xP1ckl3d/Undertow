//go:build windows

package pivot

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWASMRegistryRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := wasmHostOperation(ctx, "registry.read", wasmHostRequest{Key: `HKCU\Environment`})
	if err != nil || !strings.Contains(value.(string), "HKEY_CURRENT_USER") {
		t.Fatalf("registry read: value=%v err=%v", value, err)
	}
	if _, err := wasmHostOperation(ctx, "registry.read", wasmHostRequest{Key: `HKCR\SomeKey`}); err == nil {
		t.Fatal("unsupported registry hive was accepted")
	}
}
