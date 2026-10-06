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
	"runtime"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func run(ctx context.Context, method, target, path, executionContext, shortID string, output io.Writer) error {
	var err error
	switch method {
	case "winrm":
		err = runWinRM(ctx, target, path, "T"+shortID, output)
	case "wmi":
		err = runWMI(target, path, output)
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

func runWinRS(ctx context.Context, output io.Writer, target string, args ...string) error {
	all := append([]string{"-r:" + target}, args...)
	text, err := runCommand(ctx, output, "winrs.exe", all...)
	if err != nil && !strings.Contains(strings.ToUpper(text), "SUCCESS:") {
		return err
	}
	return nil
}

func runWinRM(ctx context.Context, target, path, taskName string, output io.Writer) error {
	create := []string{"schtasks.exe", "/Create", "/TN", taskName, "/SC", "ONCE", "/ST", "00:00", "/TR", path, "/IT", "/RL", "HIGHEST", "/F"}
	if err := runWinRS(ctx, output, target, create...); err != nil {
		return fmt.Errorf("create target-local WinRM task: %w", err)
	}
	defer cleanupWinRMTask(target, taskName, output)
	if err := runWinRS(ctx, output, target, "schtasks.exe", "/Run", "/TN", taskName); err != nil {
		return fmt.Errorf("run target-local WinRM task: %w", err)
	}
	fmt.Fprintln(output, "WinRM launch accepted")
	return waitForLaunch(ctx)
}

func cleanupWinRMTask(target, taskName string, output io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = runWinRS(ctx, output, target, "schtasks.exe", "/Delete", "/TN", taskName, "/F")
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

func runWMI(target, path string, output io.Writer) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil && !isOLECode(err, 1) {
		return fmt.Errorf("initialize COM: %w", err)
	}
	defer ole.CoUninitialize()
	locatorUnknown, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return fmt.Errorf("create WMI locator: %w", err)
	}
	defer locatorUnknown.Release()
	locator, err := locatorUnknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("open WMI locator: %w", err)
	}
	defer locator.Release()
	serviceValue, err := oleutil.CallMethod(locator, "ConnectServer", target, `root\cimv2`)
	if err != nil {
		return fmt.Errorf("connect remote WMI: %w", err)
	}
	defer serviceValue.Clear()
	service := serviceValue.ToIDispatch()
	securityValue, err := oleutil.GetProperty(service, "Security_")
	if err != nil {
		return fmt.Errorf("open WMI security settings: %w", err)
	}
	defer securityValue.Clear()
	security := securityValue.ToIDispatch()
	if _, err := oleutil.PutProperty(security, "ImpersonationLevel", 3); err != nil {
		return fmt.Errorf("set WMI impersonation: %w", err)
	}
	if _, err := oleutil.PutProperty(security, "AuthenticationLevel", 6); err != nil {
		return fmt.Errorf("set WMI authentication: %w", err)
	}
	classValue, err := oleutil.CallMethod(service, "Get", "Win32_Process")
	if err != nil {
		return fmt.Errorf("open Win32_Process: %w", err)
	}
	defer classValue.Clear()
	class := classValue.ToIDispatch()
	methodsValue, err := oleutil.GetProperty(class, "Methods_")
	if err != nil {
		return fmt.Errorf("open WMI methods: %w", err)
	}
	defer methodsValue.Clear()
	methodValue, err := oleutil.CallMethod(methodsValue.ToIDispatch(), "Item", "Create")
	if err != nil {
		return fmt.Errorf("open Win32_Process.Create: %w", err)
	}
	defer methodValue.Clear()
	inDefinitionValue, err := oleutil.GetProperty(methodValue.ToIDispatch(), "InParameters")
	if err != nil {
		return fmt.Errorf("open WMI input parameters: %w", err)
	}
	defer inDefinitionValue.Clear()
	inValue, err := oleutil.CallMethod(inDefinitionValue.ToIDispatch(), "SpawnInstance_")
	if err != nil {
		return fmt.Errorf("create WMI input parameters: %w", err)
	}
	defer inValue.Clear()
	if _, err := oleutil.PutProperty(inValue.ToIDispatch(), "CommandLine", `"`+path+`"`); err != nil {
		return fmt.Errorf("set WMI command line: %w", err)
	}
	outValue, err := oleutil.CallMethod(service, "ExecMethod_", "Win32_Process", "Create", inValue.ToIDispatch())
	if err != nil {
		return fmt.Errorf("execute Win32_Process.Create: %w", err)
	}
	defer outValue.Clear()
	out := outValue.ToIDispatch()
	returnValue, err := oleutil.GetProperty(out, "ReturnValue")
	if err != nil {
		return fmt.Errorf("read WMI return value: %w", err)
	}
	defer returnValue.Clear()
	if returnValue.Val != 0 {
		return fmt.Errorf("Win32_Process.Create returned %d", returnValue.Val)
	}
	processID, err := oleutil.GetProperty(out, "ProcessId")
	if err == nil {
		defer processID.Clear()
		fmt.Fprintf(output, "WMI process created with PID %d\n", processID.Val)
	} else {
		fmt.Fprintln(output, "WMI process created")
	}
	return nil
}

func isOLECode(err error, code uintptr) bool {
	var oleErr *ole.OleError
	return errors.As(err, &oleErr) && oleErr.Code() == code
}
