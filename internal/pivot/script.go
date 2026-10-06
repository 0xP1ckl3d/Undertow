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
	"os/exec"
	"runtime"
	"sync"
	"time"

	"undertow/internal/mux"
)

const ScriptDestination = "script.undertow.invalid:0"
const ScriptSourceLimit = 1 << 20

// MemoryRequest describes source bytes that follow its JSON line on the same
// authenticated stream. Source is never named as a path on the agent.
type MemoryRequest struct {
	Language string   `json:"language,omitempty"`
	Args     []string `json:"args,omitempty"`
	Stdin    []byte   `json:"stdin,omitempty"`
	Size     int      `json:"size"`
}

func validateMemoryRequest(request MemoryRequest, source []byte, limit int) error {
	if request.Size < 1 || request.Size > limit || request.Size != len(source) {
		return fmt.Errorf("source must contain 1 to %d bytes", limit)
	}
	if len(request.Args) > 0 {
		return validateArgv(append([]string{"source"}, request.Args...))
	}
	return nil
}

func OpenScript(ctx context.Context, agent *mux.Mux, language string, source []byte) (*InteractiveSession, error) {
	if agent == nil {
		return nil, errors.New("agent is not connected")
	}
	request := MemoryRequest{Language: language, Size: len(source)}
	if err := validateScriptRequest(request, source); err != nil {
		return nil, err
	}
	stream, err := agent.Open(ctx, ScriptDestination)
	if err != nil {
		return nil, err
	}
	return StartMemorySession(ctx, stream, bufio.NewReader(stream), request, source)
}

func validateScriptRequest(request MemoryRequest, source []byte) error {
	if err := validateMemoryRequest(request, source, ScriptSourceLimit); err != nil {
		return err
	}
	if request.Language != "bash" && request.Language != "powershell" {
		return errors.New("script interpreter must be bash or powershell")
	}
	if len(request.Args) != 0 || len(request.Stdin) != 0 {
		return errors.New("script arguments are not supported yet")
	}
	return nil
}

// StartMemorySession is shared by direct agent streams, VPN relays and local
// control upgrades. The source write runs beside output reads to avoid filling
// the transport window when a script writes while it consumes stdin.
func StartMemorySession(ctx context.Context, stream interface {
	io.ReadWriteCloser
	CloseWrite() error
}, reader *bufio.Reader, request MemoryRequest, source []byte) (*InteractiveSession, error) {
	if request.Size != len(source) || request.Size < 1 {
		stream.Close()
		return nil, errors.New("invalid source size")
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
		return nil, fmt.Errorf("agent task: %s", string(payload))
	}
	go func() {
		if _, err := io.Copy(stream, bytes.NewReader(source)); err != nil {
			_ = stream.Close()
			return
		}
		_ = stream.CloseWrite()
	}()
	return session, nil
}

type framedOutput struct {
	stream io.Writer
	mu     *sync.Mutex
	kind   byte
}

func (w framedOutput) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		n := len(p)
		if n > 8<<10 {
			n = 8 << 10
		}
		w.mu.Lock()
		err := writeInteractiveFrame(w.stream, w.kind, p[:n])
		w.mu.Unlock()
		if err != nil {
			return total - len(p), err
		}
		p = p[n:]
	}
	return total, nil
}

type exactSourceReader struct {
	reader io.Reader
	left   int64
}

func (r *exactSourceReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.left {
		p = p[:r.left]
	}
	n, err := r.reader.Read(p)
	r.left -= int64(n)
	if err == io.EOF && r.left > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func scriptExecutable(language string) (string, []string, error) {
	switch language {
	case "bash":
		path, err := exec.LookPath("bash")
		return path, []string{"-s"}, err
	case "powershell":
		candidates := []string{"pwsh", "powershell"}
		if runtime.GOOS == "windows" {
			candidates = []string{"powershell.exe", "pwsh.exe"}
		}
		for _, name := range candidates {
			if path, err := exec.LookPath(name); err == nil {
				// Windows PowerShell's `-Command -` consumes stdin as an
				// interactive command stream. Multi-line constructs such as
				// try blocks can then reach EOF without executing and still
				// return exit code zero. Read the complete source and compile it
				// as one script block so the in-memory script has file semantics.
				return path, []string{"-NoProfile", "-NonInteractive", "-Command", "$source=[Console]::In.ReadToEnd(); & ([ScriptBlock]::Create($source))"}, nil
			}
		}
		return "", nil, errors.New("PowerShell interpreter is not installed")
	default:
		return "", nil, errors.New("script interpreter must be bash or powershell")
	}
}

func serveScript(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(stream)
	line := make([]byte, 0, 128)
	var err error
	for len(line) < 8192 {
		b, readErr := reader.ReadByte()
		if readErr != nil {
			err = readErr
			break
		}
		line = append(line, b)
		if b == '\n' {
			break
		}
	}
	if err != nil || len(line) == 0 || line[len(line)-1] != '\n' {
		RejectInteractive(stream, errors.New("invalid script request"))
		return
	}
	var request MemoryRequest
	if json.Unmarshal(line, &request) != nil || request.Size < 1 || request.Size > ScriptSourceLimit || request.Language != "bash" && request.Language != "powershell" || len(request.Args) != 0 || len(request.Stdin) != 0 {
		RejectInteractive(stream, errors.New("invalid script request"))
		return
	}
	path, args, err := scriptExecutable(request.Language)
	if err != nil {
		RejectInteractive(stream, err)
		return
	}
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-commandCtx.Done():
		}
	}()
	command := exec.CommandContext(commandCtx, path, args...)
	configureExecProcess(command)
	command.Stdin = &exactSourceReader{reader: reader, left: int64(request.Size)}
	var writeMu sync.Mutex
	command.Stdout = framedOutput{stream: stream, mu: &writeMu, kind: InteractiveOutput}
	command.Stderr = framedOutput{stream: stream, mu: &writeMu, kind: InteractiveStderr}
	if err := command.Start(); err != nil {
		RejectInteractive(stream, err)
		return
	}
	if err := writeInteractiveFrame(stream, InteractiveReady, nil); err != nil {
		cancel()
		_ = command.Wait()
		return
	}
	err = command.Wait()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			code = -1
			writeMu.Lock()
			_ = WriteInteractiveError(stream, err)
			writeMu.Unlock()
		}
	}
	if commandCtx.Err() != nil {
		writeMu.Lock()
		_ = WriteInteractiveError(stream, commandCtx.Err())
		writeMu.Unlock()
		code = -1
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	writeMu.Lock()
	_ = writeInteractiveFrame(stream, InteractiveExit, result[:])
	writeMu.Unlock()
	_ = stream.CloseWrite()
	_, _ = io.Copy(io.Discard, stream)
}
