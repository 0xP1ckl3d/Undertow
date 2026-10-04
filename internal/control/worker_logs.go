package control

import (
	"bytes"
	"sync"
	"time"
)

type WorkerLogEntry struct {
	Seq  uint64    `json:"seq"`
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

type WorkerLogSnapshot struct {
	Entries []WorkerLogEntry `json:"entries"`
	Next    uint64           `json:"next"`
	Gap     bool             `json:"gap"`
}

// WorkerLogBuffer retains bounded, process-local worker diagnostics. It never
// owns operational state or file contents. The server's ordinary logger still
// writes to stderr and its configured background log file.
type WorkerLogBuffer struct {
	mu      sync.Mutex
	entries []WorkerLogEntry
	next    uint64
	onWrite func()
}

func NewWorkerLogBuffer() *WorkerLogBuffer   { return &WorkerLogBuffer{} }
func (b *WorkerLogBuffer) OnWrite(fn func()) { b.mu.Lock(); b.onWrite = fn; b.mu.Unlock() }

func (b *WorkerLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		if len(line) > 4096 {
			line = line[:4096]
		}
		b.next++
		b.entries = append(b.entries, WorkerLogEntry{Seq: b.next, At: time.Now().UTC(), Text: string(line)})
	}
	if len(b.entries) > 1000 {
		b.entries = append([]WorkerLogEntry(nil), b.entries[len(b.entries)-1000:]...)
	}
	callback := b.onWrite
	b.mu.Unlock()
	if callback != nil {
		callback()
	}
	return len(p), nil
}

func (b *WorkerLogBuffer) Snapshot(after uint64) WorkerLogSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := WorkerLogSnapshot{Entries: make([]WorkerLogEntry, 0, 100), Next: after}
	if len(b.entries) > 0 && after > 0 && after+1 < b.entries[0].Seq {
		out.Gap = true
	}
	start := 0
	if after == 0 && len(b.entries) > 40 {
		start = len(b.entries) - 40
	}
	for _, entry := range b.entries[start:] {
		if entry.Seq > after {
			out.Entries = append(out.Entries, entry)
			out.Next = entry.Seq
			if len(out.Entries) == 100 {
				break
			}
		}
	}
	return out
}
