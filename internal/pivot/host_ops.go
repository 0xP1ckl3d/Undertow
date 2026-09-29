package pivot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var builtinArgCount = map[string][2]int{
	"pwd": {0, 0}, "ls": {0, 1}, "stat": {1, 1},
	"mkdir": {1, 1}, "rm": {1, 1}, "whoami": {0, 0},
	"ps": {0, 0}, "privileges": {0, 0}, "env": {0, 1},
	"interfaces": {0, 0}, "dns": {0, 0}, "route-table": {0, 0},
}

func validateBuiltin(name string, args []string) error {
	count, ok := builtinArgCount[name]
	if !ok {
		return fmt.Errorf("unknown built-in command %q", name)
	}
	if len(args) < count[0] || len(args) > count[1] {
		return fmt.Errorf("%s expects %d to %d arguments", name, count[0], count[1])
	}
	return nil
}

func runBuiltin(parent context.Context, name string, args []string) ExecResult {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := validateBuiltin(name, args); err != nil {
		return ExecResult{Error: err.Error()}
	}
	var output string
	var err error
	switch name {
	case "pwd":
		output, err = os.Getwd()
	case "ls":
		path := "."
		if len(args) == 1 {
			path = args[0]
		}
		var entries []os.DirEntry
		entries, err = os.ReadDir(path)
		if err == nil {
			var lines []string
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() {
					name += string(os.PathSeparator)
				}
				lines = append(lines, name)
			}
			output = strings.Join(lines, "\n")
		}
	case "stat":
		var info os.FileInfo
		info, err = os.Lstat(args[0])
		if err == nil {
			output = fmt.Sprintf("path: %s\nsize: %d\nmode: %s\nmodified: %s\ndirectory: %t", args[0], info.Size(), info.Mode(), info.ModTime().Format(time.RFC3339), info.IsDir())
		}
	case "mkdir":
		err = os.Mkdir(args[0], 0700)
		if err == nil {
			output = "created " + args[0]
		}
	case "rm":
		clean := filepath.Clean(args[0])
		if clean == "." || clean == string(os.PathSeparator) || filepath.VolumeName(clean)+string(os.PathSeparator) == clean {
			err = errors.New("refusing to remove a root or working directory")
		} else {
			err = os.Remove(clean)
		}
		if err == nil {
			output = "removed " + clean
		}
	case "whoami":
		var current *user.User
		current, err = user.Current()
		if err == nil {
			output = current.Username
		}
	case "ps", "privileges", "dns", "route-table":
		output, err = platformHostInfo(ctx, name)
	case "env":
		if len(args) == 1 {
			value, exists := os.LookupEnv(args[0])
			if !exists {
				err = fmt.Errorf("environment variable %q is not set", args[0])
			} else {
				output = args[0] + "=" + value
			}
		} else {
			values := os.Environ()
			sort.Strings(values)
			output = strings.Join(values, "\n")
		}
	case "interfaces":
		var interfaces []net.Interface
		interfaces, err = net.Interfaces()
		if err == nil {
			var lines []string
			for _, iface := range interfaces {
				lines = append(lines, fmt.Sprintf("%s index=%d mtu=%d flags=%s mac=%s", iface.Name, iface.Index, iface.MTU, iface.Flags, iface.HardwareAddr))
				addresses, addressErr := iface.Addrs()
				if addressErr != nil {
					lines = append(lines, "  addresses: "+addressErr.Error())
					continue
				}
				for _, address := range addresses {
					lines = append(lines, "  "+address.String())
				}
			}
			output = strings.Join(lines, "\n")
		}
	}
	if err != nil {
		return ExecResult{Error: err.Error()}
	}
	if len(output) > 32<<10 {
		output = output[:32<<10] + "\n[output truncated]"
	}
	if output != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	return ExecResult{Stdout: output}
}
