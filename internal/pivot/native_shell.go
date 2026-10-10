package pivot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"undertow/internal/mux"
	"undertow/internal/nativemodule"
)

const NativeShellDestination = "native-shell.undertow.invalid:0"

func OpenNativeShell(ctx context.Context, agent *mux.Mux, module []byte) (*InteractiveSession, error) {
	if agent == nil {
		return nil, errors.New("agent is not connected")
	}
	request := MemoryRequest{Size: len(module)}
	if err := validateNativeShellRequest(request, module); err != nil {
		return nil, err
	}
	stream, err := agent.Open(ctx, NativeShellDestination)
	if err != nil {
		return nil, err
	}
	return StartNativeShellSession(ctx, stream, bufio.NewReader(stream), request, module)
}

func validateNativeShellRequest(request MemoryRequest, module []byte) error {
	if err := validateMemoryRequest(request, module, NativeModuleLimit); err != nil {
		return err
	}
	if request.Language != "" || len(request.Args) != 0 || len(request.Stdin) != 0 {
		return errors.New("native live shell accepts only a module payload")
	}
	_, _, err := nativemodule.Parse(module)
	return err
}

// StartNativeShellSession uploads a native module without closing the write
// side because subsequent bytes are framed live terminal input.
func StartNativeShellSession(ctx context.Context, stream interface {
	io.ReadWriteCloser
	CloseWrite() error
}, reader *bufio.Reader, request MemoryRequest, module []byte) (*InteractiveSession, error) {
	if request.TokenContextID == "" {
		request.TokenContextID = TokenContextID(ctx)
	}
	if err := ValidateTokenContextID(request.TokenContextID); err != nil {
		stream.Close()
		return nil, err
	}
	if err := validateNativeShellRequest(request, module); err != nil {
		stream.Close()
		return nil, err
	}
	session := &InteractiveSession{stream: stream, reader: reader}
	ready := make(chan struct{})
	watchStopped := make(chan struct{})
	defer func() { close(ready); <-watchStopped }()
	go func() {
		defer close(watchStopped)
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-ready:
		}
	}()
	if err := json.NewEncoder(stream).Encode(request); err != nil {
		stream.Close()
		return nil, err
	}
	kind, payload, err := session.Read()
	if err != nil {
		stream.Close()
		return nil, err
	}
	if kind != InteractiveReady {
		stream.Close()
		return nil, fmt.Errorf("native live shell agent: %s", string(payload))
	}
	if _, err := io.Copy(stream, bytes.NewReader(module)); err != nil {
		stream.Close()
		return nil, err
	}
	return session, nil
}

func serveNativeShell(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(stream)
	line := make([]byte, 0, 512)
	for len(line) < 1<<20 {
		next, err := reader.ReadByte()
		if err != nil {
			RejectInteractive(stream, errors.New("invalid native live shell request"))
			return
		}
		line = append(line, next)
		if next == '\n' {
			break
		}
	}
	var request MemoryRequest
	if len(line) == 0 || line[len(line)-1] != '\n' || json.Unmarshal(line, &request) != nil || request.Size < 9 || request.Size > NativeModuleLimit || request.Language != "" || len(request.Args) != 0 || len(request.Stdin) != 0 {
		RejectInteractive(stream, errors.New("invalid native live shell request"))
		return
	}
	ctx, releaseToken, err := acquireTokenContext(ctx, request.TokenContextID)
	if err != nil {
		RejectInteractive(stream, err)
		return
	}
	defer releaseToken()
	restore, err := enterTokenThread(ctx)
	if err != nil {
		RejectInteractive(stream, err)
		return
	}
	defer func() { _ = restore() }()
	select {
	case nativeSlots <- struct{}{}:
		defer func() { <-nativeSlots }()
	default:
		RejectInteractive(stream, errors.New("agent native concurrency limit reached (2 runs)"))
		return
	}
	if err := writeInteractiveFrame(stream, InteractiveReady, nil); err != nil {
		return
	}
	receiveTimer := time.AfterFunc(5*time.Minute, func() { _ = stream.Close() })
	module := make([]byte, request.Size)
	if _, err := io.ReadFull(reader, module); err != nil {
		receiveTimer.Stop()
		writeNativeResult(stream, -1, fmt.Errorf("receive native live shell module: %w", err))
		return
	}
	receiveTimer.Stop()
	if err := validateNativeShellRequest(request, module); err != nil {
		writeNativeResult(stream, -1, err)
		return
	}
	meta, dll, err := nativemodule.Parse(module)
	if err == nil {
		err = nativemodule.Compatible(meta)
	}
	if err != nil {
		writeNativeResult(stream, -1, err)
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	input := make(chan []byte, 32)
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		defer close(input)
		for {
			kind, data, err := readInteractiveFrame(reader)
			if err != nil {
				cancel()
				return
			}
			switch kind {
			case InteractiveInput:
				select {
				case input <- data:
				case <-runCtx.Done():
					return
				}
			case InteractiveResize:
				// The in-process runspace has no pseudoconsole to resize.
			default:
				cancel()
				return
			}
		}
	}()
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()
	var writeMu sync.Mutex
	write := func(kind byte, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeInteractiveFrame(stream, kind, data)
	}
	code, runErr := executeNativeShell(runCtx, dll, input, write)
	if runCtx.Err() != nil && runErr == nil {
		runErr = fmt.Errorf("native live shell: %w", runCtx.Err())
		code = -1
	}
	cancel()
	writeMu.Lock()
	if runErr != nil {
		_ = WriteInteractiveError(stream, runErr)
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	_ = writeInteractiveFrame(stream, InteractiveExit, result[:])
	writeMu.Unlock()
	_ = stream.CloseWrite()
	select {
	case <-inputDone:
	case <-time.After(3 * time.Second):
	}
}
