//go:build windows && amd64

package bof

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
)

func TestExecuteCorpus(t *testing.T) {
	for _, tc := range []struct {
		name, format string
		values       []string
		want         string
	}{
		{"hello", "", nil, "hello BOF pid="},
		{"arguments", "iszZb", []string{"123", "-7", "hello", "雪", "base64:AP8="}, "int=123 short=-7 ansi=hello wide0=96ea binary=2 remain=0"},
		{"imports", "", nil, "imports pid="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := fixture(t, tc.name)
			args, err := EncodeArguments(tc.format, tc.values, nil)
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			code, err := Execute(context.Background(), object, args, func(_ bool, b []byte) error { _, e := output.Write(b); return e })
			if err != nil || code != 0 || !strings.Contains(output.String(), tc.want) {
				t.Fatalf("code=%d error=%v output=%q", code, err, output.String())
			}
		})
	}
}

func TestMissingWindowsImport(t *testing.T) {
	object := fixture(t, "hello")
	old := []byte("GetCurrentProcessId")
	position := bytes.Index(object, old)
	if position < 0 {
		t.Fatal("fixture has no Windows import")
	}
	copy(object[position:], []byte("BadCurrentProcessId"))
	args, _ := EncodeArguments("", nil, nil)
	_, err := Execute(context.Background(), object, args, func(bool, []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "unresolved Windows import") {
		t.Fatalf("missing Windows import: %v", err)
	}
}
func TestRepeatedConcurrentExecution(t *testing.T) {
	object := fixture(t, "hello")
	args, _ := EncodeArguments("", nil, nil)
	var group sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := Execute(context.Background(), object, args, func(bool, []byte) error { return nil })
			if err != nil {
				errors <- err
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
