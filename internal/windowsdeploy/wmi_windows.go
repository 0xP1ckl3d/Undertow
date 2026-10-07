//go:build windows

package windowsdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Jump needs only a small part of IDispatch. Keeping that boundary here avoids
// linking a general-purpose automation package into every Windows agent.
const (
	clsctxInprocServer  = 1
	clsctxLocalServer   = 4
	dispatchMethod      = 1
	dispatchPropertyGet = 2
	dispatchPropertyPut = 4
	vtI4                = 3
	vtBSTR              = 8
	vtDispatch          = 9
	dispidPropertyPut   = -3
	rpcEChangedMode     = 0x80010106
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	oleaut32             = windows.NewLazySystemDLL("oleaut32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCLSIDFromProgID  = ole32.NewProc("CLSIDFromProgID")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSysAllocString   = oleaut32.NewProc("SysAllocString")
	procSysFreeString    = oleaut32.NewProc("SysFreeString")
	procVariantClear     = oleaut32.NewProc("VariantClear")
	iidIDispatch         = windows.GUID{Data1: 0x00020400, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidNull              windows.GUID
)

type dispatch struct{ vtbl *dispatchVTable }

type dispatchVTable struct {
	queryInterface   uintptr
	addRef           uintptr
	release          uintptr
	getTypeInfoCount uintptr
	getTypeInfo      uintptr
	getIDsOfNames    uintptr
	invoke           uintptr
}

type variant struct {
	vt        uint16
	reserved1 uint16
	reserved2 uint16
	reserved3 uint16
	value     int64
	padding   [8]byte
}

type dispatchParams struct {
	args       *variant
	namedArgs  *int32
	argCount   uint32
	namedCount uint32
}

func failedHRESULT(value uintptr) bool { return int32(value) < 0 }

func hresultError(operation string, value uintptr) error {
	return fmt.Errorf("%s: HRESULT 0x%08x", operation, uint32(value))
}

func (d *dispatch) release() {
	if d != nil {
		syscall.SyscallN(d.vtbl.release, uintptr(unsafe.Pointer(d)))
	}
}

func (d *dispatch) id(name string) (int32, error) {
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	names := []*uint16{wide}
	var id int32
	hr, _, _ := syscall.SyscallN(d.vtbl.getIDsOfNames, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&iidNull)), uintptr(unsafe.Pointer(&names[0])), 1, 0, uintptr(unsafe.Pointer(&id)))
	runtime.KeepAlive(names)
	if failedHRESULT(hr) {
		return 0, hresultError("resolve COM member "+name, hr)
	}
	return id, nil
}

func (d *dispatch) invoke(name string, flags uint16, values ...any) (variant, error) {
	id, err := d.id(name)
	if err != nil {
		return variant{}, err
	}
	args := make([]variant, len(values))
	cleanups := make([]func(), 0, len(values))
	for i, value := range values {
		entry, cleanup, err := makeVariant(value)
		if err != nil {
			for _, fn := range cleanups {
				fn()
			}
			return variant{}, err
		}
		args[len(values)-1-i] = entry
		if cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
	}
	defer func() {
		for _, fn := range cleanups {
			fn()
		}
	}()
	params := dispatchParams{argCount: uint32(len(args))}
	if len(args) != 0 {
		params.args = &args[0]
	}
	var propertyID int32
	if flags == dispatchPropertyPut {
		propertyID = dispidPropertyPut
		params.namedArgs = &propertyID
		params.namedCount = 1
	}
	var result variant
	var argError uint32
	hr, _, _ := syscall.SyscallN(d.vtbl.invoke, uintptr(unsafe.Pointer(d)), uintptr(id), uintptr(unsafe.Pointer(&iidNull)), 0, uintptr(flags), uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&result)), 0, uintptr(unsafe.Pointer(&argError)))
	runtime.KeepAlive(args)
	if failedHRESULT(hr) {
		return variant{}, hresultError("invoke COM member "+name, hr)
	}
	return result, nil
}

