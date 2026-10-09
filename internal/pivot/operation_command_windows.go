//go:build windows

package pivot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var createProcessWithTokenW = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateProcessWithTokenW")

type tokenProcessCreator func(windows.Token, *uint16, []uint16, bool, uint32, *uint16, *uint16, *windows.StartupInfo, *windows.ProcessInformation) error

func rawCreateProcessAsUser(token windows.Token, application *uint16, commandLine []uint16, inherit bool, flags uint32, environment *uint16, directory *uint16, startup *windows.StartupInfo, info *windows.ProcessInformation) error {
	line := append([]uint16(nil), commandLine...)
	return windows.CreateProcessAsUser(token, application, &line[0], nil, nil, inherit, flags, environment, directory, startup, info)
}

var createProcessAsUserCall tokenProcessCreator = rawCreateProcessAsUser

var createProcessWithTokenCall tokenProcessCreator = func(token windows.Token, application *uint16, commandLine []uint16, _ bool, flags uint32, environment *uint16, directory *uint16, startup *windows.StartupInfo, info *windows.ProcessInformation) error {
	line := append([]uint16(nil), commandLine...)
	result, _, err := createProcessWithTokenW.Call(
		uintptr(token), 0, uintptr(unsafe.Pointer(application)), uintptr(unsafe.Pointer(&line[0])),
		uintptr(flags), uintptr(unsafe.Pointer(environment)), uintptr(unsafe.Pointer(directory)),
		uintptr(unsafe.Pointer(startup)), uintptr(unsafe.Pointer(info)),
	)
	if result == 0 {
		return err
	}
	return nil
}

func createProcessWithSelectedToken(token windows.Token, application *uint16, commandLine []uint16, inherit bool, flags uint32, environment *uint16, directory *uint16, startup *windows.StartupInfo, info *windows.ProcessInformation) error {
	return launchWithPreparedCaller(token, application, commandLine, inherit, flags, environment, directory, startup, info)
}

type selectedTokenProcess struct {
	handle windows.Handle
	io     sync.WaitGroup
	mu     sync.Mutex
	ioErr  error
	done   chan struct{}
	inputs []io.Closer
}

func (p *selectedTokenProcess) copy(dst io.Writer, src io.Reader, closeDst, closeSrc, wait bool) {
	if wait {
		p.io.Add(1)
	}
	go func() {
		if wait {
			defer p.io.Done()
		}
		if closeSrc {
			defer func() { _ = src.(io.Closer).Close() }()
		}
		_, err := io.Copy(dst, src)
		if closeDst {
			if closer, ok := dst.(io.Closer); ok {
				_ = closer.Close()
			}
		}
		if err != nil && !errors.Is(err, os.ErrClosed) {
			p.mu.Lock()
			if p.ioErr == nil {
				p.ioErr = err
			}
			p.mu.Unlock()
		}
	}()
}

func duplicateCommandFile(value any) (any, bool) {
	file, ok := value.(*os.File)
	if !ok {
		return value, false
	}
	current, _ := windows.GetCurrentProcess()
	var duplicate windows.Handle
	if windows.DuplicateHandle(current, windows.Handle(file.Fd()), current, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS) != nil {
		return value, false
	}
	return os.NewFile(uintptr(duplicate), file.Name()+"-token-bridge"), true
}

func (p *selectedTokenProcess) Wait() error {
	defer close(p.done)
	defer windows.CloseHandle(p.handle)
	state, err := windows.WaitForSingleObject(p.handle, windows.INFINITE)
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("wait for selected-token process: state %d", state)
	}
	var code uint32
	if err = windows.GetExitCodeProcess(p.handle, &code); err != nil {
		return err
	}
	for _, input := range p.inputs {
		_ = input.Close()
	}
	p.io.Wait()
	p.mu.Lock()
	ioErr := p.ioErr
	p.mu.Unlock()
	if ioErr != nil {
		return ioErr
	}
	if code != 0 {
		return operationExitError{code: int(int32(code))}
	}
	return nil
}

func (p *selectedTokenProcess) Kill() error { return windows.TerminateProcess(p.handle, 1) }

func sameWriter(a, b io.Writer) bool {
	if a == nil || b == nil {
		return false
	}
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	return av.Kind() == reflect.Pointer && bv.Kind() == reflect.Pointer && av.Type() == bv.Type() && av.Pointer() == bv.Pointer()
}

