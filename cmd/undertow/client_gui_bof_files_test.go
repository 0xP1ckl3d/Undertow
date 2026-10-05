//go:build linux || windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"undertow/internal/bof"
	"undertow/internal/pivot"
)

type guiBOFPipe struct{ net.Conn }

func (p guiBOFPipe) CloseWrite() error { return p.Close() }

func writeGUIBOFFrame(w io.Writer, kind byte, data []byte) error {
	var header [5]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func TestGUIBOFFileCallbacksStayOutOfTextOutput(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	data := bytes.Repeat([]byte{0, 1, 2, 3, 0xff}, 20000)
	finished := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(server)
		if _, err := reader.ReadBytes('\n'); err != nil {
			finished <- err
			return
		}
		if err := writeGUIBOFFrame(server, pivot.InteractiveReady, nil); err != nil {
			finished <- err
			return
		}
		open := make([]byte, 8)
		binary.BigEndian.PutUint32(open[:4], 7)
		binary.BigEndian.PutUint32(open[4:], uint32(len(data)))
		open = append(open, []byte("proof.bin")...)
		write := append([]byte{0, 0, 0, 7}, data...)
		for _, event := range []struct {
			kind uint32
			body []byte
		}{{bof.CallbackFile, open}, {bof.CallbackFileWrite, write}, {bof.CallbackFileClose, []byte{0, 0, 0, 7}}} {
			first := true
			for len(event.body) > 0 {
				n := len(event.body)
				if n > bof.CallbackChunkSize {
					n = bof.CallbackChunkSize
				}
				flags := byte(0)
				if first {
					flags |= 1
				}
				if n == len(event.body) {
					flags |= 2
				}
				if err := writeGUIBOFFrame(server, pivot.InteractiveBOFCallback, bof.EncodeCallbackChunk(event.kind, flags, event.body[:n])); err != nil {
					finished <- err
					return
				}
				event.body = event.body[n:]
				first = false
			}
		}
		if err := writeGUIBOFFrame(server, pivot.InteractiveOutput, []byte("done\n")); err != nil {
			finished <- err
			return
		}
		finished <- writeGUIBOFFrame(server, pivot.InteractiveExit, []byte{0, 0, 0, 0})
	}()
	session, err := pivot.StartInteractive(context.Background(), guiBOFPipe{client}, bufio.NewReader(client), pivot.InteractiveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	gui := &guiServer{transferDir: t.TempDir(), downloads: make(map[string]guiDownload)}
	recorder := httptest.NewRecorder()
	writer := &guiCommandEventWriter{encode: json.NewEncoder(recorder), flush: recorder}
	gui.streamGUISession(writer, session)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"kind":"file"`) || !strings.Contains(body, "proof.bin") || !strings.Contains(body, "done\\n") || strings.Contains(body, "\\u0000") {
		t.Fatalf("unexpected GUI output: %q", body)
	}
	if len(gui.downloads) != 1 {
		t.Fatalf("download count=%d", len(gui.downloads))
	}
	for _, item := range gui.downloads {
		got, err := os.ReadFile(item.path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("download mismatch: %v", err)
		}
	}
}
