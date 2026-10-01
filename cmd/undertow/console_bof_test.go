package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/bof"
)

func TestBOFConsoleManifestAndInspection(t *testing.T) {
	object := filepath.Join("..", "..", "examples", "bof", "arguments.o")
	values := []string{"123", "-7", "hello", "雪", "base64:AP8="}
	got, err := bofArguments(object, "", "", values)
	if err != nil {
		t.Fatal(err)
	}
	want, err := bof.EncodeArguments("iszZb", values, bof.ReadBinaryFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("manifest packet=%x want %x", got, want)
	}
	if _, err := bofArguments(object, "", "", values[:2]); err == nil {
		t.Fatal("manifest accepted wrong argument count")
	}
	var output strings.Builder
	if err := bofCommand([]string{"inspect", object}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Compatibility : Supported") || !strings.Contains(output.String(), "BeaconDataParse") {
		t.Fatalf("inspection output: %s", output.String())
	}
	bad := filepath.Join(t.TempDir(), "bad.o")
	if err := os.WriteFile(bad, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := bofCommand([]string{"inspect", bad}, &output); err == nil {
		t.Fatal("invalid object inspected as valid")
	}
}
