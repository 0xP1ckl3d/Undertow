//go:build windows

package pivot

import (
	"context"
	"fmt"
)

func platformHostInfo(ctx context.Context, name string) (string, error) {
	switch name {
	case "ps":
		return inventoryCommand(ctx, "tasklist.exe", "/fo", "csv", "/v")
	case "privileges":
		return inventoryCommand(ctx, "whoami.exe", "/all")
	case "dns":
		return inventoryCommand(ctx, "ipconfig.exe", "/all")
	case "route-table":
		return inventoryCommand(ctx, "route.exe", "print")
	}
	return "", fmt.Errorf("unsupported host operation %q", name)
}