func makeSelectedTokenPipe(childReads bool) (parent, child *os.File, err error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	if childReads {
		parent, child = write, read
	} else {
		parent, child = read, write
	}
	if err = windows.SetHandleInformation(windows.Handle(child.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		read.Close()
		write.Close()
		return nil, nil, err
	}
	_ = windows.SetHandleInformation(windows.Handle(parent.Fd()), windows.HANDLE_FLAG_INHERIT, 0)
	return parent, child, nil
}

func startOperationCommand(ctx context.Context, command *exec.Cmd) (operationProcess, error) {
	token := operationToken(ctx)
	if token == 0 {
		if err := command.Start(); err != nil {
			return nil, err
		}
		return &execOperationProcess{command: command}, nil
	}
	if !operationTokenCanLaunch(ctx) {
		return nil, errors.New("selected authentication context supports impersonation but lacks process-launch token rights")
	}
	if len(command.Args) == 0 || command.Path == "" {
		return nil, errors.New("selected-token process has no executable")
	}
	stdinParent, stdinChild, err := makeSelectedTokenPipe(true)
	if err != nil {
		return nil, err
	}
	stdoutParent, stdoutChild, err := makeSelectedTokenPipe(false)
	if err != nil {
		stdinParent.Close()
		stdinChild.Close()
		return nil, err
	}
	stderrParent, stderrChild := stdoutParent, stdoutChild
	sharedOutput := sameWriter(command.Stdout, command.Stderr)
	if !sharedOutput {
		stderrParent, stderrChild, err = makeSelectedTokenPipe(false)
		if err != nil {
			stdinParent.Close()
			stdinChild.Close()
			stdoutParent.Close()
			stdoutChild.Close()
			return nil, err
		}
	}
	cleanup := func() {
		stdinParent.Close()
		stdinChild.Close()
		stdoutParent.Close()
		stdoutChild.Close()
		if !sharedOutput {
			stderrParent.Close()
			stderrChild.Close()
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer attributes.Delete()
	handles := []windows.Handle{windows.Handle(stdinChild.Fd()), windows.Handle(stdoutChild.Fd())}
	if !sharedOutput {
		handles = append(handles, windows.Handle(stderrChild.Fd()))
	}
	if err = attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		cleanup()
		return nil, err
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES, StdInput: handles[0], StdOutput: handles[1], StdErr: windows.Handle(stderrChild.Fd())}, ProcThreadAttributeList: attributes.List()}
	if command.SysProcAttr != nil && command.SysProcAttr.HideWindow {
		startup.StartupInfo.Flags |= windows.STARTF_USESHOWWINDOW
		startup.StartupInfo.ShowWindow = windows.SW_HIDE
	}
	application, err := windows.UTF16PtrFromString(command.Path)
	if err != nil {
		cleanup()
		return nil, err
	}
	line, err := windows.UTF16FromString(windows.ComposeCommandLine(command.Args))
	if err != nil {
		cleanup()
		return nil, err
	}
	var directory *uint16
	if command.Dir != "" {
		directory, err = windows.UTF16PtrFromString(command.Dir)
		if err != nil {
			cleanup()
			return nil, err
		}
	}
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if command.SysProcAttr != nil {
		flags |= command.SysProcAttr.CreationFlags
	}
	var info windows.ProcessInformation
	err = createProcessWithSelectedToken(token, application, line, true, flags, nil, directory, &startup.StartupInfo, &info)
	if err != nil {
		cleanup()
		return nil, err
	}
	_ = windows.CloseHandle(info.Thread)
	stdinChild.Close()
	stdoutChild.Close()
	if !sharedOutput {
		stderrChild.Close()
	}
	process := &selectedTokenProcess{handle: info.Process, done: make(chan struct{})}
	if command.Stdin == nil {
		stdinParent.Close()
	} else {
		stdin, duplicated := duplicateCommandFile(command.Stdin)
		if duplicated {
			process.inputs = append(process.inputs, stdin.(io.Closer))
		} else if closer, ok := stdin.(io.Closer); ok {
			process.inputs = append(process.inputs, closer)
		}
		process.inputs = append(process.inputs, stdinParent)
		// A terminal reader may remain open after the child exits. Do not make
		// Wait depend on that reader; closing the bridge on process exit ends
		// the copier without delaying Job or Live-shell completion.
		process.copy(stdinParent, stdin.(io.Reader), true, duplicated, false)
	}
	stdout := command.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stdoutValue, stdoutDuplicated := duplicateCommandFile(stdout)
	_, stdoutCloseable := stdoutValue.(io.Closer)
	_, stdoutIsFile := stdoutValue.(*os.File)
	process.copy(stdoutValue.(io.Writer), stdoutParent, stdoutDuplicated || stdoutCloseable && !stdoutIsFile, true, true)
	if !sharedOutput {
		stderr := command.Stderr
		if stderr == nil {
			stderr = io.Discard
		}
		stderrValue, stderrDuplicated := duplicateCommandFile(stderr)
		_, stderrCloseable := stderrValue.(io.Closer)
		_, stderrIsFile := stderrValue.(*os.File)
		process.copy(stderrValue.(io.Writer), stderrParent, stderrDuplicated || stderrCloseable && !stderrIsFile, true, true)
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = process.Kill()
		case <-process.done:
		}
	}()
	return process, nil
}
