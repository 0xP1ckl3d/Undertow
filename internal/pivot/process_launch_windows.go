//go:build windows

package pivot

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	seImpersonate        = "SeImpersonatePrivilege"
	seAssignPrimaryToken = "SeAssignPrimaryTokenPrivilege"
	seIncreaseQuota      = "SeIncreaseQuotaPrivilege"
)

func privilegeState(token windows.Token, name string) (windows.LUID, bool, bool, error) {
	label, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.LUID{}, false, false, err
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, label, &luid); err != nil {
		return windows.LUID{}, false, false, err
	}
	var size uint32
	_ = windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size)
	if size == 0 {
		return luid, false, false, errors.New("token privilege information unavailable")
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buffer[0], size, &size); err != nil {
		return luid, false, false, err
	}
	for _, privilege := range (*windows.Tokenprivileges)(unsafe.Pointer(&buffer[0])).AllPrivileges() {
		if privilege.Luid == luid {
			return luid, true, privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0, nil
		}
	}
	return luid, false, false, nil
}

func enableLaunchPrivilege(token windows.Token, name string) error {
	luid, present, enabled, err := privilegeState(token, name)
	if err != nil {
		return fmt.Errorf("query %s: %w", name, err)
	}
	if !present {
		return fmt.Errorf("%s is absent from the process-launch caller token", name)
	}
	if enabled {
		return nil
	}
	state := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}}}
	if err := windows.AdjustTokenPrivileges(token, false, &state, 0, nil, nil); err != nil {
		return fmt.Errorf("enable %s: %w", name, err)
	}
	_, present, enabled, err = privilegeState(token, name)
	if err != nil || !present || !enabled {
		return fmt.Errorf("%s could not be enabled on the process-launch caller token", name)
	}
	return nil
}

func launchTokenSession(token windows.Token) (uint32, error) {
	var session, size uint32
	if err := windows.GetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session)), &size); err != nil {
		return 0, err
	}
	return session, nil
}

// The caller identity is a short-lived impersonation duplicate of the
// original agent process token. It is never the selected child token, and its
// privilege changes cannot modify the agent process or stored context.
func launchWithPreparedCaller(selected windows.Token, application *uint16, commandLine []uint16, inherit bool, flags uint32, environment *uint16, directory *uint16, startup *windows.StartupInfo, info *windows.ProcessInformation) (resultErr error) {
	runtime.LockOSThread()
	unlock := true
	defer func() {
		if unlock {
			runtime.UnlockOSThread()
		}
	}()
	var prior windows.Token
	err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, true, &prior)
	if err != nil && !errors.Is(err, windows.ERROR_NO_TOKEN) {
		return fmt.Errorf("capture process-launch thread identity: %w", err)
	}
	if prior != 0 {
		defer prior.Close()
	}
	if err := windows.RevertToSelf(); err != nil {
		// A failed revert leaves the OS thread's effective identity unknown.
		// Retire this thread rather than returning it to unrelated operations.
		unlock = false
		return fmt.Errorf("revert before process launch: %w", err)
	}
	defer func() {
		if prior != 0 {
			err = windows.SetThreadToken(nil, prior)
		} else {
			err = windows.RevertToSelf()
		}
		if err == nil {
			return
		}
		// Never return a thread with an unknown identity to the runtime pool.
		unlock = false
		if info.Process != 0 {
			_ = windows.TerminateProcess(info.Process, 1)
			_ = windows.CloseHandle(info.Thread)
			_ = windows.CloseHandle(info.Process)
			*info = windows.ProcessInformation{}
		}
		resultErr = fmt.Errorf("restore process-launch thread identity: %w", err)
	}()
	var processToken windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &processToken); err != nil {
		return fmt.Errorf("open process-launch caller token: %w", err)
	}
	defer processToken.Close()
	_, canImpersonate, _, err := privilegeState(processToken, seImpersonate)
	if err != nil {
		return err
	}
	_, canAssign, _, err := privilegeState(processToken, seAssignPrimaryToken)
	if err != nil {
		return err
	}
	_, canIncreaseQuota, _, err := privilegeState(processToken, seIncreaseQuota)
	if err != nil {
		return err
	}
	useWithToken := canImpersonate
	if !useWithToken && (!canAssign || !canIncreaseQuota) {
		return errors.New("process-launch caller lacks SeImpersonatePrivilege and the SeAssignPrimaryTokenPrivilege/SeIncreaseQuotaPrivilege pair")
	}
	if !useWithToken && inherit {
		var callerSession uint32
		if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &callerSession); err != nil {
			return fmt.Errorf("read caller session: %w", err)
		}
		selectedSession, err := launchTokenSession(selected)
		if err != nil {
			return fmt.Errorf("read selected token session: %w", err)
		}
		if selectedSession != callerSession {
			return errors.New("CreateProcessAsUserW cannot inherit standard handles across sessions")
		}
	}
	var caller windows.Token
	if err := windows.DuplicateTokenEx(processToken, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE|windows.TOKEN_ADJUST_PRIVILEGES, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &caller); err != nil {
		return fmt.Errorf("duplicate process-launch caller token: %w", err)
	}
	defer caller.Close()
	if useWithToken {
		if err := enableLaunchPrivilege(caller, seImpersonate); err != nil {
			return err
		}
	} else {
		for _, name := range []string{seAssignPrimaryToken, seIncreaseQuota} {
			if err := enableLaunchPrivilege(caller, name); err != nil {
				return err
			}
		}
	}
	if err := windows.SetThreadToken(nil, caller); err != nil {
		return fmt.Errorf("activate process-launch caller: %w", err)
	}
	if environment == nil {
		flags &^= windows.CREATE_UNICODE_ENVIRONMENT
	}
	if useWithToken {
		// Secondary Logon rejects the extended handle-list startup used by
		// CreateProcessAsUser. Its STARTUPINFO contract still accepts inheritable
		// standard handles, so retain those fields and use the plain structure.
		plainStartup := *startup
		plainStartup.Cb = uint32(unsafe.Sizeof(windows.StartupInfo{}))
		flags &^= windows.EXTENDED_STARTUPINFO_PRESENT
		if err := createProcessWithTokenCall(selected, application, commandLine, inherit, flags, environment, directory, &plainStartup, info); err != nil {
			return fmt.Errorf("CreateProcessWithTokenW: %w", err)
		}
	} else if err := createProcessAsUserCall(selected, application, commandLine, inherit, flags, environment, directory, startup, info); err != nil {
		return fmt.Errorf("CreateProcessAsUserW: %w", err)
	}
	return nil
}
