//go:build windows && amd64

package pivot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These fields match sdk/native/undertow_native.h. All addresses in the table,
// including the table itself and its argument buffer, are native allocations.
type nativeAPI struct {
	Size       uint32
	Version    uint32
	Context    uintptr
	Write      uintptr
	WriteError uintptr
	Cancelled  uintptr
	JobState   uintptr
	OS         uint32
	Arch       uint32
	PID        uint32
	Reserved   uint32
}

type nativeRun struct {
	ctx   context.Context
	write func(byte, []byte) error
}

var nativeRuns = struct {
	sync.RWMutex
	items map[uintptr]*nativeRun
}{items: make(map[uintptr]*nativeRun)}
var nativeNextID atomic.Uint64

func nativeLookup(id uintptr) *nativeRun {
	nativeRuns.RLock()
	defer nativeRuns.RUnlock()
	return nativeRuns.items[id]
}

func nativeWrite(kind byte, id, ptr, length uintptr) uintptr {
	run := nativeLookup(id)
	if run == nil || run.ctx.Err() != nil || length > 32<<10 || length > 0 && ptr == 0 {
		return ^uintptr(0)
	}
	if length == 0 {
		return 0
	}
	data := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(length))...)
	for len(data) > 0 {
		n := len(data)
		if n > 8<<10 {
			n = 8 << 10
		}
		if err := run.write(kind, data[:n]); err != nil {
			return ^uintptr(0)
		}
		data = data[n:]
	}
	return 0
}

var nativeWriteCallback = syscall.NewCallback(func(id, ptr, length uintptr) uintptr { return nativeWrite(InteractiveOutput, id, ptr, length) })
var nativeErrorCallback = syscall.NewCallback(func(id, ptr, length uintptr) uintptr { return nativeWrite(InteractiveStderr, id, ptr, length) })
var nativeCancelCallback = syscall.NewCallback(func(id uintptr) uintptr {
	if run := nativeLookup(id); run != nil && run.ctx.Err() == nil {
		return 0
	}
	return 1
})
var nativeStateCallback = syscall.NewCallback(func(id uintptr) uintptr {
	if run := nativeLookup(id); run != nil && run.ctx.Err() == nil {
		return 0
	}
	return 1
})

func executeNative(ctx context.Context, dll, args []byte, write func(byte, []byte) error) (int, error) {
	dir, err := os.MkdirTemp("", "module-")
	if err != nil {
		return -1, fmt.Errorf("create native module directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "module.dll")
	if err := os.WriteFile(path, dll, 0600); err != nil {
		return -1, fmt.Errorf("write native module: %w", err)
	}
	// Restrict dependent DLL imports to System32. Module authors link ordinary
	// Windows import libraries; arbitrary sidecar DLLs are not part of v1.
	handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return -1, fmt.Errorf("load native module (check system DLL imports and PE relocations): %w", err)
	}
	defer windows.FreeLibrary(handle)
	entry, err := windows.GetProcAddress(handle, "undertow_main")
	if err != nil {
		return -1, fmt.Errorf("missing native entry point undertow_main: %w", err)
	}
	apiAddress, err := windows.VirtualAlloc(0, 4096, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return -1, fmt.Errorf("allocate native API table: %w", err)
	}
	defer windows.VirtualFree(apiAddress, 0, windows.MEM_RELEASE)
	argsAddress, err := windows.VirtualAlloc(0, uintptr(len(args)), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return -1, fmt.Errorf("allocate native arguments: %w", err)
	}
	defer windows.VirtualFree(argsAddress, 0, windows.MEM_RELEASE)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(argsAddress)), len(args)), args)
	id := uintptr(nativeNextID.Add(1))
	run := &nativeRun{ctx: ctx, write: write}
	nativeRuns.Lock()
	nativeRuns.items[id] = run
	nativeRuns.Unlock()
	defer func() { nativeRuns.Lock(); delete(nativeRuns.items, id); nativeRuns.Unlock() }()
	*(*nativeAPI)(unsafe.Pointer(apiAddress)) = nativeAPI{
		Size: uint32(unsafe.Sizeof(nativeAPI{})), Version: 1, Context: id,
		Write: nativeWriteCallback, WriteError: nativeErrorCallback,
		Cancelled: nativeCancelCallback, JobState: nativeStateCallback,
		OS: 1, Arch: 1, PID: uint32(os.Getpid()),
	}
	result, _, _ := syscall.SyscallN(entry, apiAddress, argsAddress, uintptr(len(args)))
	code := int(int32(result))
	if code != 0 {
		return code, fmt.Errorf("native module returned non-zero status %d", code)
	}
	return 0, nil
}
