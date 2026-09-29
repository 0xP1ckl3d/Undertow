package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/rand"
	"testing"
	"time"

	"undertow/internal/security"
)

func pair(t *testing.T) (*Session, *Session) {
	t.Helper()
	var keys security.Keys
	for i := range keys.ClientToServer {
		keys.ClientToServer[i] = byte(i + 1)
		keys.ServerToClient[i] = byte(i + 50)
	}
	copy(keys.ClientPrefix[:], []byte{1, 2, 3, 4})
	copy(keys.ServerPrefix[:], []byte{5, 6, 7, 8})
	a, err := New(88, keys, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(88, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

// This deterministic carrier drops and reorders encrypted packets without waiting
// for wall-clock timers, so retransmission behavior is repeatable in CI.
func TestDeterministicLossyCarrier(t *testing.T) {
	a, b := pair(t)
	defer a.Close()
	defer b.Close()
	msg := bytes.Repeat([]byte("loss-and-reorder"), 1200)
	if err := a.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	type flight struct {
		to   *Session
		wire []byte
		tick int
	}
	var flights []flight
	rng := rand.New(rand.NewSource(42))
	start := time.Now()
	delivered := false
	forcedDrop := false
	for tick := 0; tick < 1200; tick++ {
		now := start.Add(time.Duration(tick) * 20 * time.Millisecond)
		for _, side := range []struct{ from, to *Session }{{a, b}, {b, a}} {
			wire, err := side.from.NextPacket(now)
			if err != nil {
				t.Fatal(err)
			}
			if side.from == a && binary.BigEndian.Uint64(wire[20:28]) == 1 && !forcedDrop {
				forcedDrop = true
				continue
			}
			if rng.Intn(10) < 2 {
				continue
			}
			flights = append(flights, flight{to: side.to, wire: wire, tick: tick + rng.Intn(4)})
		}
		kept := flights[:0]
		for _, f := range flights {
			if f.tick > tick {
				kept = append(kept, f)
				continue
			}
			if err := f.to.Process(f.wire, now); err != nil {
				t.Fatal(err)
			}
		}
		flights = kept
		if got, ok := b.TryRecv(); ok {
			if !bytes.Equal(got, msg) {
				t.Fatal("corrupted delivery")
			}
			delivered = true
		}
		if delivered && a.Stats().InFlight == 0 {
			if a.Stats().Retransmits == 0 {
				t.Fatal("loss did not exercise retransmission")
			}
			return
		}
	}
	t.Fatal("message did not converge under simulated loss")
}

func TestLossReorderFragmentAndDuplicate(t *testing.T) {
	a, b := pair(t)
	defer a.Close()
	defer b.Close()
	msg := bytes.Repeat([]byte("transport-data-"), 500)
	if err := a.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var packets [][]byte
	for a.Stats().Queued > 0 {
		p, err := a.NextPacket(now)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, p)
	}
	for i := len(packets) - 1; i >= 0; i-- {
		if i == 2 {
			continue
		}
		if err := b.Process(packets[i], now); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := b.TryRecv(); ok {
		t.Fatal("completed despite missing fragment")
	}
	if err := b.Process(packets[2], now); err != nil {
		t.Fatal(err)
	}
	got, ok := b.TryRecv()
	if !ok || !bytes.Equal(got, msg) {
		t.Fatal("reassembled data mismatch")
	}
	if err := b.Process(packets[2], now); err != nil {
		t.Fatal(err)
	}
	if b.Stats().Duplicates == 0 {
		t.Fatal("duplicate not counted")
	}
	ack, err := b.NextPacket(now)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint64(ack[20:28]) != 0 {
		t.Fatal("ACK-only packet consumed reliable sequence")
	}
	if err := a.Process(ack, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if a.Stats().InFlight != 0 {
		t.Fatal("ACK did not clear in-flight packets")
	}
	if err := a.Send(context.Background(), []byte("lost")); err != nil {
		t.Fatal(err)
	}
	first, err := a.NextPacket(now)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := a.NextPacket(now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retry) || a.Stats().Retransmits == 0 {
		t.Fatal("missing exact-byte retransmission")
	}
}
