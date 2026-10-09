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
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"undertow/internal/mux"
)

const WASMDestination = "wasm.undertow.invalid:0"
const WASMModuleLimit = 4 << 20
const WASMStdinLimit = 64 << 10
const wasmOutputLimit = 4 << 20
const wasmRuntimeLimit = 2 * time.Minute
const wasmMemoryPages = 256 // 16 MiB of guest linear memory.

var wasmSlots = make(chan struct{}, 2)

func OpenWASM(ctx context.Context, agent *mux.Mux, module []byte, args []string, stdin []byte) (*InteractiveSession, error) {
	if agent == nil {
		return nil, errors.New("agent is not connected")
	}
	request := MemoryRequest{Size: len(module), Args: args, Stdin: stdin}
	if err := validateWASMRequest(request, module); err != nil {
		return nil, err
	}
	stream, err := agent.Open(ctx, WASMDestination)
	if err != nil {
		return nil, err
	}
	return StartMemorySession(ctx, stream, bufio.NewReader(stream), request, module)
}

func validateWASMRequest(request MemoryRequest, module []byte) error {
	if err := validateMemoryRequest(request, module, WASMModuleLimit); err != nil {
		return err
	}
	if request.Language != "" {
		return errors.New("WASM modules do not take a script language")
	}
	if len(request.Stdin) > WASMStdinLimit {
		return errors.New("WASM stdin exceeds 64 KiB")
	}
	if len(module) < 8 || !bytes.Equal(module[:4], []byte{0, 'a', 's', 'm'}) {
		return errors.New("source is not a WebAssembly module")
	}
	return nil
}

type wasmFramedOutput struct {
	framed    framedOutput
	remaining *atomic.Int64
	exceeded  *atomic.Bool
	cancel    context.CancelFunc
}

func (w wasmFramedOutput) Write(p []byte) (int, error) {
	if w.remaining.Add(-int64(len(p))) < 0 {
		w.exceeded.Store(true)
		w.cancel()
		return 0, errors.New("WASM output exceeded 4 MiB")
	}
	return w.framed.Write(p)
}

func serveWASM(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(stream)
	line := make([]byte, 0, 256)
	var err error
	for len(line) < 128<<10 {
		var b byte
		b, err = reader.ReadByte()
		if err != nil {
			break
		}
		line = append(line, b)
		if b == '\n' {
			break
		}
	}
	if err != nil || len(line) == 0 || line[len(line)-1] != '\n' {
		RejectInteractive(stream, errors.New("invalid WASM request"))
		return
	}
	var request MemoryRequest
	if json.Unmarshal(line, &request) != nil || request.Size < 8 || request.Size > WASMModuleLimit || request.Language != "" || len(request.Stdin) > WASMStdinLimit || validateArgv(append([]string{"module"}, request.Args...)) != nil {
		RejectInteractive(stream, errors.New("invalid WASM request"))
		return
	}
	ctx, releaseToken, tokenErr := acquireTokenContext(ctx, request.TokenContextID)
	if tokenErr != nil {
		RejectInteractive(stream, tokenErr)
		return
	}
	defer releaseToken()
	restore, tokenErr := enterTokenThread(ctx)
	if tokenErr != nil {
		RejectInteractive(stream, tokenErr)
		return
	}
	defer func() {
		if err := restore(); err != nil {
			_ = WriteInteractiveError(stream, err)
		}
	}()
	select {
	case wasmSlots <- struct{}{}:
		defer func() { <-wasmSlots }()
	default:
		RejectInteractive(stream, errors.New("agent WASM concurrency limit reached (2 runs)"))
		return
	}
	if err := writeInteractiveFrame(stream, InteractiveReady, nil); err != nil {
		return
	}
	receiveTimer := time.AfterFunc(5*time.Minute, func() { _ = stream.Close() })
	defer receiveTimer.Stop()
	module := make([]byte, request.Size)
	if _, err := io.ReadFull(reader, module); err != nil {
		writeWASMResult(stream, -1, fmt.Errorf("receive WASM module: %w", err))
		return
	}
	receiveTimer.Stop()
	if len(module) < 8 || !bytes.Equal(module[:4], []byte{0, 'a', 's', 'm'}) {
		writeWASMResult(stream, -1, errors.New("source is not a WebAssembly module"))
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, wasmRuntimeLimit)
	defer cancel()
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()
	runtimeConfig := wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(wasmMemoryPages).WithCloseOnContextDone(true)
	wazeroRuntime := wazero.NewRuntimeWithConfig(runCtx, runtimeConfig)
	defer wazeroRuntime.Close(context.Background())
	if _, err := wasi_snapshot_preview1.Instantiate(runCtx, wazeroRuntime); err != nil {
		writeWASMResult(stream, -1, err)
		return
	}
	closeHost, err := instantiateWASMHost(runCtx, wazeroRuntime)
	if err != nil {
		writeWASMResult(stream, -1, err)
		return
	}
	defer closeHost()
	var writeMu sync.Mutex
	var remaining atomic.Int64
	var exceeded atomic.Bool
	remaining.Store(wasmOutputLimit)
	stdout := wasmFramedOutput{framed: framedOutput{stream: stream, mu: &writeMu, kind: InteractiveOutput}, remaining: &remaining, exceeded: &exceeded, cancel: cancel}
	stderr := wasmFramedOutput{framed: framedOutput{stream: stream, mu: &writeMu, kind: InteractiveStderr}, remaining: &remaining, exceeded: &exceeded, cancel: cancel}
	argv := append([]string{"undertow-module"}, request.Args...)
	config := wazero.NewModuleConfig().WithArgs(argv...).WithStdin(bytes.NewReader(request.Stdin)).WithStdout(stdout).WithStderr(stderr)
	_, err = wazeroRuntime.InstantiateWithConfig(runCtx, module, config)
	code := 0
	if err != nil {
		var exit *sys.ExitError
		if errors.As(err, &exit) {
			code = int(exit.ExitCode())
		} else {
			code = -1
		}
	}
	if exceeded.Load() {
		err = errors.New("WASM output exceeded 4 MiB")
		code = -1
	} else if runCtx.Err() != nil {
		err = fmt.Errorf("WASM runtime: %w", runCtx.Err())
		code = -1
	} else if code != -1 {
		err = nil
	}
	writeMu.Lock()
	writeWASMResult(stream, code, err)
	writeMu.Unlock()
	_, _ = io.Copy(io.Discard, stream)
}

func writeWASMResult(stream *mux.Stream, code int, err error) {
	if err != nil {
		_ = WriteInteractiveError(stream, err)
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	_ = writeInteractiveFrame(stream, InteractiveExit, result[:])
	_ = stream.CloseWrite()
}
