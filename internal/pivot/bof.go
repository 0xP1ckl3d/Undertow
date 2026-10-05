package pivot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"undertow/internal/bof"
	"undertow/internal/mux"
)

const BOFDestination = "bof.undertow.invalid:0"
const BOFObjectLimit = bof.MaxObjectSize

func OpenBOF(ctx context.Context, agent *mux.Mux, object, arguments []byte) (*InteractiveSession, error) {
	if agent == nil {
		return nil, errors.New("agent is not connected")
	}
	if len(object) == 0 || len(object) > BOFObjectLimit {
		return nil, errors.New("BOF object exceeds 8 MiB limit or is empty")
	}
	compat, err := bof.Parse(object)
	if err != nil {
		return nil, err
	}
	if !compat.Supported {
		return nil, fmt.Errorf("unsupported BOF: %s", compat.Errors[0])
	}
	if len(arguments) < 4 || len(arguments) > bof.MaxArguments+4 {
		return nil, errors.New("invalid BOF argument buffer")
	}
	stream, err := agent.Open(ctx, BOFDestination)
	if err != nil {
		return nil, err
	}
	return StartMemorySession(ctx, stream, bufio.NewReader(stream), MemoryRequest{Size: len(object), Stdin: arguments}, object)
}

func serveBOF(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(stream)
	line := make([]byte, 0, 256)
	var err error
	for len(line) < 2<<20 {
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
		RejectInteractive(stream, errors.New("invalid BOF request"))
		return
	}
	var request MemoryRequest
	if json.Unmarshal(line, &request) != nil || request.Size < 20 || request.Size > BOFObjectLimit || request.Language != "" || len(request.Args) != 0 || len(request.Stdin) < 4 || len(request.Stdin) > bof.MaxArguments+4 {
		RejectInteractive(stream, errors.New("invalid BOF request"))
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
	timer := time.AfterFunc(5*time.Minute, func() { _ = stream.Close() })
	defer timer.Stop()
	object := make([]byte, request.Size)
	if _, err := io.ReadFull(reader, object); err != nil {
		writeNativeResult(stream, -1, fmt.Errorf("receive BOF object: %w", err))
		return
	}
	timer.Stop()
	compat, err := bof.Parse(object)
	if err == nil && !compat.Supported {
		err = fmt.Errorf("unsupported BOF: %s", compat.Errors[0])
	}
	if err != nil {
		writeNativeResult(stream, -1, err)
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()
	var mu sync.Mutex
	output := func(callback byte, data []byte) error {
		kind := InteractiveOutput
		if callback == bof.WorkerStderr {
			kind = InteractiveStderr
		} else if callback == bof.WorkerCallback {
			kind = InteractiveBOFCallback
		}
		mu.Lock()
		defer mu.Unlock()
		for len(data) > 0 {
			n := len(data)
			if n > 8<<10 {
				n = 8 << 10
			}
			if err := writeInteractiveFrame(stream, kind, data[:n]); err != nil {
				return err
			}
			data = data[n:]
		}
		return nil
	}
	code, runErr := executeBOFWorker(runCtx, object, request.Stdin, output)
	if runCtx.Err() != nil {
		code, runErr = -1, fmt.Errorf("BOF runtime: %w", runCtx.Err())
	}
	mu.Lock()
	writeNativeResult(stream, code, runErr)
	mu.Unlock()
	_, _ = io.Copy(io.Discard, stream)
}
