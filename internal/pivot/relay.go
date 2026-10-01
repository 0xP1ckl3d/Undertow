package pivot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"undertow/internal/mux"
)

const RelayListenerDestination = "relay-listener.undertow.invalid:0"
const RelayInboundDestination = "relay-inbound.undertow.invalid:0"

type RelayListenerRequest struct {
	Bind string `json:"bind"`
}
type RelayListenerResult struct {
	Bind  string `json:"bind,omitempty"`
	Error string `json:"error,omitempty"`
}

func ValidateRelayBind(bind string) error {
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

// ServeAgentRelayListener opens exactly one operator-requested TCP listener.
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
	listener, err := net.Listen("tcp4", request.Bind)
	if err != nil {
		_ = json.NewEncoder(control).Encode(RelayListenerResult{Error: err.Error()})
		return
	}
	defer listener.Close()
	if err := json.NewEncoder(control).Encode(RelayListenerResult{Bind: listener.Addr().String()}); err != nil {
		return
	}
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
			openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			upstream, err := session.Open(openCtx, RelayInboundDestination)
			cancel()
			if err != nil {
				_ = conn.Close()
				return
			}
			bridge(ctx, conn, upstream)
		}(conn)
	}
}
