package pivot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"undertow/internal/mux"
)

const ListenerDestination = "listener.undertow.invalid:0"

type ListenerRequest struct {
	ID   string `json:"id"`
	Bind string `json:"bind"`
}

type ListenerResult struct {
	Bind  string `json:"bind,omitempty"`
	Error string `json:"error,omitempty"`
}

type ClientForwardRequest struct {
	Target string `json:"target"`
}

func AgentForwardDestination(id string) string { return "forward." + id + ".undertow.invalid:0" }
func ClientForwardDestination(id string) string {
	return "client-forward." + id + ".undertow.invalid:0"
}

func ForwardID(destination, prefix string) (string, bool) {
	start := prefix + "."
	end := ".undertow.invalid:0"
	if !strings.HasPrefix(destination, start) || !strings.HasSuffix(destination, end) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(destination, start), end)
	if len(id) != 32 {
		return "", false
	}
	for _, char := range id {
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return "", false
		}
	}
	return id, true
}

func ValidateForwardAddress(value string, loopbackOnly bool) error {
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("forward address must be numeric IPv4:PORT")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Is4() || loopbackOnly && !ip.IsLoopback() {
		return errors.New("forward address must use a numeric IPv4 address; client target must be loopback")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 || loopbackOnly && port == 0 {
		return errors.New("forward port must be between 1 and 65535; agent bind may use 0")
	}
	return nil
}

func ServeAgentListener(ctx context.Context, session *mux.Mux, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	var request ListenerRequest
	if err := json.NewDecoder(io.LimitReader(stream, 1024)).Decode(&request); err != nil {
		writeListenerError(stream, "invalid listener request")
		return
	}
	if _, valid := ForwardID(AgentForwardDestination(request.ID), "forward"); !valid || ValidateForwardAddress(request.Bind, false) != nil {
		writeListenerError(stream, "invalid listener address or ID")
		return
	}
	listener, err := net.Listen("tcp4", request.Bind)
	if err != nil {
		writeListenerError(stream, err.Error())
		return
	}
	defer listener.Close()
	if err := json.NewEncoder(stream).Encode(ListenerResult{Bind: listener.Addr().String()}); err != nil {
		return
	}
	go func() {
		select {
		case <-stream.Done():
		case <-session.Done():
		case <-ctx.Done():
		}
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			upstream, err := session.Open(openCtx, AgentForwardDestination(request.ID))
			cancel()
			if err != nil {
				_ = conn.Close()
				return
			}
			bridge(ctx, conn, upstream)
		}(conn)
	}
}

func writeListenerError(stream *mux.Stream, message string) {
	_ = json.NewEncoder(stream).Encode(ListenerResult{Error: message})
	_ = stream.CloseWrite()
	_, _ = io.Copy(io.Discard, stream)
}

func ServeClientForward(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	var request ClientForwardRequest
	if err := json.NewDecoder(io.LimitReader(stream, 1024)).Decode(&request); err != nil || ValidateForwardAddress(request.Target, true) != nil {
		_, _ = io.WriteString(stream, "invalid target\n")
		return
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp4", request.Target)
	if err != nil {
		_, _ = fmt.Fprintf(stream, "dial failed: %v\n", err)
		return
	}
	if _, err := io.WriteString(stream, "ok\n"); err != nil {
		_ = conn.Close()
		return
	}
	bridge(ctx, conn, stream)
}

func ServeClientForwards(ctx context.Context, session *mux.Mux) {
	for {
		stream, err := session.Accept(ctx)
		if err != nil {
			return
		}
		if _, valid := ForwardID(stream.Destination(), "client-forward"); !valid {
			stream.Fail(errors.New("unknown server stream"))
			continue
		}
		go ServeClientForward(ctx, stream)
	}
}

func ForwardReady(stream *mux.Stream) (io.Reader, error) {
	reader := bufio.NewReaderSize(stream, 256)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if line != "ok\n" {
		return nil, errors.New(strings.TrimSpace(line))
	}
	return reader, nil
}

func BridgeForwardStreams(ctx context.Context, agent, client *mux.Stream, clientReader io.Reader) {
	defer agent.Close()
	defer client.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(client, agent); _ = client.CloseWrite(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(agent, clientReader); _ = agent.CloseWrite(); done <- struct{}{} }()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-ctx.Done():
			return
		case <-agent.Done():
			return
		case <-client.Done():
			return
		}
	}
}