func makeVariant(value any) (variant, func(), error) {
	switch value := value.(type) {
	case string:
		wide, err := windows.UTF16PtrFromString(value)
		if err != nil {
			return variant{}, nil, err
		}
		bstr, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(wide)))
		if bstr == 0 {
			return variant{}, nil, errors.New("allocate COM string")
		}
		return variant{vt: vtBSTR, value: int64(bstr)}, func() { procSysFreeString.Call(bstr) }, nil
	case int:
		return variant{vt: vtI4, value: int64(value)}, nil, nil
	case *dispatch:
		return variant{vt: vtDispatch, value: int64(uintptr(unsafe.Pointer(value)))}, nil, nil
	default:
		return variant{}, nil, fmt.Errorf("unsupported COM argument %T", value)
	}
}

func clearVariant(value *variant) { procVariantClear.Call(uintptr(unsafe.Pointer(value))) }

func takeDispatch(value *variant) (*dispatch, error) {
	if value.vt != vtDispatch || value.value == 0 {
		clearVariant(value)
		return nil, fmt.Errorf("COM member returned type %d instead of object", value.vt)
	}
	result := (*dispatch)(unsafe.Pointer(uintptr(value.value)))
	value.vt, value.value = 0, 0
	return result, nil
}

func variantInt(value *variant) (int64, error) {
	defer clearVariant(value)
	switch value.vt {
	case 2: // VT_I2
		return int64(int16(value.value)), nil
	case vtI4:
		return int64(int32(value.value)), nil
	case 18, 19: // VT_UI2, VT_UI4
		return int64(uint32(value.value)), nil
	case 20, 21: // VT_I8, VT_UI8
		return value.value, nil
	default:
		return 0, fmt.Errorf("COM member returned non-integer type %d", value.vt)
	}
}

func createDispatch(progID string) (*dispatch, error) {
	wide, err := windows.UTF16PtrFromString(progID)
	if err != nil {
		return nil, err
	}
	var classID windows.GUID
	hr, _, _ := procCLSIDFromProgID.Call(uintptr(unsafe.Pointer(wide)), uintptr(unsafe.Pointer(&classID)))
	if failedHRESULT(hr) {
		return nil, hresultError("resolve WMI COM class", hr)
	}
	var result *dispatch
	hr, _, _ = procCoCreateInstance.Call(uintptr(unsafe.Pointer(&classID)), 0, clsctxInprocServer|clsctxLocalServer, uintptr(unsafe.Pointer(&iidIDispatch)), uintptr(unsafe.Pointer(&result)))
	if failedHRESULT(hr) {
		return nil, hresultError("create WMI COM class", hr)
	}
	return result, nil
}

func objectProperty(object *dispatch, name string) (*dispatch, error) {
	value, err := object.invoke(name, dispatchPropertyGet)
	if err != nil {
		return nil, err
	}
	return takeDispatch(&value)
}

func objectMethod(object *dispatch, name string, args ...any) (*dispatch, error) {
	value, err := object.invoke(name, dispatchMethod, args...)
	if err != nil {
		return nil, err
	}
	return takeDispatch(&value)
}

