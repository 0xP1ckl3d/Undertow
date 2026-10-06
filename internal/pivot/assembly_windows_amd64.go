//go:build windows && amd64

package pivot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
)

// Windows PowerShell 5.1 hosts the installed .NET Framework CLR. The assembly
// bytes arrive on stdin; neither the assembly nor a generated script is written
// to the agent filesystem. The process is short lived and killed on cancellation.
const assemblyHost = `$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
try {
  $utf8=New-Object System.Text.UTF8Encoding($false)
  [Console]::InputEncoding=$utf8
  [Console]::OutputEncoding=$utf8
  $stdout=[System.IO.StreamWriter]::new([Console]::OpenStandardOutput(),$utf8,4096)
  $stdout.AutoFlush=$true
  [Console]::SetOut($stdout)
  $stderr=[System.IO.StreamWriter]::new([Console]::OpenStandardError(),$utf8,4096)
  $stderr.AutoFlush=$true
  [Console]::SetError($stderr)
  $request=[Console]::In.ReadToEnd() | ConvertFrom-Json
  $assembly=[Reflection.Assembly]::Load([Convert]::FromBase64String($request.source))
  $entry=$assembly.EntryPoint
  if ($null -eq $entry) {
    $flags=[Reflection.BindingFlags]::Static -bor [Reflection.BindingFlags]::Public -bor [Reflection.BindingFlags]::NonPublic
    $entries=@($assembly.GetTypes() | ForEach-Object { $_.GetMethods($flags) | Where-Object { $_.Name -eq 'Main' } })
    if ($entries.Count -ne 1) { throw 'Assembly DLL requires exactly one static Main method' }
    $entry=$entries[0]
  }
  $parameters=$entry.GetParameters()
  if ($parameters.Length -eq 0) { $invoke=@() }
  elseif ($parameters.Length -eq 1 -and $parameters[0].ParameterType -eq [string[]]) {
    $invoke=New-Object 'object[]' 1
    if ($null -eq $request.args) { $invoke[0]=[string[]]@() }
    else { $invoke[0]=[string[]]@($request.args) }
  } else { throw 'Assembly entry point must be Main() or Main(string[] args)' }
  $result=$entry.Invoke($null,$invoke)
  if ($result -is [System.Threading.Tasks.Task]) {
    $result.GetAwaiter().GetResult() | Out-Null
    if ($result.GetType().IsGenericType) { $result=$result.Result } else { $result=$null }
  }
  if ($result -is [int]) { exit $result }
  exit 0
} catch {
  [Console]::Error.WriteLine($_.Exception.ToString())
  exit 1
}`

type assemblyOutput struct {
	kind      byte
	write     func(byte, []byte) error
	remaining *atomic.Int64
}

func (o assemblyOutput) Write(data []byte) (int, error) {
	total := len(data)
	if o.remaining.Add(-int64(total)) < 0 {
		return 0, errors.New("assembly output exceeded 4 MiB")
	}
	for len(data) > 0 {
		n := len(data)
		if n > 8<<10 {
			n = 8 << 10
		}
		if err := o.write(o.kind, data[:n]); err != nil {
			return total - len(data), err
		}
		data = data[n:]
	}
	return total, nil
}

func executeAssembly(ctx context.Context, source []byte, args []string, write func(byte, []byte) error) (int, error) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	path := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	units := utf16.Encode([]rune(assemblyHost))
	encoded := make([]byte, len(units)*2)
	for i, unit := range units {
		encoded[2*i], encoded[2*i+1] = byte(unit), byte(unit>>8)
	}
	input, err := json.Marshal(struct {
		Source []byte   `json:"source"`
		Args   []string `json:"args"`
	}{Source: source, Args: args})
	if err != nil {
		return -1, err
	}
	command := exec.CommandContext(ctx, path, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Stdin = bytes.NewReader(input)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := command.Start(); err != nil {
		return -1, fmt.Errorf("start .NET Framework worker: %w", err)
	}
	var remaining atomic.Int64
	remaining.Store(4 << 20)
	var firstErr error
	var mu sync.Mutex
	var group sync.WaitGroup
	for _, pipe := range []struct {
		reader io.Reader
		kind   byte
	}{{stdout, InteractiveOutput}, {stderr, InteractiveStderr}} {
		group.Add(1)
		go func(reader io.Reader, kind byte) {
			defer group.Done()
			_, copyErr := io.Copy(assemblyOutput{kind: kind, write: write, remaining: &remaining}, reader)
			if copyErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = copyErr
				}
				mu.Unlock()
				_ = command.Process.Kill()
			}
		}(pipe.reader, pipe.kind)
	}
	done := make(chan error, 1)
	go func() { group.Wait(); done <- command.Wait() }()
	var waitErr error
	select {
	case <-ctx.Done():
		_ = command.Process.Kill()
		return -1, ctx.Err()
	case waitErr = <-done:
	}
	if firstErr != nil {
		return -1, firstErr
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode(), fmt.Errorf("assembly returned non-zero status %d", exitErr.ExitCode())
		}
		return -1, fmt.Errorf(".NET Framework worker: %w", waitErr)
	}
	return 0, nil
}
