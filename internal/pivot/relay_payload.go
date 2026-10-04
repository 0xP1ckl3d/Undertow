package pivot

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"undertow/internal/mux"
	"undertow/internal/transport/tlscert"
)

type relayPayloadRegistry struct {
	mu          sync.RWMutex
	tokens      map[string]struct{}
	certificate *tls.Certificate
	active      chan struct{}
}

func newRelayPayloadRegistry() *relayPayloadRegistry {
	return &relayPayloadRegistry{tokens: make(map[string]struct{}), active: make(chan struct{}, 8)}
}

func (r *relayPayloadRegistry) serveCommands(control *mux.Stream) {
	decoder := json.NewDecoder(control)
	encoder := json.NewEncoder(control)
	for {
		var command RelayPayloadCommand
		if err := decoder.Decode(&command); err != nil {
			return
		}
		result := r.apply(command)
		if err := encoder.Encode(result); err != nil {
			return
		}
	}
}

func (r *relayPayloadRegistry) apply(command RelayPayloadCommand) RelayPayloadResult {
	if len(command.Token) != 48 {
		return RelayPayloadResult{Error: "invalid payload token"}
	}
	if decoded, err := hex.DecodeString(command.Token); err != nil || len(decoded) != 24 {
		return RelayPayloadResult{Error: "invalid payload token"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch command.Operation {
	case "add":
		if r.certificate == nil {
			certificate, err := tlscert.Load("", "", true)
			if err != nil {
				return RelayPayloadResult{Error: err.Error()}
			}
			r.certificate = &certificate
		}
		r.tokens[command.Token] = struct{}{}
	case "remove":
		delete(r.tokens, command.Token)
	default:
		return RelayPayloadResult{Error: "unknown payload listener operation"}
	}
	if r.certificate == nil {
		return RelayPayloadResult{}
	}
	certHash := sha256.Sum256(r.certificate.Leaf.Raw)
	keyHash := sha256.Sum256(r.certificate.Leaf.RawSubjectPublicKeyInfo)
	return RelayPayloadResult{TLSCertSHA256: hex.EncodeToString(certHash[:]), TLSPublicKeyPin: base64.StdEncoding.EncodeToString(keyHash[:])}
}

type bufferedRelayConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedRelayConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

// serveDownload distinguishes a TLS download from the framed Undertow relay
// handshake without consuming the latter's first bytes. The same listener and
// port continue to carry child sessions.
func (r *relayPayloadRegistry) serveDownload(ctx context.Context, session *mux.Mux, conn net.Conn) (net.Conn, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReaderSize(conn, 4096)
	prefix, err := reader.Peek(6)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return conn, true
	}
	buffered := &bufferedRelayConn{Conn: conn, reader: reader}
	if prefix[0] != 0x16 || prefix[1] != 0x03 || prefix[2] > 0x04 || prefix[3] == 0 && prefix[4] == 0 || prefix[5] != 0x01 {
		return buffered, false
	}
	select {
	case r.active <- struct{}{}:
		defer func() { <-r.active }()
	default:
		_ = conn.Close()
		return conn, true
	}
	r.mu.RLock()
	certificate := r.certificate
	r.mu.RUnlock()
	if certificate == nil {
		_ = conn.Close()
		return conn, true
	}
	tlsConn := tls.Server(buffered, &tls.Config{Certificates: []tls.Certificate{*certificate}, MinVersion: tls.VersionTLS12})
	defer tlsConn.Close()
	_ = tlsConn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		return conn, true
	}
	request, err := http.ReadRequest(bufio.NewReaderSize(io.LimitReader(tlsConn, 8192), 4096))
	if err != nil {
		return conn, true
	}
	defer request.Body.Close()
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		relayHTTPError(tlsConn, http.StatusMethodNotAllowed)
		return conn, true
	}
	if request.URL.RawQuery != "" || strings.Count(request.URL.Path, "/") != 1 {
		relayHTTPError(tlsConn, http.StatusNotFound)
		return conn, true
	}
	token := strings.TrimPrefix(request.URL.Path, "/")
	r.mu.RLock()
	_, allowed := r.tokens[token]
	r.mu.RUnlock()
	if !allowed {
		relayHTTPError(tlsConn, http.StatusNotFound)
		return conn, true
	}
	_ = tlsConn.SetDeadline(time.Now().Add(10 * time.Minute))
	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	stream, err := session.Open(openCtx, RelayPayloadDestination)
	cancel()
	if err != nil {
		relayHTTPError(tlsConn, http.StatusBadGateway)
		return conn, true
	}
	defer stream.Close()
	if err := json.NewEncoder(stream).Encode(RelayPayloadRequest{Token: token, Head: request.Method == http.MethodHead}); err != nil {
		relayHTTPError(tlsConn, http.StatusBadGateway)
		return conn, true
	}
	_ = stream.CloseWrite()
	upstream := bufio.NewReaderSize(stream, 4096)
	headerLine, err := upstream.ReadBytes('\n')
	if err != nil || len(headerLine) > 1024 {
		relayHTTPError(tlsConn, http.StatusBadGateway)
		return conn, true
	}
	var header RelayPayloadHeader
	if err := json.Unmarshal(headerLine, &header); err != nil || header.Error != "" || header.Size < 0 || header.Size > 512<<20 || len(header.SHA256) != 64 {
		relayHTTPError(tlsConn, http.StatusNotFound)
		return conn, true
	}
	if _, err := fmt.Fprintf(tlsConn, "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nX-Artifact-SHA256: %s\r\nContent-Disposition: attachment; filename=\"payload\"\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\nConnection: close\r\n\r\n", header.Size, header.SHA256); err != nil {
		return conn, true
	}
	if request.Method == http.MethodGet {
		_, _ = io.CopyN(tlsConn, upstream, header.Size)
	}
	return conn, true
}

func relayHTTPError(conn io.Writer, status int) {
	body := http.StatusText(status)
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, body, len(body), body)
}
