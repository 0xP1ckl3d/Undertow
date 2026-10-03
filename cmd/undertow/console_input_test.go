package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestConsoleEditorConsumesModifiedKeys(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"delete", "abc def\x1b[1;5D\x1b[3~\n", "abc ef"},
		{"ctrl right", "abc def\x1b[1;5D\x1b[1;5C!\n", "abc def!"},
		{"unknown key", "abc\x1b[2~\n", "abc"},
		{"home and end", "abc\x1b[H!\x1b[F?\n", "!abc?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editor := newConsoleEditor(io.Discard, false)
			lines := make(chan string, 1)
			if err := editor.read(context.Background(), strings.NewReader(tc.input), lines); err != io.EOF {
				t.Fatalf("read = %v", err)
			}
			if got := <-lines; got != tc.want {
				t.Fatalf("submitted line = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConsoleEditorWrapsWithoutReprintingOnEachKey(t *testing.T) {
	var output bytes.Buffer
	editor := newConsoleEditor(&output, false)
	editor.columns = 12
	editor.showPrompt("> ", false)
	if err := editor.read(context.Background(), strings.NewReader("abcdefghijklmnopqrstuvwxyz"), make(chan string, 1)); err != io.EOF {
		t.Fatalf("read = %v", err)
	}
	if got := output.String(); !strings.Contains(got, "abcdefghi\r\njklmnopqrst\r\nuvwxyz") || strings.Contains(got, "\x1b[J") {
		t.Fatalf("typing at the end repainted the wrapped prompt: %q", got)
	}
}

func TestConsoleEditorRedrawClearsAllWrappedRows(t *testing.T) {
	var output bytes.Buffer
	editor := newConsoleEditor(&output, false)
	editor.columns = 12
	editor.showPrompt("> ", false)
	if err := editor.read(context.Background(), strings.NewReader("abcdefghijklmnopqrstuvwxyz\x1b[D!"), make(chan string, 1)); err != io.EOF {
		t.Fatalf("read = %v", err)
	}
	want := "\x1b[2A\r\x1b[J> abcdefghi\r\njklmnopqrst\r\nuvwxy!z"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("wrapped redraw did not clear and replace all rows: %q", output.String())
	}
}
