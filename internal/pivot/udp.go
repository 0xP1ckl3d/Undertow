package pivot

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"

	"undertow/internal/mux"
)

const udpIdleTimeout = 60 * time.Second

// BridgeUDP carries datagram boundaries over one logical stream. The agent
// socket is connected to one destination, so replies cannot change source.
func BridgeUDP(ctx context.Context, local net.Conn, stream *mux.Stream) {
	var once sync.Once
	stop := func() { once.Do(func() { _ = local.Close(); _ = stream.Close() }) }
	defer stop()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 65535)
		for {
			_ = local.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			n, err := local.Read(buf)
			if err != nil {
				stop()
				return
			}
			frame := make([]byte, 2+n)
			binary.BigEndian.PutUint16(frame[:2], uint16(n))
			copy(frame[2:], buf[:n])
			if _, err = stream.Write(frame); err != nil {
				stop()
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		var header [2]byte
		for {
			if _, err := io.ReadFull(stream, header[:]); err != nil {
				stop()
				return
			}
			n := int(binary.BigEndian.Uint16(header[:]))
			if n == 0 {
				stop()
				return
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(stream, buf); err != nil {
				stop()
				return
			}
			if _, err := local.Write(buf); err != nil {
				stop()
				return
			}
			_ = local.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		}
	}()
	select {
	case <-ctx.Done():
		stop()
	case <-stream.Done():
		stop()
	}
	wg.Wait()
}
