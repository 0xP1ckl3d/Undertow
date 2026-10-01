package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"

	"undertow/internal/transport/stream"
)

const maxMessage = 65535

type messageConn struct {
	io.ReadWriteCloser
	remote string
	write  sync.Mutex
}

func (c *messageConn) RemoteAddr() string { return c.remote }
func (c *messageConn) ReadMessage() ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(c.ReadWriteCloser, header[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(header[:]))
	if n == 0 || n > maxMessage {
		return nil, errors.New("invalid relay message size")
	}
	message := make([]byte, n)
	_, err := io.ReadFull(c.ReadWriteCloser, message)
	return message, err
}
func (c *messageConn) WriteMessage(message []byte) error {
	if len(message) == 0 || len(message) > maxMessage {
		return errors.New("invalid relay message size")
	}
	c.write.Lock()
	defer c.write.Unlock()
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(message)))
	if err := writeAll(c.ReadWriteCloser, header[:]); err != nil {
		return err
	}
	return writeAll(c.ReadWriteCloser, message)
}

func writeAll(writer io.Writer, message []byte) error {
	for len(message) > 0 {
		n, err := writer.Write(message)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		message = message[n:]
	}
	return nil
}

func Dial(ctx context.Context, address, fingerprint string, token []byte, key ed25519.PrivateKey) (*stream.Connection, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	framed := &messageConn{ReadWriteCloser: conn, remote: conn.RemoteAddr().String()}
	connection, err := stream.Dial(ctx, framed, fingerprint, token, key)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return connection, nil
}

func Accept(ctx context.Context, conn io.ReadWriteCloser, parentID string, identity ed25519.PrivateKey, token []byte) (*stream.Peer, error) {
	framed := &messageConn{ReadWriteCloser: conn, remote: "relay via " + parentID}
	peer, err := stream.Accept(ctx, framed, identity, token)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	peer.Carrier = "relay"
	peer.Via = parentID
	return peer, nil
}
