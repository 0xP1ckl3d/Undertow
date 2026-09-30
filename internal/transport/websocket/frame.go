package websocket

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
)

const maxMessage = 2048

type messageConn struct {
	conn   net.Conn
	reader *bufio.Reader
	client bool
	write  sync.Mutex
}

func (c *messageConn) RemoteAddr() string { return c.conn.RemoteAddr().String() }
func (c *messageConn) Close() error       { return c.conn.Close() }

func (c *messageConn) ReadMessage() ([]byte, error) {
	var assembled []byte
	fragmented := false
	for {
		var head [2]byte
		if _, err := io.ReadFull(c.reader, head[:]); err != nil {
			return nil, err
		}
		if head[0]&0x70 != 0 {
			return nil, errors.New("unsupported WebSocket extension bits")
		}
		final := head[0]&0x80 != 0
		opcode := head[0] & 0x0f
		masked := head[1]&0x80 != 0
		if masked == c.client {
			return nil, errors.New("invalid WebSocket masking direction")
		}
		length := uint64(head[1] & 0x7f)
		if length == 126 {
			var ext [2]byte
			if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(ext[:]))
		} else if length == 127 {
			var ext [8]byte
			if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(ext[:])
		}
		if length > maxMessage || uint64(len(assembled))+length > maxMessage {
			return nil, errors.New("WebSocket message too large")
		}
		if opcode >= 8 && (!final || length > 125) {
			return nil, errors.New("invalid WebSocket control frame")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.reader, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 8:
			return nil, io.EOF
		case 9:
			if err := c.writeFrame(10, payload); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 2:
			if fragmented {
				return nil, errors.New("unexpected WebSocket binary frame")
			}
			assembled = append(assembled, payload...)
			fragmented = !final
		case 0:
			if !fragmented {
				return nil, errors.New("unexpected WebSocket continuation")
			}
			assembled = append(assembled, payload...)
			fragmented = !final
		default:
			return nil, errors.New("expected binary WebSocket data")
		}
		if !fragmented {
			return assembled, nil
		}
	}
}

func (c *messageConn) WriteMessage(payload []byte) error {
	if len(payload) == 0 || len(payload) > maxMessage {
		return errors.New("invalid WebSocket message size")
	}
	return c.writeFrame(2, payload)
}

func (c *messageConn) writeFrame(opcode byte, payload []byte) error {
	c.write.Lock()
	defer c.write.Unlock()
	var header [14]byte
	header[0] = 0x80 | opcode
	n := 2
	if len(payload) < 126 {
		header[1] = byte(len(payload))
	} else {
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(len(payload)))
		n = 4
	}
	if !c.client {
		if err := writeAll(c.conn, header[:n]); err != nil {
			return err
		}
		return writeAll(c.conn, payload)
	}
	header[1] |= 0x80
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	copy(header[n:], mask[:])
	n += 4
	encoded := make([]byte, len(payload))
	for i, b := range payload {
		encoded[i] = b ^ mask[i%4]
	}
	if err := writeAll(c.conn, header[:n]); err != nil {
		return err
	}
	return writeAll(c.conn, encoded)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
