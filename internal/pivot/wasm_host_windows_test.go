//go:build windows

package pivot

import (
	"context"
	"os"
	"path/filepath"
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

func TestWASMWindowsAuditOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	value, err := wasmHostOperation(ctx, "windows.service_names", wasmHostRequest{})
	if err != nil {
		t.Fatal(err)
	}
	names := value.([]string)
	if len(names) == 0 || len(names) > 128 {
		t.Fatalf("service names: %d", len(names))
	}
	second, err := wasmHostOperation(ctx, "windows.service_names", wasmHostRequest{Offset: int64(len(names)), Limit: 128})
	if err != nil {
		t.Fatalf("second service page: %v, %v", second, err)
	}
	if len(names) == 128 && len(second.([]string)) > 0 && names[len(names)-1] >= second.([]string)[0] {
		t.Fatal("service pagination is not ordered")
	}
	if _, err := wasmHostOperation(ctx, "windows.service_config", wasmHostRequest{Name: names[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := wasmHostOperation(ctx, "windows.service_config", wasmHostRequest{Name: `..\Invalid`}); err == nil {
		t.Fatal("invalid service name accepted")
	}
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
		t.Fatal(err)
	}
	acl, err := wasmHostOperation(ctx, "windows.file_acl", wasmHostRequest{Path: path})
	if err != nil || !strings.Contains(strings.ToLower(acl.(string)), "sample.txt") {
		t.Fatalf("ACL: %v, %v", acl, err)
	}
}
