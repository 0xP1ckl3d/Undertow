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

	"undertow/internal/assembly"
	"undertow/internal/mux"
)

const AssemblyDestination = "assembly.undertow.invalid:0"
const AssemblyLimit = assembly.MaxSize

func OpenAssembly(ctx context.Context, agent *mux.Mux, source []byte, args []string) (*InteractiveSession, error) {
	if agent == nil {
		return nil, errors.New("agent is not connected")
	}
	if err := ValidateAssembly(source, args); err != nil {
		return nil, err
	}
	stream, err := agent.Open(ctx, AssemblyDestination)
	if err != nil {
		return nil, err
	}
	return StartMemorySession(ctx, stream, bufio.NewReader(stream), MemoryRequest{Size: len(source), Args: args}, source)
}

func ValidateAssembly(source []byte, args []string) error {
	if err := validateMemoryRequest(MemoryRequest{Size: len(source), Args: args}, source, AssemblyLimit); err != nil {
		return err
	}
	if _, err := assembly.Inspect(source); err != nil {
		return err
	}
	return nil
}

func serveAssembly(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(stream)
	line := make([]byte, 0, 512)
	var err error
	for len(line) <= 128<<10 {
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
	if err != nil || len(line) == 0 || len(line) > 128<<10 || line[len(line)-1] != '\n' {
		RejectInteractive(stream, errors.New("invalid assembly request"))
		return
	}
	var request MemoryRequest
	if json.Unmarshal(line, &request) != nil || request.Size < 256 || request.Size > AssemblyLimit || request.Language != "" || len(request.Stdin) != 0 || validateArgv(append([]string{"assembly"}, request.Args...)) != nil {
		RejectInteractive(stream, errors.New("invalid assembly request"))
		return
	}
	select {
	case nativeSlots <- struct{}{}:
		defer func() { <-nativeSlots }()
	default:
		RejectInteractive(stream, errors.New("agent native concurrency limit reached (2 runs)"))
		return
	}
	if writeInteractiveFrame(stream, InteractiveReady, nil) != nil {
		return
	}
	receiveTimer := time.AfterFunc(5*time.Minute, func() { _ = stream.Close() })
	defer receiveTimer.Stop()
	source := make([]byte, request.Size)
	if _, err := io.ReadFull(reader, source); err != nil {
		writeAssemblyResult(stream, -1, fmt.Errorf("receive assembly: %w", err))
		return
	}
	receiveTimer.Stop()
	if _, err := assembly.Inspect(source); err != nil {
		writeAssemblyResult(stream, -1, err)
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()
	var mu sync.Mutex
	write := func(kind byte, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		return writeInteractiveFrame(stream, kind, data)
	}
	code, err := executeAssembly(runCtx, source, request.Args, write)
	if runCtx.Err() != nil {
		code, err = -1, fmt.Errorf("assembly: %w", runCtx.Err())
	}
	mu.Lock()
	writeAssemblyResult(stream, code, err)
	mu.Unlock()
	_, _ = io.Copy(io.Discard, stream)
}

func writeAssemblyResult(stream *mux.Stream, code int, err error) {
	if err != nil {
		_ = WriteInteractiveError(stream, err)
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	_ = writeInteractiveFrame(stream, InteractiveExit, result[:])
	_ = stream.CloseWrite()
}
