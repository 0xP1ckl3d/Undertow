//go:build !linux && !windows

package pivot

import "context"

func wasmPlatformInventory(ctx context.Context, op string) (any, error) {
	switch op {
	case "processes":
		return platformHostInfo(ctx, "ps")
	case "privileges":
		return platformHostInfo(ctx, "privileges")
	case "routes":
		return platformHostInfo(ctx, "route-table")
	case "dns_config":
		return platformHostInfo(ctx, "dns")
	}
	return nil, errWASMUnsupported
}

func wasmRegistryRead(context.Context, wasmHostRequest) (any, error) { return nil, errWASMUnsupported }

func wasmWindowsAudit(context.Context, string, wasmHostRequest) (any, error) {
	return nil, errWASMUnsupported
}
