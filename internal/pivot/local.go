package pivot

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// BridgeConn copies a userland-stack TCP flow to a server-side socket.
func BridgeConn(ctx context.Context, a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	copyHalf := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyHalf(a, b)
	go copyHalf(b, a)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-ctx.Done():
			a.Close()
			b.Close()
			return
		}
	}
}

// BridgeUDPConn keeps each Read/Write as one datagram between connected UDP
// sockets. TUN MTU bounds packets well below the temporary buffer size.
func BridgeUDPConn(ctx context.Context, a, b net.Conn) {
	var once sync.Once
	stop := func() { once.Do(func() { a.Close(); b.Close() }) }
	defer stop()
	done := make(chan struct{}, 2)
	copyPackets := func(dst, src net.Conn) {
		buf := make([]byte, 65535)
		for {
			_ = src.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			n, err := src.Read(buf)
			if err != nil {
				done <- struct{}{}
				return
			}
			if _, err = dst.Write(buf[:n]); err != nil {
				done <- struct{}{}
				return
			}
		}
	}
	go copyPackets(a, b)
	go copyPackets(b, a)
	select {
	case <-done:
	case <-ctx.Done():
	}
	stop()
}
