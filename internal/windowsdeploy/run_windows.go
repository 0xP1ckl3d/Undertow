//go:build windows

package windowsdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"
)

func run(ctx context.Context, method, target, path, executionContext, shortID string, output io.Writer) error {
	var err error
	switch method {
	case "winrm":
		err = runWinRM(ctx, target, path, "T"+shortID, output)
	case "wmi":
		err = runWMI(ctx, target, path, "T"+shortID, output)
	case "service-control":
		err = runService(ctx, target, path, "S"+shortID, output)
	case "scheduled-task":
		err = runScheduledTask(ctx, target, path, executionContext, "T"+shortID, output)
	default:
		return errors.New("unsupported Windows deployment method")
	}
	if err != nil {
		if cleanupErr := removeTargetFile(target, path); cleanupErr != nil {
			fmt.Fprintf(output, "Target file cleanup failed: %v\n", cleanupErr)
		} else {
			fmt.Fprintln(output, "Removed target file after launch failure")
		}
	}
	return err
}

func runCommand(ctx context.Context, output io.Writer, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	data, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(data))
	if text != "" {
		fmt.Fprintln(output, text)
	}
	if err != nil {
		return text, fmt.Errorf("%s failed: %w", name, err)
	}
	return text, nil
}

func targetSharePath(target, path string) (string, error) {
	if len(path) < 4 || path[1:3] != `:\` {
		return "", errors.New("invalid target path")
	}
	const windowsRoot = `C:\Windows\`
	if len(path) >= len(windowsRoot) && strings.EqualFold(path[:len(windowsRoot)], windowsRoot) {
		return `\\` + target + `\ADMIN$\` + path[len(windowsRoot):], nil
	}
	return `\\` + target + `\` + strings.ToUpper(path[:1]) + `$\` + path[3:], nil
}

func removeTargetFile(target, path string) error {
	share, err := targetSharePath(target, path)
	if err != nil {
		return err
	}
	err = os.Remove(share)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

const winRMLaunchMarker = "Undertow WinRM launch accepted"

func winRMArguments(target, path string) []string {
	return []string{"-r:" + target, path, "_jump-launch"}
}

func runWinRM(ctx context.Context, target, path, _ string, output io.Writer) error {
	launchContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	text, err := runCommand(launchContext, output, "winrs.exe", winRMArguments(target, path)...)
	if !strings.Contains(text, winRMLaunchMarker) {
		if err != nil {
			return fmt.Errorf("launch through WinRM: %w", err)
		}
		return errors.New("launch through WinRM did not return its confirmation marker")
	}
	return nil
}

func runService(ctx context.Context, target, path, serviceName string, output io.Writer) error {
	remote := `\\` + target
	if _, err := runCommand(ctx, output, "sc.exe", remote, "create", serviceName, "binPath=", `"`+path+`"`, "start=", "demand", "obj=", "LocalSystem"); err != nil {
		return fmt.Errorf("create remote service: %w", err)
	}
	if _, err := runCommand(ctx, output, "sc.exe", remote, "start", serviceName); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = runCommand(cleanup, output, "sc.exe", remote, "delete", serviceName)
		return fmt.Errorf("start remote service: %w", err)
	}
	fmt.Fprintf(output, "Service %s started\n", serviceName)
	return nil
}

func currentTaskIdentity(target string) (string, error) {
	account, err := user.Current()
	if err != nil || !strings.Contains(account.Username, `\`) {
		return "", errors.New("current-user task requires a resolvable domain identity")
	}
	parts := strings.SplitN(account.Username, `\`, 2)
	host, _ := os.Hostname()
	if strings.EqualFold(parts[0], host) && !strings.EqualFold(target, host) && !strings.EqualFold(target, "localhost") {
		return "", errors.New("current-user task cannot map a source-local account on a different target")
	}
	return account.Username, nil
}

func runScheduledTask(ctx context.Context, target, path, executionContext, taskName string, output io.Writer) error {
	args := []string{"/Create", "/S", target, "/TN", taskName, "/SC", "ONCE", "/ST", "00:00", "/TR", path, "/RL", "HIGHEST", "/F"}
	if executionContext == "local-system" {
		args = append(args, "/RU", "SYSTEM")
	} else {
		identity, err := currentTaskIdentity(target)
		if err != nil {
			return err
		}
		args = append(args, "/RU", identity, "/IT")
	}
	if _, err := runCommand(ctx, output, "schtasks.exe", args...); err != nil {
		return fmt.Errorf("create remote scheduled task: %w", err)
	}
	defer cleanupScheduledTask(target, taskName, output)
	if _, err := runCommand(ctx, output, "schtasks.exe", "/Run", "/S", target, "/TN", taskName); err != nil {
		return fmt.Errorf("run remote scheduled task: %w", err)
	}
	fmt.Fprintf(output, "Scheduled task %s launched\n", taskName)
	return waitForLaunch(ctx)
}

func cleanupScheduledTask(target, taskName string, output io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = runCommand(ctx, output, "schtasks.exe", "/Delete", "/S", target, "/TN", taskName, "/F")
}

func waitForLaunch(ctx context.Context) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
