//go:build !linux && !windows

package pivot

import (
	"context"
	"fmt"
)

func platformHostInfo(ctx context.Context, name string) (string, error) {
	switch name {
	case "ps":
		return inventoryCommand(ctx, "ps", "-eo", "pid,ppid,user,comm")
	case "privileges":
		return inventoryCommand(ctx, "id")
	case "dns":
		return inventoryCommand(ctx, "scutil", "--dns")
	case "route-table":
		return inventoryCommand(ctx, "netstat", "-rn")
	}
	return "", fmt.Errorf("unsupported host operation %q", name)
}
