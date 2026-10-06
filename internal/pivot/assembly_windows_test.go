//go:build windows && amd64

package pivot

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/assembly"
)

func TestAssemblyInspectorRejectsUnsupportedPE(t *testing.T) {
	valid := compileAssemblyFixture(t, `using System; class Program { static void Main() { Console.WriteLine("ok"); } }`, false)
	for _, sample := range [][]byte{nil, []byte("MZ"), valid[:100]} {
		if _, err := assembly.Inspect(sample); err == nil {
			t.Fatal("malformed assembly accepted")
		}
	}
	badMachine := append([]byte(nil), valid...)
	peOffset := binary.LittleEndian.Uint32(badMachine[0x3c:])
	binary.LittleEndian.PutUint16(badMachine[peOffset+4:], 0xaa64)
	if _, err := assembly.Inspect(badMachine); err == nil || !strings.Contains(err.Error(), "machine") {
		t.Fatalf("architecture error=%v", err)
	}
	badCLR := append([]byte(nil), valid...)
	// The PE32 CLR directory is the fifteenth data directory after the
	// fixed 96-byte optional-header prefix.
	optional := int(peOffset) + 24
	binary.LittleEndian.PutUint32(badCLR[optional+96+14*8:], 0)
	if _, err := assembly.Inspect(badCLR); err == nil || !strings.Contains(err.Error(), "CLR header") {
		t.Fatalf("CLR error=%v", err)
	}
}

func compileAssemblyFixture(t *testing.T, source string, library bool) []byte {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "fixture.cs")
	output := filepath.Join(dir, "fixture.exe")
	target := "/target:exe"
	if library {
		output, target = filepath.Join(dir, "fixture.dll"), "/target:library"
	}
	if err := os.WriteFile(input, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	compiler := filepath.Join(os.Getenv("SystemRoot"), "Microsoft.NET", "Framework64", "v4.0.30319", "csc.exe")
	if _, err := os.Stat(compiler); err != nil {
		t.Skip(".NET Framework compiler unavailable")
	}
	if outputText, err := exec.Command(compiler, "/nologo", target, "/out:"+output, input).CombinedOutput(); err != nil {
		t.Fatalf("compile fixture: %v: %s", err, outputText)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAssemblyMemoryExecution(t *testing.T) {
	program := compileAssemblyFixture(t, `using System; class Program { static int Main(string[] args) { Console.WriteLine("args="+String.Join("|",args)); Console.Error.WriteLine("stderr-ok"); return 7; } }`, false)
	if meta, err := assembly.Inspect(program); err != nil || meta.Architecture != "amd64" || !meta.Executable {
		t.Fatalf("inspect=%+v %v", meta, err)
	}
	for i := 0; i < 2; i++ {
		var stdout, stderr strings.Builder
		code, err := executeAssembly(context.Background(), program, []string{"héllo", "two words"}, func(kind byte, data []byte) error {
			if kind == InteractiveStderr {
				_, _ = stderr.Write(data)
			} else {
				_, _ = stdout.Write(data)
			}
			return nil
		})
		if code != 7 || err == nil || !strings.Contains(stdout.String(), "args=héllo|two words") || !strings.Contains(stderr.String(), "stderr-ok") {
			t.Fatalf("code=%d err=%v stdout=%q stderr=%q", code, err, stdout.String(), stderr.String())
		}
	}
}

func TestAssemblyLibraryAndCancellation(t *testing.T) {
	library := compileAssemblyFixture(t, `using System; using System.Threading; public class Program { public static int Main(string[] args) { if (args.Length>0) Thread.Sleep(30000); Console.WriteLine("library-ok"); return 0; } }`, true)
	if _, err := assembly.Inspect(library); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	code, err := executeAssembly(context.Background(), library, nil, func(_ byte, data []byte) error { _, _ = output.Write(data); return nil })
	if code != 0 || err != nil || !strings.Contains(output.String(), "library-ok") {
		t.Fatalf("code=%d err=%v output=%q", code, err, output.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, err = executeAssembly(ctx, library, []string{"wait"}, func(byte, []byte) error { return nil })
	if code != -1 || err == nil {
		t.Fatalf("cancel code=%d err=%v", code, err)
	}
}
