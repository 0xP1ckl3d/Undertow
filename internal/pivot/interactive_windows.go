//go:build windows

package pivot

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A ConPTY owns the remote echo, line editing, VT output, and console control
// handling. The local console only forwards raw input and renders this output.
type interactiveConPTY struct {
	input       *os.File
	output      *os.File
	console     windows.Handle
	mu          sync.Mutex
	closed      bool
	readStarted bool
}

func (p *interactiveConPTY) Read(b []byte) (int, error) {
	p.mu.Lock()
	p.readStarted = true
	p.mu.Unlock()
	n, err := p.output.Read(b)
	if err != nil {
		_ = p.output.Close()
	}
	return n, err
}

func (p *interactiveConPTY) Write(b []byte) (int, error) { return p.input.Write(b) }

func (p *interactiveConPTY) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	readStarted := p.readStarted
	p.mu.Unlock()
	_ = p.input.Close()
	// The output reader must keep draining while ConPTY emits its final frame.
	windows.ClosePseudoConsole(p.console)
	if !readStarted {
		_ = p.output.Close()
	}
	return nil
}

func (p *interactiveConPTY) resize(cols, rows uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}
	return windows.ResizePseudoConsole(p.console, terminalSize(cols, rows))
}

func terminalSize(cols, rows uint16) windows.Coord {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return windows.Coord{X: int16(min(cols, 32767)), Y: int16(min(rows, 32767))}
}

type interactiveWindowsProcess struct {
	handle windows.Handle
	done   chan struct{}
}

type interactiveWindowsExit struct{ code int }

var updateProcThreadAttribute = windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")

func attachPseudoConsole(list *windows.ProcThreadAttributeList, console windows.Handle) error {
	// This Windows attribute takes the HPCON handle value itself as lpValue.
	// x/sys/windows.Update expects a pointer, so call the API with the value.
	result, _, err := updateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(list)), 0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(console), unsafe.Sizeof(console), 0, 0,
	)
	if result == 0 {
		return err
	}
	return nil
}

func (e interactiveWindowsExit) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e interactiveWindowsExit) ExitCode() int { return e.code }

func (p *interactiveWindowsProcess) Wait() error {
	defer close(p.done)
	defer windows.CloseHandle(p.handle)
	state, err := windows.WaitForSingleObject(p.handle, windows.INFINITE)
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("wait for interactive process: state %d", state)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.handle, &code); err != nil {
		return err
	}
	if code != 0 {
		return interactiveWindowsExit{code: int(int32(code))}
	}
	return nil
}

func startInteractiveProcess(ctx context.Context, request InteractiveRequest) (interactiveProcess, io.ReadWriteCloser, func(uint16, uint16) error, error) {
	argv := request.Argv
	if len(argv) == 0 {
		argv = []string{"cmd.exe"}
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, nil, nil, err
	}
	app, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, nil, err
	}
	arguments := make([]string, len(argv))
	arguments[0] = windows.EscapeArg(path)
	for i := 1; i < len(argv); i++ {
		arguments[i] = windows.EscapeArg(argv[i])
	}
	line, err := windows.UTF16FromString(strings.Join(arguments, " "))
	if err != nil {
		return nil, nil, nil, err
	}

	var inputRead, inputWrite, outputRead, outputWrite windows.Handle
	defer func() {
		for _, handle := range []windows.Handle{inputRead, inputWrite, outputRead, outputWrite} {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
		}
	}()
	if err := windows.CreatePipe(&inputRead, &inputWrite, nil, 0); err != nil {
		return nil, nil, nil, err
	}
	if err := windows.CreatePipe(&outputRead, &outputWrite, nil, 0); err != nil {
		return nil, nil, nil, err
	}
	var console windows.Handle
	if err := windows.CreatePseudoConsole(terminalSize(request.Cols, request.Rows), inputRead, outputWrite, 0, &console); err != nil {
		return nil, nil, nil, fmt.Errorf("create Windows pseudoconsole: %w", err)
	}
	defer func() {
		if console != 0 {
			windows.ClosePseudoConsole(console)
		}
	}()
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, nil, nil, err
	}
	defer attributes.Delete()
	if err := attachPseudoConsole(attributes.List(), console); err != nil {
		return nil, nil, nil, err
	}
	startup := windows.StartupInfoEx{
		// Null standard handles prevent a child launched by a console-attached
		// agent from writing into the agent's own terminal instead of ConPTY.
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES},
		ProcThreadAttributeList: attributes.List(),
	}
	var processInfo windows.ProcessInformation
	if err := windows.CreateProcess(app, &line[0], nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &startup.StartupInfo, &processInfo); err != nil {
		return nil, nil, nil, err
	}
	_ = windows.CloseHandle(processInfo.Thread)
	// ConPTY holds its own copies. Releasing these ends lets the host reader see
	// EOF when the console closes.
	_ = windows.CloseHandle(inputRead)
	inputRead = 0
	_ = windows.CloseHandle(outputWrite)
	outputWrite = 0
	terminal := &interactiveConPTY{
		input:   os.NewFile(uintptr(inputWrite), "conpty-input"),
		output:  os.NewFile(uintptr(outputRead), "conpty-output"),
		console: console,
	}
	inputWrite, outputRead, console = 0, 0, 0
	process := &interactiveWindowsProcess{handle: processInfo.Process, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = terminal.Close()
		case <-process.done:
		}
	}()
	return process, terminal, terminal.resize, nil
}
