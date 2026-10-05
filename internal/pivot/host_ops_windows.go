//go:build windows

package pivot

import (
	"context"
	"fmt"
)

func platformHostInfo(ctx context.Context, name string) (string, error) {
	switch name {
	case "ps":
		return windowsProcessInventory(ctx)
	case "privileges":
		return inventoryCommand(ctx, "whoami.exe", "/all")
	case "dns":
		return inventoryCommand(ctx, "ipconfig.exe", "/all")
	case "route-table":
		return inventoryCommand(ctx, "route.exe", "print")
	}
	return "", fmt.Errorf("unsupported host operation %q", name)
}
