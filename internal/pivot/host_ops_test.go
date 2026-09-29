package pivot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinFileOperations(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "created")
	if result := runBuiltin(context.Background(), "mkdir", []string{dir}); result.Error != "" {
		t.Fatal(result.Error)
	}
	if result := runBuiltin(context.Background(), "ls", []string{root}); result.Error != "" || !strings.Contains(result.Stdout, "created") {
		t.Fatalf("list directory: %+v", result)
	}
	if result := runBuiltin(context.Background(), "stat", []string{dir}); result.Error != "" || !strings.Contains(result.Stdout, "directory: true") {
		t.Fatalf("stat directory: %+v", result)
	}
	if result := runBuiltin(context.Background(), "rm", []string{dir}); result.Error != "" {
		t.Fatalf("remove directory: %+v", result)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
	if result := runBuiltin(context.Background(), "rm", []string{"."}); result.Error == "" {
		t.Fatal("removed current directory")
	}
}

func TestBuiltinRequestValidation(t *testing.T) {
	if err := validateExecRequest(ExecRequest{Builtin: "pwd"}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []ExecRequest{
		{Builtin: "pwd", Args: []string{"unexpected"}},
		{Builtin: "unknown"},
		{Builtin: "ls", Argv: []string{"echo"}},
		{Argv: []string{"echo"}, Args: []string{"unexpected"}},
	} {
		if err := validateExecRequest(request); err == nil {
			t.Fatalf("accepted invalid request: %+v", request)
		}
	}
}
