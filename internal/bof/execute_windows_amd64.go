//go:build windows && amd64

package bof

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed bridge/bridge.dll
var bridgeDLL []byte

type bofRun struct {
	ctx    context.Context
	output func(bool, []byte) error
	count  atomic.Int64
}

var bofRuns = struct {
	sync.RWMutex
	items map[uintptr]*bofRun
}{items: make(map[uintptr]*bofRun)}
var bofNext atomic.Uint64

var bofOutputCallback = syscall.NewCallback(func(id, kind, ptr, length uintptr) uintptr {
	bofRuns.RLock()
	run := bofRuns.items[id]
	bofRuns.RUnlock()
	if run == nil || run.ctx.Err() != nil || length > 4<<20 || length > 0 && ptr == 0 {
		return ^uintptr(0)
	}
	if run.count.Add(int64(length)) > 4<<20 {
		return ^uintptr(0)
	}
	if length == 0 {
		return 0
	}
	data := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(length))...)
	if err := run.output(kind == 0x0d || kind == 1, data); err != nil {
		return ^uintptr(0)
	}
	return 0
})

func align(value, boundary uintptr) uintptr { return (value + boundary - 1) &^ (boundary - 1) }

type loadedSection struct {
	address uintptr
	offset  uintptr
}

func Execute(ctx context.Context, object, args []byte, output func(bool, []byte) error) (int, error) {
	parsed, err := Parse(object)
	if err != nil {
		return -1, err
	}
	if !parsed.Supported {
		return -1, fmt.Errorf("unsupported BOF: %s", parsed.Errors[0])
	}
	if len(args) < 4 || len(args) > MaxArguments+4 {
		return -1, errors.New("invalid BOF argument buffer")
	}
	if output == nil {
		return -1, errors.New("BOF output callback is missing")
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	// Beacon uses thread-local state in the bridge DLL. The entry point and
	// all synchronous callbacks must remain on one Windows thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	file, err := os.CreateTemp("", "undertow-bof-bridge-*.dll")
	if err != nil {
		return -1, err
	}
	bridgePath := file.Name()
	defer os.Remove(bridgePath)
	if _, err := file.Write(bridgeDLL); err != nil {
		file.Close()
		return -1, err
	}
	if err := file.Close(); err != nil {
		return -1, err
	}
	bridge, err := windows.LoadLibraryEx(bridgePath, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return -1, fmt.Errorf("load Beacon bridge: %w", err)
	}
	defer windows.FreeLibrary(bridge)
	enter, err := windows.GetProcAddress(bridge, "BofEnter")
	if err != nil {
		return -1, err
	}
	leave, err := windows.GetProcAddress(bridge, "BofLeave")
	if err != nil {
		return -1, err
	}
	resolved := make(map[string]uintptr)
	var libraries []windows.Handle
	defer func() {
		for i := len(libraries) - 1; i >= 0; i-- {
			windows.FreeLibrary(libraries[i])
		}
	}()
	loadedLibraries := make(map[string]windows.Handle)
	for _, imp := range parsed.WindowsImports {
		library := loadedLibraries[imp.Library]
		if library == 0 {
			library, err = windows.LoadLibraryEx(imp.Library, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
			if err != nil {
				return -1, fmt.Errorf("unresolved Windows import %s: %w", imp.Name, err)
			}
			loadedLibraries[imp.Library] = library
			libraries = append(libraries, library)
		}
		address, lookupErr := windows.GetProcAddress(library, imp.Export)
		if lookupErr != nil {
			return -1, fmt.Errorf("unresolved Windows import %s: %w", imp.Name, lookupErr)
		}
		resolved[imp.Name] = address
	}
	for _, imp := range parsed.BeaconImports {
		address, lookupErr := windows.GetProcAddress(bridge, imp.Export)
		if lookupErr != nil {
			return -1, fmt.Errorf("unresolved Beacon API import %s: %w", imp.Name, lookupErr)
		}
		resolved[imp.Name] = address
	}
	// One allocation keeps REL32 targets in range of every loaded section.
	sections := make([]loadedSection, len(parsed.Sections))
	total := uintptr(0)
	for i, section := range parsed.Sections {
		total = align(total, 16)
		sections[i].offset = total
		total += uintptr(section.Size)
		if total > 32<<20 {
			return -1, errors.New("BOF loaded sections exceed 32 MiB")
		}
	}
	imports := make(map[string]uintptr)
	for _, imp := range append(append([]Import(nil), parsed.WindowsImports...), parsed.BeaconImports...) {
		total = align(total, 16)
		imports[imp.Name] = total
		total += 16
	}
	total = align(total, 16)
	argumentOffset := total
	total += uintptr(len(args))
	if total == 0 || total > 36<<20 {
		return -1, errors.New("BOF loaded allocation exceeds limit")
	}
	base, err := windows.VirtualAlloc(0, total, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_EXECUTE_READWRITE)
	if err != nil {
		return -1, fmt.Errorf("allocate BOF sections: %w", err)
	}
	defer windows.VirtualFree(base, 0, windows.MEM_RELEASE)
	for i, section := range parsed.Sections {
		sections[i].address = base + sections[i].offset
		if len(section.Data) > 0 {
			copy(unsafe.Slice((*byte)(unsafe.Pointer(sections[i].address)), len(section.Data)), section.Data)
		}
	}
	for _, imp := range append(append([]Import(nil), parsed.WindowsImports...), parsed.BeaconImports...) {
		place := base + imports[imp.Name]
		target := resolved[imp.Name]
		if imp.Indirect {
			*(*uintptr)(unsafe.Pointer(place)) = target
		} else {
			// mov rax, imm64; jmp rax
			stub := unsafe.Slice((*byte)(unsafe.Pointer(place)), 12)
			stub[0] = 0x48
			stub[1] = 0xb8
			binary.LittleEndian.PutUint64(stub[2:10], uint64(target))
			stub[10] = 0xff
			stub[11] = 0xe0
		}
	}
	for _, reloc := range parsed.Relocations {
		if err := applyRelocation(parsed, sections, imports, base, reloc); err != nil {
			return -1, err
		}
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(base+argumentOffset)), len(args)), args)
	var entry uintptr
	for _, symbol := range parsed.Symbols {
		if !symbol.Auxiliary && symbol.Name == "go" && symbol.Section > 0 {
			entry = sections[symbol.Section-1].address + uintptr(symbol.Value)
			break
		}
	}
	if entry == 0 {
		return -1, errors.New("missing go entry point")
	}
	id := uintptr(bofNext.Add(1))
	run := &bofRun{ctx: ctx, output: output}
	bofRuns.Lock()
	bofRuns.items[id] = run
	bofRuns.Unlock()
	defer func() { bofRuns.Lock(); delete(bofRuns.items, id); bofRuns.Unlock() }()
	syscall.SyscallN(enter, bofOutputCallback, id)
	defer syscall.SyscallN(leave)
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	// BOF go has a void return type. Its status is zero if it returned.
	syscall.SyscallN(entry, base+argumentOffset, uintptr(len(args)))
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	if run.count.Load() > 4<<20 {
		return -1, errors.New("BOF output exceeded 4 MiB")
	}
	return 0, nil
}

