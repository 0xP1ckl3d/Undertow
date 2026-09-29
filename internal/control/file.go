package control

import (
	"bufio"
	"context"
	"errors"
	"io"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

// ServeFileRelay forwards a VPN client's transfer to its selected agent.
// The server never opens the transferred file on its own filesystem.
func (m *Manager) ServeFileRelay(ctx context.Context, client *mux.Stream) {
	defer client.Close()
	if err := client.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReaderSize(client, 4096)
	request, err := pivot.ReadFileMessage(reader)
	if err != nil || request.AgentID == "" {
		if err == nil {
			err = errors.New("agent ID is required")
		}
		relayFileError(client, reader, err)
		return
	}
	agent := m.Get(request.AgentID)
	if agent == nil {
		relayFileError(client, reader, errors.New("agent is not connected"))
		return
	}
	upstream, err := agent.Open(ctx, pivot.FileDestination)
	if err != nil {
		relayFileError(client, reader, err)
		return
	}
	defer upstream.Close()
	if err := pivot.WriteFileMessage(upstream, request); err != nil {
		relayFileError(client, reader, err)
		return
	}
	upDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, reader)
		_ = upstream.CloseWrite()
		close(upDone)
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.CloseWrite()
	_ = client.Close()
	<-upDone
}

func relayFileError(client *mux.Stream, reader *bufio.Reader, err error) {
	_ = pivot.WriteFileMessage(client, pivot.FileMessage{Error: err.Error()})
	_ = client.CloseWrite()
	_, _ = io.Copy(io.Discard, reader)
}
