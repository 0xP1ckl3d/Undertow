//go:build linux

package pivot

import (
	"context"
	"errors"
	"io"
	"os"
)

func wasmPlatformInventory(ctx context.Context, op string) (any, error) {
	switch op {
	case "processes":
		return platformHostInfo(ctx, "ps")
	case "privileges":
		return platformHostInfo(ctx, "privileges")
	case "routes":
		return wasmInventoryFallback(ctx, "/proc/net/route", "ip", "-4", "route", "show", "table", "all")
	case "dns_config":
		return platformHostInfo(ctx, "dns")
	case "users":
		return wasmReadInventoryFile("/etc/passwd")
	case "groups":
		return wasmReadInventoryFile("/etc/group")
	case "neighbours":
		return wasmInventoryFallback(ctx, "/proc/net/arp", "ip", "neigh", "show")
	case "connections":
		return wasmInventoryFallback(ctx, "/proc/net/tcp", "ss", "-tunap")
	case "services":
		return inventoryCommand(ctx, "systemctl", "list-units", "--type=service", "--all", "--no-pager", "--plain")
	case "startup":
		return inventoryCommand(ctx, "systemctl", "list-unit-files", "--type=service", "--no-pager", "--plain")
	}
	return nil, errWASMUnsupported
}

func wasmReadInventoryFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 32<<10))
	return string(data), nil
}

func wasmInventoryFallback(ctx context.Context, path, program string, args ...string) (string, error) {
	value, err := inventoryCommand(ctx, program, args...)
	if err == nil {
		return value, nil
	}
	value, fileErr := wasmReadInventoryFile(path)
	if fileErr == nil {
		return value, nil
	}
	return "", errors.Join(err, fileErr)
}

func wasmRegistryRead(context.Context, wasmHostRequest) (any, error) {
	return nil, errWASMUnsupported
}

func wasmWindowsAudit(context.Context, string, wasmHostRequest) (any, error) {
	return nil, errWASMUnsupported
}
