//go:build linux

package pivot

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func platformHostInfo(ctx context.Context, name string) (string, error) {
	switch name {
	case "ps":
		return inventoryCommand(ctx, "ps", "-eo", "pid,ppid,user,comm")
	case "privileges":
		groups, err := os.Getgroups()
		if err != nil {
			return "", err
		}
		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			return "", err
		}
		lines := []string{fmt.Sprintf("uid=%d euid=%d gid=%d egid=%d groups=%v", os.Getuid(), os.Geteuid(), os.Getgid(), os.Getegid(), groups)}
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "CapInh:") || strings.HasPrefix(line, "CapPrm:") || strings.HasPrefix(line, "CapEff:") || strings.HasPrefix(line, "CapBnd:") || strings.HasPrefix(line, "NoNewPrivs:") {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n"), nil
	case "dns":
		content, err := os.ReadFile("/etc/resolv.conf")
		return string(content), err
	case "route-table":
		v4, err := inventoryCommand(ctx, "ip", "-4", "route", "show", "table", "all")
		if err != nil {
			return "", err
		}
		v6, err := inventoryCommand(ctx, "ip", "-6", "route", "show", "table", "all")
		return "IPv4:\n" + v4 + "IPv6:\n" + v6, err
	}
	return "", fmt.Errorf("unsupported host operation %q", name)
}
