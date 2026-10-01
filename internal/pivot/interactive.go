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
)

// InteractiveDestination is deliberately separate from one-shot execution.
const InteractiveDestination = "interactive.undertow.invalid:0"
const InteractiveRelayDestination = "interactive-relay.undertow.invalid:0"

const (
	InteractiveReady      byte = 'R'
	InteractiveInput      byte = 'I'
	InteractiveOutput     byte = 'O'
	InteractiveStderr     byte = 'D'
	InteractiveResize     byte = 'Z'
	InteractiveExit       byte = 'X'
	InteractiveError      byte = 'E'
	interactiveFrameLimit      = 32 << 10
)

type InteractiveRequest struct {
	Argv []string `json:"argv,omitempty"`
	Cols uint16   `json:"cols,omitempty"`
	Rows uint16   `json:"rows,omitempty"`
}

type interactiveProcess interface {
	Wait() error
}

type InteractiveSession struct {
	stream interface {
		io.ReadWriteCloser
		CloseWrite() error
	}
	reader *bufio.Reader
	writes sync.Mutex
}

func OpenInteractive(ctx context.Context, agent *mux.Mux, request InteractiveRequest) (*InteractiveSession, error) {
	if agent == nil {
		return nil, errors.New("agent is not connected")
	}
	if len(request.Argv) != 0 {
		if err := validateArgv(request.Argv); err != nil {
			return nil, err
		}
	}
	stream, err := agent.Open(ctx, InteractiveDestination)
	if err != nil {
		return nil, err
	}
	return StartInteractive(ctx, stream, bufio.NewReader(stream), request)
}

// StartInteractive starts a session on an already opened mux stream or a local
// authenticated relay connection. reader must retain any handshake read-ahead.
func StartInteractive(ctx context.Context, stream interface {
	io.ReadWriteCloser
	CloseWrite() error
}, reader *bufio.Reader, request InteractiveRequest) (*InteractiveSession, error) {
	if len(request.Argv) != 0 {
		if err := validateArgv(request.Argv); err != nil {
			stream.Close()
			return nil, err
		}
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
		_, _ = io.Copy(io.Discard, session.reader)
		_ = stream.CloseWrite()
		stream.Close()
		return nil, fmt.Errorf("interactive agent: %s", string(payload))
	}
	return session, nil
}

func (s *InteractiveSession) Send(data []byte) error { return s.writeFrame(InteractiveInput, data) }
func (s *InteractiveSession) Resize(cols, rows uint16) error {
	var data [4]byte
	binary.BigEndian.PutUint16(data[:2], cols)
	binary.BigEndian.PutUint16(data[2:], rows)
	return s.writeFrame(InteractiveResize, data[:])
}
func (s *InteractiveSession) Read() (byte, []byte, error) {
	kind, data, err := readInteractiveFrame(s.reader)
	if err == nil && kind == InteractiveExit {
		_ = s.stream.CloseWrite()
	}
	return kind, data, err
}
func (s *InteractiveSession) Close() error { return s.stream.Close() }
func (s *InteractiveSession) writeFrame(kind byte, data []byte) error {
	s.writes.Lock()
	defer s.writes.Unlock()
	return writeInteractiveFrame(s.stream, kind, data)
}

func readInteractiveFrame(reader io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > interactiveFrameLimit {
		return 0, nil, errors.New("interactive frame exceeds limit")
	}
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	return header[0], data, err
}

func writeInteractiveFrame(writer io.Writer, kind byte, data []byte) error {
	if len(data) > interactiveFrameLimit {
		return errors.New("interactive frame exceeds limit")
	}
	var header [5]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}

func WriteInteractiveError(writer io.Writer, err error) error {
	message := err.Error()
	if len(message) > 1024 {
		message = message[:1024]
	}
	return writeInteractiveFrame(writer, InteractiveError, []byte(message))
}

func RejectInteractive(stream *mux.Stream, err error) {
	_ = WriteInteractiveError(stream, err)
	_ = stream.CloseWrite()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, stream); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func serveInteractive(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(stream)
	var request InteractiveRequest
	line := make([]byte, 0, 256)
	var err error
	for len(line) < 8192 {
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
	if err != nil || len(line) == 8192 || len(line) == 0 || line[len(line)-1] != '\n' || json.Unmarshal(line, &request) != nil {
		RejectInteractive(stream, errors.New("invalid interactive request"))
		return
	}
	if len(request.Argv) != 0 {
		if err := validateArgv(request.Argv); err != nil {
			RejectInteractive(stream, err)
			return
		}
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command, terminal, resize, err := startInteractiveProcess(commandCtx, request)
	if err != nil {
		RejectInteractive(stream, err)
		return
	}
	defer terminal.Close()
	if err := writeInteractiveFrame(stream, InteractiveReady, nil); err != nil {
		return
	}
	var writeMu sync.Mutex
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buffer := make([]byte, 8<<10)
		forward := true
		for {
			n, err := terminal.Read(buffer)
			if n > 0 && forward {
				writeMu.Lock()
				writeErr := writeInteractiveFrame(stream, InteractiveOutput, buffer[:n])
				writeMu.Unlock()
				if writeErr != nil {
					// Keep draining ConPTY while disconnect teardown closes it.
					forward = false
				}
			}
			if err != nil {
				return
			}
		}
	}()
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		for {
			kind, data, err := readInteractiveFrame(reader)
			if err != nil {
				cancel()
				return
			}
			switch kind {
			case InteractiveInput:
				if _, err := terminal.Write(data); err != nil {
					cancel()
					return
				}
			case InteractiveResize:
				if len(data) == 4 {
					_ = resize(binary.BigEndian.Uint16(data[:2]), binary.BigEndian.Uint16(data[2:]))
				}
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
		case <-commandCtx.Done():
		}
	}()
	err = command.Wait()
	_ = terminal.Close()
	<-outputDone
	code := 0
	if err != nil {
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			code = -1
		}
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	writeMu.Lock()
	_ = writeInteractiveFrame(stream, InteractiveExit, result[:])
	writeMu.Unlock()
	_ = stream.CloseWrite()
	select {
	case <-inputDone:
	case <-stream.Done():
	case <-time.After(3 * time.Second):
	}
}
