//go:build windows

package pivot

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	logonUserW              = windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")
	impersonateLoggedOnUser = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateLoggedOnUser")
)

func logonWindowsCredential(credential *WindowsCredential) (windows.Token, error) {
	user, err := windows.UTF16PtrFromString(credential.Username)
	if err != nil {
		return 0, err
	}
	var domain *uint16
	if credential.Domain != "" {
		domain, err = windows.UTF16PtrFromString(credential.Domain)
		if err != nil {
			return 0, err
		}
	}
	password, err := windows.UTF16PtrFromString(credential.Password)
	if err != nil {
		return 0, err
	}
	const (
		logon32LogonNewCredentials = 9
		logon32ProviderWinNT50     = 3
	)
	var token windows.Token
	result, _, callErr := logonUserW.Call(
		uintptr(unsafe.Pointer(user)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(password)),
		logon32LogonNewCredentials, logon32ProviderWinNT50, uintptr(unsafe.Pointer(&token)),
	)
	if result == 0 {
		return 0, fmt.Errorf("create supplied Windows network identity: %w", callErr)
	}
	return token, nil
}

func withWindowsCredential(credential *WindowsCredential, action func()) error {
	if credential == nil {
		action()
		return nil
	}
	token, err := logonWindowsCredential(credential)
	if err != nil {
		return err
	}
	defer token.Close()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, callErr := impersonateLoggedOnUser.Call(uintptr(token))
	if result == 0 {
		return fmt.Errorf("activate supplied Windows network identity: %w", callErr)
	}
	defer windows.RevertToSelf()
	action()
	return nil
}