func runWMIProcess(target, commandLine string) (int64, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, 0)
	initialized := !failedHRESULT(hr)
	if failedHRESULT(hr) && uint32(hr) != rpcEChangedMode {
		return 0, hresultError("initialize COM", hr)
	}
	if initialized {
		defer procCoUninitialize.Call()
	}

	locator, err := createDispatch("WbemScripting.SWbemLocator")
	if err != nil {
		return 0, err
	}
	defer locator.release()
	service, err := objectMethod(locator, "ConnectServer", target, `root\cimv2`)
	if err != nil {
		return 0, fmt.Errorf("connect remote WMI: %w", err)
	}
	defer service.release()
	security, err := objectProperty(service, "Security_")
	if err != nil {
		return 0, fmt.Errorf("open WMI security settings: %w", err)
	}
	defer security.release()
	for name, setting := range map[string]int{"ImpersonationLevel": 3, "AuthenticationLevel": 6} {
		value, err := security.invoke(name, dispatchPropertyPut, setting)
		if err != nil {
			return 0, fmt.Errorf("set WMI %s: %w", name, err)
		}
		clearVariant(&value)
	}
	class, err := objectMethod(service, "Get", "Win32_Process")
	if err != nil {
		return 0, fmt.Errorf("open Win32_Process: %w", err)
	}
	defer class.release()
	methods, err := objectProperty(class, "Methods_")
	if err != nil {
		return 0, fmt.Errorf("open WMI methods: %w", err)
	}
	defer methods.release()
	method, err := objectMethod(methods, "Item", "Create")
	if err != nil {
		return 0, fmt.Errorf("open Win32_Process.Create: %w", err)
	}
	defer method.release()
	inputDefinition, err := objectProperty(method, "InParameters")
	if err != nil {
		return 0, fmt.Errorf("open WMI input parameters: %w", err)
	}
	defer inputDefinition.release()
	input, err := objectMethod(inputDefinition, "SpawnInstance_")
	if err != nil {
		return 0, fmt.Errorf("create WMI input parameters: %w", err)
	}
	defer input.release()
	value, err := input.invoke("CommandLine", dispatchPropertyPut, commandLine)
	if err != nil {
		return 0, fmt.Errorf("set WMI command line: %w", err)
	}
	clearVariant(&value)
	result, err := objectMethod(class, "ExecMethod_", "Create", input)
	if err != nil {
		return 0, fmt.Errorf("execute Win32_Process.Create: %w", err)
	}
	defer result.release()
	returnValue, err := result.invoke("ReturnValue", dispatchPropertyGet)
	if err != nil {
		return 0, fmt.Errorf("read WMI return value: %w", err)
	}
	code, err := variantInt(&returnValue)
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, fmt.Errorf("Win32_Process.Create returned %d", code)
	}
	processID, err := result.invoke("ProcessId", dispatchPropertyGet)
	if err != nil {
		return 0, fmt.Errorf("read WMI process ID: %w", err)
	}
	id, err := variantInt(&processID)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func quoteWindowsArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}
	var quoted strings.Builder
	quoted.WriteByte('"')
	backslashes := 0
	for _, character := range value {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			quoted.WriteString(strings.Repeat("\\", backslashes*2+1))
			quoted.WriteByte('"')
			backslashes = 0
			continue
		}
		quoted.WriteString(strings.Repeat("\\", backslashes))
		backslashes = 0
		quoted.WriteRune(character)
	}
	quoted.WriteString(strings.Repeat("\\", backslashes*2))
	quoted.WriteByte('"')
	return quoted.String()
}

func windowsCommandLine(program string, arguments ...string) string {
	parts := make([]string, 0, len(arguments)+1)
	parts = append(parts, quoteWindowsArgument(program))
	for _, argument := range arguments {
		parts = append(parts, quoteWindowsArgument(argument))
	}
	return strings.Join(parts, " ")
}

func waitWMI(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runWMI(ctx context.Context, target, path, taskName string, output io.Writer) error {
	const scheduler = `C:\Windows\System32\schtasks.exe`
	create := windowsCommandLine(scheduler, "/Create", "/TN", taskName, "/SC", "ONCE", "/ST", "00:00", "/TR", path, "/IT", "/RL", "HIGHEST", "/F")
	pid, err := runWMIProcess(target, create)
	if err != nil {
		return fmt.Errorf("start target-local task registration through WMI: %w", err)
	}
	fmt.Fprintf(output, "WMI started target-local task registration %s with PID %d\n", taskName, pid)
	if err := waitWMI(ctx, 2*time.Second); err != nil {
		return err
	}
	run := windowsCommandLine(scheduler, "/Run", "/TN", taskName)
	pid, err = runWMIProcess(target, run)
	if err != nil {
		return fmt.Errorf("start target-local task through WMI: %w", err)
	}
	fmt.Fprintf(output, "WMI started target-local task %s with PID %d\n", taskName, pid)
	if err := waitWMI(ctx, 2*time.Second); err != nil {
		return err
	}
	remove := windowsCommandLine(scheduler, "/Delete", "/TN", taskName, "/F")
	pid, err = runWMIProcess(target, remove)
	if err != nil {
		fmt.Fprintf(output, "WMI task cleanup could not be started: %v\n", err)
	} else {
		fmt.Fprintf(output, "WMI started cleanup for task %s with PID %d\n", taskName, pid)
	}
	return nil
}
