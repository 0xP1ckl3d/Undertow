package pivot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Socket handles live only for one WASM instance and are closed when it exits.
type wasmSocketState struct {
	connections map[uint32]net.Conn
	next        uint32
}

func (s *wasmSocketState) closeAll() {
	for id, conn := range s.connections {
		_ = conn.Close()
		delete(s.connections, id)
	}
}

func (s *wasmSocketState) operation(ctx context.Context, op string, req wasmHostRequest) (any, error) {
	switch op {
	case "net.open":
		if len(s.connections) >= 8 {
			return nil, errors.New("socket handle limit reached (8)")
		}
		if req.Network != "tcp" && req.Network != "udp" {
			return nil, errors.New("network must be tcp or udp")
		}
		if req.Address == "" || len(req.Address) > 512 {
			return nil, errors.New("invalid address")
		}
		if _, _, err := net.SplitHostPort(req.Address); err != nil {
			return nil, err
		}
		timeout := wasmSocketTimeout(req.TimeoutMS)
		dialCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(dialCtx, req.Network, req.Address)
		if err != nil {
			return nil, err
		}
		s.next++
		if s.next == 0 {
			s.next++
		}
		for s.connections[s.next] != nil {
			s.next++
		}
		s.connections[s.next] = conn
		return map[string]any{"handle": s.next}, nil
	case "net.write":
		conn, err := s.socket(req.Handle)
		if err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil || len(data) > 8192 {
			return nil, errors.New("data must be base64 and at most 8192 bytes")
		}
		_ = conn.SetWriteDeadline(time.Now().Add(wasmSocketTimeout(req.TimeoutMS)))
		n, err := conn.Write(data)
		if err != nil {
			return nil, err
		}
		return map[string]any{"bytes": n}, nil
	case "net.read":
		conn, err := s.socket(req.Handle)
		if err != nil {
			return nil, err
		}
		limit := req.Limit
		if limit <= 0 || limit > 8192 {
			limit = 8192
		}
		_ = conn.SetReadDeadline(time.Now().Add(wasmSocketTimeout(req.TimeoutMS)))
		buf := make([]byte, limit)
		n, err := conn.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return map[string]any{"data_base64": base64.StdEncoding.EncodeToString(buf[:n]), "bytes": n, "eof": errors.Is(err, io.EOF)}, nil
	case "net.close":
		conn, err := s.socket(req.Handle)
		if err != nil {
			return nil, err
		}
		_ = conn.Close()
		delete(s.connections, req.Handle)
		return map[string]any{"closed": true}, nil
	default:
		return wasmHostOperation(ctx, op, req)
	}
}

func (s *wasmSocketState) socket(handle uint32) (net.Conn, error) {
	if handle == 0 || s.connections[handle] == nil {
		return nil, fmt.Errorf("unknown socket handle %d", handle)
	}
	return s.connections[handle], nil
}

func wasmSocketTimeout(milliseconds int) time.Duration {
	if milliseconds <= 0 || milliseconds > 5000 {
		return 5 * time.Second
	}
	return time.Duration(milliseconds) * time.Millisecond
}
