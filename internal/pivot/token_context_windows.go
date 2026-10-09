//go:build windows

package pivot

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
	"undertow/internal/authcontext"
)

func operationToken(ctx context.Context) windows.Token {
	t, _ := ctx.Value(tokenLeaseKey{}).(*authcontext.WindowsToken)
	if t == nil {
		return 0
	}
	return t.Native()
}
func operationTokenCanLaunch(ctx context.Context) bool {
	t, _ := ctx.Value(tokenLeaseKey{}).(*authcontext.WindowsToken)
	return t != nil && t.CanLaunch()
}
func configureTokenProcess(ctx context.Context, command *exec.Cmd) {
	if t := operationToken(ctx); t != 0 {
		if command.SysProcAttr == nil {
			command.SysProcAttr = &syscall.SysProcAttr{}
		}
		command.SysProcAttr.Token = syscall.Token(t)
	}
}

// Only the current goroutine's locked OS thread is impersonated. If restoration
// fails, never unlock it back into Go's thread pool; the caller must return so
// the runtime discards the contaminated thread.
func enterTokenThread(ctx context.Context) (func() error, error) {
	t := operationToken(ctx)
	if t == 0 {
		return func() error { return nil }, nil
	}
	runtime.LockOSThread()
	var prior windows.Token
	err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, true, &prior)
	if err != nil && err != windows.ERROR_NO_TOKEN {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("capture thread identity: %w", err)
	}
	result, _, callErr := impersonateLoggedOnUser.Call(uintptr(t))
	if result == 0 {
		if prior != 0 {
			_ = prior.Close()
		}
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("activate authentication context: %w", callErr)
	}
	return func() error {
		var err error
		if prior != 0 {
			err = windows.SetThreadToken(nil, prior)
			_ = prior.Close()
		} else {
			err = windows.RevertToSelf()
		}
		if err != nil {
			return fmt.Errorf("restore operation thread identity: %w", err)
		}
		runtime.UnlockOSThread()
		return nil
	}, nil
}
