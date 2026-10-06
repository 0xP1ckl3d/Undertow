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
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-client.Done():
			cancel()
		case <-requestCtx.Done():
		}
	}()
	releaseTurn, err := m.foregroundTurn(requestCtx, request.AgentID)
	if err != nil {
		relayFileError(client, reader, err)
		return
	}
	defer releaseTurn()
	upstream, err := m.openAgentForOperator(requestCtx, client.Done(), request.AgentID, pivot.FileDestination)
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
	<-upDone
}

func relayFileError(client *mux.Stream, reader *bufio.Reader, err error) {
	_ = pivot.WriteFileMessage(client, pivot.FileMessage{Error: err.Error()})
	_ = client.CloseWrite()
	_, _ = io.Copy(io.Discard, reader)
}
