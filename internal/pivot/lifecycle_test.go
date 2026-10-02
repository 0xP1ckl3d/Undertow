package pivot

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestConfiguredAgentShutdownHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	stopped := make(chan struct{})
	go ServeAgentWithLifecycle(ctx, agent, DefaultCapabilities(), func() { close(stopped) })
	stream, err := server.Open(ctx, ShutdownDestination)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var ack [1]byte
	if _, err := io.ReadFull(stream, ack[:]); err != nil || ack[0] != 1 {
		t.Fatalf("ack=%v err=%v", ack, err)
	}
	select {
	case <-stopped:
		t.Fatal("stopped before confirmation")
	default:
	}
	if _, err := stream.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(stream, ack[:]); err != nil || ack[0] != 2 {
		t.Fatalf("final ack=%v err=%v", ack, err)
	}
	if _, err := stream.Read(ack[:]); err != io.EOF {
		t.Fatalf("agent did not finish shutdown stream: %v", err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("shutdown callback was not called")
	}
}
