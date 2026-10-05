package bof

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

func fileEvent(t *testing.T, collector *FileCollector, kind uint32, body []byte) []FileArtifact {
	t.Helper()
	var complete []FileArtifact
	first := true
	for {
		n := len(body)
		if n > CallbackChunkSize {
			n = CallbackChunkSize
		}
		flags := byte(0)
		if first {
			flags |= 1
		}
		if n == len(body) {
			flags |= 2
		}
		files, err := collector.Consume(EncodeCallbackChunk(kind, flags, body[:n]))
		if err != nil {
			t.Fatal(err)
		}
		complete = append(complete, files...)
		body = body[n:]
		first = false
		if len(body) == 0 {
			return complete
		}
	}
}

func TestFileCallbacksChunkedByID(t *testing.T) {
	collector := NewFileCollector(t.TempDir())
	defer collector.Close()
	data := make([]byte, 100000)
	for i := range data {
		data[i] = byte(i)
	}
	for _, id := range []uint32{7, 9} {
		open := make([]byte, 8)
		binary.BigEndian.PutUint32(open[:4], id)
		binary.BigEndian.PutUint32(open[4:8], uint32(len(data)))
		open = append(open, []byte("../proof.bin")...)
		fileEvent(t, collector, CallbackFile, open)
	}
	for _, id := range []uint32{9, 7} {
		write := make([]byte, 4)
		binary.BigEndian.PutUint32(write, id)
		write = append(write, data...)
		fileEvent(t, collector, CallbackFileWrite, write)
	}
	for _, id := range []uint32{7, 9} {
		close := make([]byte, 4)
		binary.BigEndian.PutUint32(close, id)
		files := fileEvent(t, collector, CallbackFileClose, close)
		if len(files) != 1 || files[0].Name != "proof.bin" || files[0].Size != uint64(len(data)) {
			t.Fatalf("files=%+v", files)
		}
		got, err := os.ReadFile(files[0].Path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("file %d mismatch: %v", id, err)
		}
	}
}

func TestFileCallbackRejectsIncompleteAndCleansUp(t *testing.T) {
	root := t.TempDir()
	collector := NewFileCollector(root)
	var reserved uint64
	collector.OnReserve = func(size uint64) error { reserved += size; return nil }
	collector.OnRelease = func(size uint64) { reserved -= size }
	open := []byte{0, 0, 0, 1, 0, 0, 0, 5, 'a'}
	fileEvent(t, collector, CallbackFile, open)
	fileEvent(t, collector, CallbackFileWrite, []byte{0, 0, 0, 1, 1, 2})
	_, err := collector.Consume(EncodeCallbackChunk(CallbackFileClose, 3, []byte{0, 0, 0, 1}))
	if err == nil {
		t.Fatal("accepted truncated file")
	}
	collector.Close()
	if reserved != 0 { t.Fatalf("partial file leaked %d reserved bytes",reserved) }
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("incomplete file persisted: %v %v", entries, err)
	}
}
