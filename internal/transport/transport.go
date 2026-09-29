package transport

import "context"

// SessionTransport exchanges authenticated, reliable application messages.
// Send copies the message into the session queue; delivery errors surface via Recv.
type SessionTransport interface {
	Send(context.Context, []byte) error
	Recv(context.Context) ([]byte, error)
	Close() error
}