func applyRelocation(c Compatibility, sections []loadedSection, imports map[string]uintptr, base uintptr, r Relocation) error {
	symbol := c.Symbols[r.SymbolIndex]
	var target uintptr
	var targetSection int16
	switch {
	case symbol.Section > 0:
		targetSection = symbol.Section
		if symbol.Value >= c.Sections[symbol.Section-1].Size {
			return fmt.Errorf("BOF symbol %s exceeds section", symbol.Name)
		}
		target = sections[symbol.Section-1].address + uintptr(symbol.Value)
	case symbol.Section == -1:
		target = uintptr(symbol.Value)
	case symbol.Section == 0:
		offset, ok := imports[symbol.Name]
		if !ok {
			return fmt.Errorf("unresolved BOF external symbol %s", symbol.Name)
		}
		target = base + offset
	default:
		return fmt.Errorf("unsupported BOF symbol section %d for %s", symbol.Section, symbol.Name)
	}
	place := sections[r.Section].address + uintptr(r.Offset)
	width := 4
	if r.Type == 1 {
		width = 8
	}
	if r.Type == 10 {
		width = 2
	}
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(place)), width)
	switch r.Type {
	case 1:
		binary.LittleEndian.PutUint64(bytes, uint64(target)+binary.LittleEndian.Uint64(bytes))
	case 2, 3:
		value := uint64(target) + uint64(binary.LittleEndian.Uint32(bytes))
		if r.Type == 3 {
			value = value - uint64(base)
		}
		if value > math.MaxUint32 {
			return fmt.Errorf("BOF relocation %s for %s exceeds 32-bit range", RelocationName(r.Type), symbol.Name)
		}
		binary.LittleEndian.PutUint32(bytes, uint32(value))
	case 4, 5, 6, 7, 8, 9:
		addend := int64(int32(binary.LittleEndian.Uint32(bytes)))
		displacement := int64(target) + addend - int64(place+4) - int64(r.Type-4)
		if displacement < math.MinInt32 || displacement > math.MaxInt32 {
			return fmt.Errorf("BOF relocation %s for %s is out of range", RelocationName(r.Type), symbol.Name)
		}
		binary.LittleEndian.PutUint32(bytes, uint32(int32(displacement)))
	case 10:
		if targetSection == 0 {
			return fmt.Errorf("BOF SECTION relocation for external symbol %s", symbol.Name)
		}
		value := uint32(binary.LittleEndian.Uint16(bytes)) + uint32(targetSection)
		if value > math.MaxUint16 {
			return fmt.Errorf("BOF SECTION relocation overflow for %s", symbol.Name)
		}
		binary.LittleEndian.PutUint16(bytes, uint16(value))
	case 11:
		if targetSection == 0 {
			return fmt.Errorf("BOF SECREL relocation for external symbol %s", symbol.Name)
		}
		binary.LittleEndian.PutUint32(bytes, binary.LittleEndian.Uint32(bytes)+symbol.Value)
	default:
		return fmt.Errorf("unsupported AMD64 COFF relocation %s", RelocationName(r.Type))
	}
	return nil
}
