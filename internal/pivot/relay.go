package pivot

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"undertow/internal/mux"
	"undertow/internal/namedpipe"
)

const RelayListenerDestination = "relay-listener.undertow.invalid:0"
const RelayInboundDestination = "relay-inbound.undertow.invalid:0"
const RelayPipeInboundDestination = "relay-pipe-inbound.undertow.invalid:0"

func IsPipeRelayBind(bind string) bool { return namedpipe.IsLocal(bind) }

// RelayInboundForBind identifies the listener that accepted a child. The bind
// is metadata on the authenticated parent stream, not a client supplied claim.
func RelayInboundForBind(bind string) string {
	prefix := "relay-tcp-"
	if namedpipe.IsLocal(bind) {
		prefix = "relay-pipe-"
	}
	return prefix + hex.EncodeToString([]byte(bind)) + ".undertow.invalid:0"
}

func ParseRelayInbound(destination string) (carrier, bind string, ok bool) {
	if destination == RelayInboundDestination {
		return "relay", "", true // Earlier agents did not report the bind.
	}
	if destination == RelayPipeInboundDestination {
		return "relay-smb", "", true
	}
	for _, item := range []struct{ prefix, carrier string }{{"relay-tcp-", "relay"}, {"relay-pipe-", "relay-smb"}} {
		if !strings.HasPrefix(destination, item.prefix) || !strings.HasSuffix(destination, ".undertow.invalid:0") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(destination, item.prefix), ".undertow.invalid:0")
		decoded, err := hex.DecodeString(value)
		if err != nil || ValidateRelayBind(string(decoded)) != nil || namedpipe.IsLocal(string(decoded)) != (item.carrier == "relay-smb") {
			return "", "", false
		}
		return item.carrier, string(decoded), true
	}
	return "", "", false
}

type RelayListenerRequest struct {
	Bind string `json:"bind"`
}
type RelayListenerResult struct {
	Bind  string `json:"bind,omitempty"`
	Error string `json:"error,omitempty"`
}

const RelayPayloadDestination = "relay-payload.undertow.invalid:0"

type RelayPayloadCommand struct {
	Operation string `json:"operation"`
	Token     string `json:"token"`
}

type RelayPayloadResult struct {
	Error           string `json:"error,omitempty"`
	TLSCertSHA256   string `json:"tls_cert_sha256,omitempty"`
	TLSPublicKeyPin string `json:"tls_public_key_pin,omitempty"`
}

type RelayPayloadRequest struct {
	Token string `json:"token"`
	Head  bool   `json:"head,omitempty"`
}
type RelayPayloadHeader struct {
	Error  string `json:"error,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func ValidateRelayBind(bind string) error {
	if namedpipe.IsLocal(bind) {
		return namedpipe.ValidateLocal(bind)
	}
	host, portText, err := net.SplitHostPort(bind)
	if err != nil {
		return errors.New("relay bind must be numeric IPv4:PORT")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Is4() {
		return errors.New("relay bind must use a numeric IPv4 address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return errors.New("relay port must be between 0 and 65535")
	}
	return nil
}

// ServeAgentRelayListener opens exactly one operator-requested TCP or Windows
// named-pipe listener.
// Child bytes are forwarded to the original server over this agent's session.
func ServeAgentRelayListener(ctx context.Context, session *mux.Mux, control *mux.Stream) {
	defer control.Close()
	if err := control.AcceptOpen(ctx); err != nil {
		return
	}
	var request RelayListenerRequest
	if err := json.NewDecoder(io.LimitReader(control, 1024)).Decode(&request); err != nil || ValidateRelayBind(request.Bind) != nil {
		_ = json.NewEncoder(control).Encode(RelayListenerResult{Error: "invalid relay bind"})
		return
	}
	var listener net.Listener
	var err error
	if namedpipe.IsLocal(request.Bind) {
		listener, err = namedpipe.Listen(request.Bind)
	} else {
		listener, err = net.Listen("tcp4", request.Bind)
	}
	if err != nil {
		_ = json.NewEncoder(control).Encode(RelayListenerResult{Error: err.Error()})
		return
	}
	defer listener.Close()
	actualBind := listener.Addr().String()
	if namedpipe.IsLocal(request.Bind) {
		actualBind = request.Bind
	}
	if err := json.NewEncoder(control).Encode(RelayListenerResult{Bind: actualBind}); err != nil {
		return
	}
	registry := newRelayPayloadRegistry()
	go registry.serveCommands(control)
	go func() {
		select {
		case <-control.Done():
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
			conn, handled := registry.serveDownload(ctx, session, conn)
			if handled {
				return
			}
			openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			destination := RelayInboundForBind(actualBind)
			upstream, err := session.Open(openCtx, destination)
			cancel()
			if err != nil {
				_ = conn.Close()
				return
			}
			bridge(ctx, conn, upstream)
		}(conn)
	}
}
