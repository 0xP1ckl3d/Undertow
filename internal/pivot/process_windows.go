//go:build windows

package pivot

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsProcessInventory uses the same process snapshot API as tasklist and
// avoids starting a child process for routine inventory requests.
func windowsProcessInventory(ctx context.Context) (string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return "", err
	}
	type process struct {
		pid, parent, threads uint32
		name                 string
	}
	var processes []process
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		processes = append(processes, process{entry.ProcessID, entry.ParentProcessID, entry.Threads, windows.UTF16ToString(entry.ExeFile[:])})
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return "", err
		}
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].pid < processes[j].pid })
	var out strings.Builder
	writer := csv.NewWriter(&out)
	if err := writer.Write([]string{"Image Name", "PID", "Parent PID", "Threads"}); err != nil {
		return "", err
	}
	for _, process := range processes {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := writer.Write([]string{process.name, fmt.Sprint(process.pid), fmt.Sprint(process.parent), fmt.Sprint(process.threads)}); err != nil {
			return "", err
		}
		writer.Flush()
		if out.Len() > 32<<10 {
			break
		}
	}
	writer.Flush()
	return out.String(), writer.Error()
}
