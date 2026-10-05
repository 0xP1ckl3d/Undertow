package transport

import (
	"context"
	"net"
	"time"

	"undertow/internal/session"
)

// SessionTransport exchanges authenticated, reliable application messages.
// Send copies the message into the session queue; delivery errors surface via Recv.
type SessionTransport interface {
	Send(context.Context, []byte) error
	Recv(context.Context) ([]byte, error)
	Close() error
}

// Connection adds the authenticated session ID needed by client routing and
// operator state. The mux itself only needs SessionTransport.
type Connection interface {
	SessionTransport
	ID() uint64
}

// PeerInfo is the common authenticated peer view exposed to routing and
// operator state. Carrier implementations populate transport statistics from
// their session without exposing carrier-specific connection machinery.
type PeerInfo struct {
	ID                         uint64
	AgentID, Remote, VirtualIP string
	Carrier                    string
	Via                        string
	RelayBind                  string
	EnrollmentArtifactID       string
	Connected, LastSeen        time.Time
	Authenticated              bool
	Transport                  session.Stats
}

// Peer is the boundary between a carrier listener and the server's routing,
// control, and pivot layers. A carrier owns handshake and wire I/O; the upper
// layers use only the authenticated message channel and stable peer identity.
type Peer interface {
	Snapshot() PeerInfo
	SetVirtualIP(string)
	Channel() SessionTransport
}

// Listener accepts authenticated peers. Serve owns wire I/O and exits on
// cancellation; Close releases its local endpoint. A carrier may retain its
// own legacy accept API while implementing this common interface.
type Listener interface {
	Serve(context.Context) error
	Accept(context.Context) (Peer, error)
	Peers() []PeerInfo
	Addr() net.Addr
	Close() error
}
