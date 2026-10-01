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
	"time"

	"undertow/internal/mux"
	"undertow/internal/nativemodule"
)

const NativeDestination = "native.undertow.invalid:0"
const NativeModuleLimit = nativemodule.MaxSize
const nativeOutputLimit = 4 << 20
const nativeRuntimeLimit = 2 * time.Minute

var nativeSlots = make(chan struct{}, 2)

func OpenNative(ctx context.Context, agent *mux.Mux, module []byte, args []string, data []byte) (*InteractiveSession, error) {
	if agent == nil {
		return nil, errors.New("agent is not connected")
	}
	request := MemoryRequest{Size: len(module), Args: args, Stdin: data}
	if err := validateNativeRequest(request, module); err != nil {
		return nil, err
	}
	stream, err := agent.Open(ctx, NativeDestination)
	if err != nil {
		return nil, err
	}
	return StartMemorySession(ctx, stream, bufio.NewReader(stream), request, module)
}

func validateNativeRequest(request MemoryRequest, module []byte) error {
	if err := validateMemoryRequest(request, module, NativeModuleLimit); err != nil {
		return err
	}
	if request.Language != "" {
		return errors.New("native module does not take a script language")
	}
	if _, err := nativemodule.EncodeArgs(request.Args, request.Stdin); err != nil {
		return err
	}
	_, _, err := nativemodule.Parse(module)
	return err
}

func serveNative(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(stream)
	line := make([]byte, 0, 512)
	var err error
	for len(line) < 1<<20 {
		var next byte
		next, err = reader.ReadByte()
		if err != nil {
			break
		}
		line = append(line, next)
		if next == '\n' {
			break
		}
	}
	if err != nil || len(line) == 0 || line[len(line)-1] != '\n' {
		RejectInteractive(stream, errors.New("invalid native request"))
		return
	}
	var request MemoryRequest
	if json.Unmarshal(line, &request) != nil || request.Size < 9 || request.Size > NativeModuleLimit || request.Language != "" {
		RejectInteractive(stream, errors.New("invalid native request"))
		return
	}
	args, err := nativemodule.EncodeArgs(request.Args, request.Stdin)
	if err != nil {
		RejectInteractive(stream, err)
		return
	}
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
	defer receiveTimer.Stop()
	module := make([]byte, request.Size)
	if _, err := io.ReadFull(reader, module); err != nil {
		writeNativeResult(stream, -1, fmt.Errorf("receive native module: %w", err))
		return
	}
	receiveTimer.Stop()
	meta, dll, err := nativemodule.Parse(module)
	if err == nil {
		err = nativemodule.Compatible(meta)
	}
	if err != nil {
		writeNativeResult(stream, -1, err)
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, nativeRuntimeLimit)
	defer cancel()
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()
	var writeMu sync.Mutex
	write := func(kind byte, p []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeInteractiveFrame(stream, kind, p)
	}
	code, err := executeNative(runCtx, dll, args, write)
	if runCtx.Err() != nil {
		code, err = -1, fmt.Errorf("native module: %w", runCtx.Err())
	}
	writeMu.Lock()
	writeNativeResult(stream, code, err)
	writeMu.Unlock()
	_, _ = io.Copy(io.Discard, stream)
}

func writeNativeResult(stream *mux.Stream, code int, err error) {
	if err != nil {
		_ = WriteInteractiveError(stream, err)
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	_ = writeInteractiveFrame(stream, InteractiveExit, result[:])
	_ = stream.CloseWrite()
}
