package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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

func TestAdaptiveFragmentBackoff(t *testing.T) {
	a, _ := pair(t)
	a.adaptivePayload = true
	defer a.Close()
	if err := a.Send(context.Background(), bytes.Repeat([]byte{7}, 700)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := a.NextPacket(now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NextPacket(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stats := a.Stats()
	if stats.FragmentSize != 800 || stats.PayloadAdjustments != 0 || stats.Retransmits != 1 {
		t.Fatalf("isolated loss changed payload size: %+v", stats)
	}
	if _, err := a.NextPacket(now.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NextPacket(now.Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	stats = a.Stats()
	if stats.FragmentSize != 736 || stats.PayloadAdjustments != 1 || stats.Retransmits != 3 {
		t.Fatalf("unexpected adaptive stats: %+v", stats)
	}
}

func TestRetransmittedPacketRestoresLostACK(t *testing.T) {
	sender, receiver := pair(t)
	defer sender.Close()
	defer receiver.Close()
	if err := sender.Send(context.Background(), []byte("lost acknowledgement")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	wire, err := sender.NextPacket(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.Process(wire, now); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.NextPacket(now); err != nil { // ACK lost on the path
		t.Fatal(err)
	}
	retryTime := now.Add(time.Second)
	retransmit, err := sender.NextPacket(retryTime)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retransmit, wire) {
		t.Fatal("expected identical encrypted retransmission")
	}
	if err := receiver.Process(retransmit, retryTime); err != nil {
		t.Fatal(err)
	}
	if !receiver.HasWork(retryTime) {
		t.Fatal("duplicate packet did not trigger a fresh ACK")
	}
	ack, err := receiver.NextPacket(retryTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Process(ack, retryTime); err != nil {
		t.Fatal(err)
	}
	if sender.Stats().InFlight != 0 {
		t.Fatal("fresh ACK did not release retransmitted packet")
	}
}

func TestCompletedMessageBurstKeepsPacketWindowBounded(t *testing.T) {
	sender, receiver := pair(t)
	defer sender.Close()
	defer receiver.Close()
	now := time.Now()
	for i := 0; i < 100; i++ {
		message := []byte{byte(i + 1)}
		if err := sender.Send(context.Background(), message); err != nil {
			t.Fatal(err)
		}
		wire, err := sender.NextPacket(now)
		if err != nil {
			t.Fatal(err)
		}
		if err := receiver.Process(wire, now); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		ack, err := receiver.NextPacket(now)
		if err != nil {
			t.Fatal(err)
		}
		if err := sender.Process(ack, now); err != nil {
			t.Fatal(err)
		}
		if receiver.Stats().ReceiveWindow != maxPending {
			t.Fatal("packet receive window changed during message burst")
		}
	}
	for i := 0; i < 100; i++ {
		message, err := receiver.Recv(context.Background())
		if err != nil || !bytes.Equal(message, []byte{byte(i + 1)}) {
			t.Fatalf("message %d: %x, %v", i, message, err)
		}
	}
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

func TestRepeatedSACKDoesNotRetransmitSameGapRepeatedly(t *testing.T) {
	sender, receiver := pair(t)
	defer sender.Close()
	defer receiver.Close()
	now := time.Now()
	var packets [][]byte
	for i := 0; i < 5; i++ {
		if err := sender.Send(context.Background(), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		wire, err := sender.NextPacket(now)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, wire)
	}
	for _, wire := range packets[1:] {
		if err := receiver.Process(wire, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		tick := time.Duration(i) * 30 * time.Millisecond
		ack, err := receiver.NextPacket(now.Add(tick))
		if err != nil {
			t.Fatal(err)
		}
		if err := sender.Process(ack, now.Add(tick+time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		out, err := sender.NextPacket(now.Add(tick + 30*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && !bytes.Equal(out, packets[0]) {
			t.Fatal("missing fast retransmission")
		}
		if i == 1 && binary.BigEndian.Uint64(out[20:28]) != 0 {
			t.Fatal("unchanged selective ACK caused a second fast retransmission")
		}
	}
	if sender.Stats().Retransmits != 1 {
		t.Fatalf("retransmits=%d", sender.Stats().Retransmits)
	}
}

func TestReorderedDNSRepliesDoNotTriggerFastRetransmit(t *testing.T) {
	sender, receiver := pair(t)
	defer sender.Close()
	defer receiver.Close()
	now := time.Now()
	var packets [][]byte
	for i := 0; i < 5; i++ {
		if err := sender.Send(context.Background(), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		wire, err := sender.NextPacket(now)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, wire)
	}
	for _, wire := range packets[1:] {
		if err := receiver.Process(wire, now); err != nil {
			t.Fatal(err)
		}
	}
	ack, err := receiver.NextPacket(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Process(ack, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	before, err := sender.NextPacket(now.Add(2 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if seq := binary.BigEndian.Uint64(before[20:28]); seq != 0 {
		t.Fatalf("premature retransmission of sequence %d", seq)
	}
	if err := receiver.Process(packets[0], now.Add(3*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	ack, err = receiver.NextPacket(now.Add(4 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Process(ack, now.Add(5*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if sender.Stats().Retransmits != 0 {
		t.Fatal("reordered packet caused a retransmission")
	}
}

func TestSelectiveACKDoesNotSendPastReceiveWindow(t *testing.T) {
	sender, receiver := pair(t)
	defer sender.Close()
	defer receiver.Close()
	sender.cwnd = maxPending
	for i := 0; i < maxPending+1; i++ {
		if err := sender.Send(context.Background(), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	var missing []byte
	for i := 1; i <= maxPending; i++ {
		wire, err := sender.NextPacket(now)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			missing = wire
			continue
		}
		if err := receiver.Process(wire, now); err != nil {
			t.Fatal(err)
		}
	}
	ack, err := receiver.NextPacket(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Process(ack, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	retry, err := sender.NextPacket(now.Add(30 * time.Millisecond))
	if err != nil || !bytes.Equal(retry, missing) {
		t.Fatalf("missing packet was not retransmitted: %v", err)
	}
	if sender.HasWork(now.Add(31 * time.Millisecond)) {
		t.Fatal("sender treated a SACK hole as free receive-window space")
	}
	blocked, err := sender.NextPacket(now.Add(31 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if seq := binary.BigEndian.Uint64(blocked[20:28]); seq != 0 {
		t.Fatalf("sent sequence %d beyond receiver window", seq)
	}
	if err := receiver.Process(retry, now.Add(32*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	ack, err = receiver.NextPacket(now.Add(33 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Process(ack, now.Add(34*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	next, err := sender.NextPacket(now.Add(35 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if seq := binary.BigEndian.Uint64(next[20:28]); seq != maxPending+1 {
		t.Fatalf("next sequence after gap closed = %d", seq)
	}
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

func TestRetransmissionDeliversAfterReceiveQueueDrains(t *testing.T) {
	sender, receiver := pair(t)
	defer sender.Close()
	defer receiver.Close()
	for i := 0; i < cap(receiver.deliver); i++ {
		receiver.deliver <- []byte("previous")
	}
	message := []byte("deliver after queue drains")
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, err := sender.NextPacket(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.Process(first, now); !errors.Is(err, ErrReceiveQueueFull) {
		t.Fatalf("full receive queue: %v", err)
	}
	<-receiver.deliver
	retry, err := sender.NextPacket(now.Add(time.Second))
	if err != nil || !bytes.Equal(first, retry) {
		t.Fatalf("retransmission: equal=%t err=%v", bytes.Equal(first, retry), err)
	}
	if err := receiver.Process(retry, now.Add(time.Second)); err != nil {
		t.Fatalf("retransmitted data was not delivered: %v", err)
	}
	for i := 1; i < cap(receiver.deliver); i++ {
		<-receiver.deliver
	}
	got, ok := receiver.TryRecv()
	if !ok || !bytes.Equal(got, message) {
		t.Fatalf("message lost after queue drained: %q", got)
	}
}

func TestPriorityMessageBypassesFullDataQueue(t *testing.T) {
	sender, receiver := pair(t)
	defer sender.Close()
	defer receiver.Close()
	for i := 0; i < maxQueued; i++ {
		if err := sender.Send(context.Background(), []byte("bulk")); err != nil {
			t.Fatal(err)
		}
	}
	if err := sender.Send(context.Background(), []byte("more bulk")); err != ErrQueueFull {
		t.Fatalf("expected a full bulk queue, got %v", err)
	}
	if err := sender.SendPriority(context.Background(), []byte("open-control")); err != nil {
		t.Fatalf("control blocked by bulk queue: %v", err)
	}
	now := time.Now()
	wire, err := sender.NextPacket(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.Process(wire, now); err != nil {
		t.Fatal(err)
	}
	got, ok := receiver.TryRecv()
	if !ok || string(got) != "open-control" {
		t.Fatalf("first delivered message = %q, want control", got)
	}
}
