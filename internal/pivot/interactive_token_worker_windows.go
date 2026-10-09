//go:build windows

package pivot

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// The selected primary token launches a small worker using the documented
// STARTUPINFO contract. The worker creates the ConPTY and shell under its own
// process identity, avoiding STARTUPINFOEX in CreateProcessWithTokenW.
func startSelectedTokenConPTY(ctx context.Context, request InteractiveRequest) (interactiveProcess, io.ReadWriteCloser, func(uint16, uint16) error, error) {
	executable, cleanup, err := bofWorkerExecutable(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	process, raw, _, err := startPipedInteractiveProcess(ctx, executable, []string{"_shell-worker"})
	if err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	terminal := &selectedTokenTerminal{ReadWriteCloser: raw}
	request.TokenContextID = ""
	request.Credential = nil
	data, err := json.Marshal(request)
	if err == nil {
		err = terminal.frame(InteractiveReady, data)
	}
	if err == nil {
		var status [1]byte
		_, err = io.ReadFull(raw, status[:])
		if err == nil && status[0] != 1 {
			message, _ := io.ReadAll(io.LimitReader(raw, 1024))
			err = fmt.Errorf("start selected-token terminal: %s", message)
		}
	}
	if err != nil {
		_ = raw.Close()
		_ = process.Wait()
		cleanup()
		return nil, nil, nil, err
	}
	return &stagedInteractiveProcess{interactiveProcess: process, cleanup: cleanup}, terminal, terminal.resize, nil
}

type stagedInteractiveProcess struct {
	interactiveProcess
	cleanup func()
}

func (p *stagedInteractiveProcess) Wait() error {
	defer p.cleanup()
	return p.interactiveProcess.Wait()
}

type selectedTokenTerminal struct {
	io.ReadWriteCloser
	mu sync.Mutex
}

func (t *selectedTokenTerminal) frame(kind byte, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return writeInteractiveFrame(t.ReadWriteCloser, kind, data)
}

func (t *selectedTokenTerminal) Write(data []byte) (int, error) {
	if err := t.frame(InteractiveInput, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (t *selectedTokenTerminal) resize(cols, rows uint16) error {
	var data [4]byte
	binary.BigEndian.PutUint16(data[:2], cols)
	binary.BigEndian.PutUint16(data[2:], rows)
	return t.frame(InteractiveResize, data[:])
}

// ShellWorkerMain runs only in the short-lived child process. No token handle
// or credential is sent to it: Windows gives it the selected process identity.
func ShellWorkerMain(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	kind, data, err := readInteractiveFrame(reader)
	if err != nil || kind != InteractiveReady {
		return errors.New("invalid shell worker request")
	}
	var request InteractiveRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	if request.TokenContextID != "" || request.Credential != nil || request.NoPTY || validateInteractiveRequest(request) != nil {
		return errors.New("invalid shell worker context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process, terminal, resize, err := startInteractiveProcess(ctx, request)
	if err != nil {
		_, _ = output.Write(append([]byte{0}, []byte(err.Error())...))
		return err
	}
	defer terminal.Close()
	if _, err := output.Write([]byte{1}); err != nil {
		return err
	}
	outputDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, terminal)
		close(outputDone)
	}()
	go func() {
		defer cancel()
		for {
			kind, data, err := readInteractiveFrame(reader)
			if err != nil {
				return
			}
			switch kind {
			case InteractiveInput:
				if _, err := terminal.Write(data); err != nil {
					return
				}
			case InteractiveResize:
				if len(data) != 4 || resize(binary.BigEndian.Uint16(data[:2]), binary.BigEndian.Uint16(data[2:])) != nil {
					return
				}
			default:
				return
			}
		}
	}()
	err = process.Wait()
	_ = terminal.Close()
	<-outputDone
	return err
}
