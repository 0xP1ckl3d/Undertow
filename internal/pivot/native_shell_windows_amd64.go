//go:build windows && amd64

package pivot

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeShellAPI struct {
	nativeAPI
	Read uintptr
}

var nativeReadCallback = syscall.NewCallback(func(id, ptr, capacity, length uintptr) uintptr {
	run := nativeLookup(id)
	if run == nil || run.input == nil || ptr == 0 || capacity == 0 || length == 0 {
		return ^uintptr(0)
	}
	for len(run.pending) == 0 {
		select {
		case data, ok := <-run.input:
			if !ok {
				*(*uintptr)(unsafe.Pointer(length)) = 0
				return 1
			}
			run.pending = data
		case <-run.ctx.Done():
			*(*uintptr)(unsafe.Pointer(length)) = 0
			return 1
		}
	}
	n := len(run.pending)
	if uintptr(n) > capacity {
		n = int(capacity)
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), n), run.pending[:n])
	run.pending = run.pending[n:]
	*(*uintptr)(unsafe.Pointer(length)) = uintptr(n)
	return 0
})

func executeNativeShell(ctx context.Context, dll []byte, input <-chan []byte, write func(byte, []byte) error) (int, error) {
	file, err := os.CreateTemp("", "native-shell-*.dll")
	if err != nil {
		return -1, fmt.Errorf("create native live shell file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(dll); err != nil {
		file.Close()
		return -1, fmt.Errorf("write native live shell: %w", err)
	}
	if err := file.Close(); err != nil {
		return -1, fmt.Errorf("write native live shell: %w", err)
	}
	handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return -1, fmt.Errorf("load native live shell: %w", err)
	}
	defer windows.FreeLibrary(handle)
	entry, err := windows.GetProcAddress(handle, "undertow_shell_main")
	if err != nil {
		return -1, fmt.Errorf("missing native live shell entry point undertow_shell_main: %w", err)
	}
	apiAddress, err := windows.VirtualAlloc(0, 4096, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return -1, fmt.Errorf("allocate native live shell API: %w", err)
	}
	defer windows.VirtualFree(apiAddress, 0, windows.MEM_RELEASE)
	id := uintptr(nativeNextID.Add(1))
	run := &nativeRun{ctx: ctx, write: write, input: input}
	nativeRuns.Lock()
	nativeRuns.items[id] = run
	nativeRuns.Unlock()
	defer func() {
		nativeRuns.Lock()
		delete(nativeRuns.items, id)
		nativeRuns.Unlock()
	}()
	*(*nativeShellAPI)(unsafe.Pointer(apiAddress)) = nativeShellAPI{
		nativeAPI: nativeAPI{
			Size: uint32(unsafe.Sizeof(nativeShellAPI{})), Version: 1, Context: id,
			Write: nativeWriteCallback, WriteError: nativeErrorCallback,
			Cancelled: nativeCancelCallback, JobState: nativeStateCallback,
			OS: 1, Arch: 1, PID: uint32(os.Getpid()),
		},
		Read: nativeReadCallback,
	}
	result, _, _ := syscall.SyscallN(entry, apiAddress)
	code := int(int32(result))
	if code != 0 {
		return code, fmt.Errorf("native live shell returned non-zero status %d", code)
	}
	return 0, nil
}
