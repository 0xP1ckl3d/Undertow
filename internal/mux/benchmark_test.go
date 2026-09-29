package mux

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/hashicorp/yamux"
	"github.com/xtaci/smux"
)

// recordConn exposes an ordered message carrier over net.Pipe. This gives the
// custom mux the same in-memory connection used by the byte-stream muxes.
type recordConn struct {
	net.Conn
	writeMu sync.Mutex
}

func (c *recordConn) Send(ctx context.Context, b []byte) error {
	if len(b) > 65535 {
		return io.ErrShortBuffer
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(b)))
	if _, err := c.Conn.Write(header[:]); err != nil {
		return err
	}
	_, err := c.Conn.Write(b)
	return err
}
func (c *recordConn) Recv(ctx context.Context) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(header[:]))
	_, err := io.ReadFull(c.Conn, b)
	return b, err
}

type benchStream interface{ io.ReadWriteCloser }
type benchPeer struct {
	open   func(context.Context) (benchStream, error)
	accept func() (benchStream, error)
	close  func()
}

func makeBenchPeer(kind string) (benchPeer, error) {
	a, b := net.Pipe()
	return makeBenchPeerOnCarrier(kind, a, b, &recordConn{Conn: a}, &recordConn{Conn: b}, func() {})
}

func makeBenchPeerOnCarrier(kind string, a, b net.Conn, ta, tb interface {
	Send(context.Context, []byte) error
	Recv(context.Context) ([]byte, error)
	Close() error
}, cleanup func()) (benchPeer, error) {
	switch kind {
	case "custom":
		ctx, cancel := context.WithCancel(context.Background())
		proxy := New(ctx, ta, true)
		agent := New(ctx, tb, false)
		return benchPeer{
			open: func(ctx context.Context) (benchStream, error) { return proxy.Open(ctx, "127.0.0.1:80") },
			accept: func() (benchStream, error) {
				s, err := agent.Accept(ctx)
				if err == nil {
					err = s.AcceptOpen(ctx)
				}
				return s, err
			},
			close: func() { cancel(); proxy.Close(); agent.Close(); cleanup() },
		}, nil
	case "yamux":
		server, err := yamux.Server(b, nil)
		if err != nil {
			return benchPeer{}, err
		}
		client, err := yamux.Client(a, nil)
		if err != nil {
			server.Close()
			return benchPeer{}, err
		}
		return benchPeer{
			open:   func(ctx context.Context) (benchStream, error) { return client.Open() },
			accept: func() (benchStream, error) { return server.Accept() },
			close:  func() { client.Close(); server.Close(); cleanup() },
		}, nil
	case "smux":
		server, err := smux.Server(b, nil)
		if err != nil {
			return benchPeer{}, err
		}
		client, err := smux.Client(a, nil)
		if err != nil {
			server.Close()
			return benchPeer{}, err
		}
		return benchPeer{
			open:   func(ctx context.Context) (benchStream, error) { return client.OpenStream() },
			accept: func() (benchStream, error) { return server.AcceptStream() },
			close:  func() { client.Close(); server.Close(); cleanup() },
		}, nil
	}
	cleanup()
	return benchPeer{}, fmt.Errorf("unknown mux %s", kind)
}

// BenchmarkMultiplexers measures full request/response completion through 1,
// 10, or 100 simultaneous streams over an identical local ordered carrier.
// Loss and DNS timing are covered separately by the integration suite.
func BenchmarkMultiplexers(b *testing.B)      { benchmarkMultiplexers(b, false) }
func BenchmarkLossyMultiplexers(b *testing.B) { benchmarkMultiplexers(b, true) }

func benchmarkMultiplexers(b *testing.B, lossy bool) {
	for _, kind := range []string{"custom", "yamux", "smux"} {
		flowCounts := []int{1, 10, 100}
		if lossy {
			flowCounts = []int{10}
		}
		for _, flows := range flowCounts {
			b.Run(fmt.Sprintf("%s/%d", kind, flows), func(b *testing.B) {
				var peer benchPeer
				var err error
				if !lossy {
					peer, err = makeBenchPeer(kind)
					defer peer.close()
				}
				if err != nil {
					b.Fatal(err)
				}
				request := make([]byte, 4096)
				b.SetBytes(int64(len(request) * 2 * flows))
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					if lossy {
						b.StopTimer()
						peer, err = makeLossyBenchPeer(kind)
						if err != nil {
							b.Fatal(err)
						}
						b.StartTimer()
					}
					ctx := context.Background()
					errCh := make(chan error, flows*2)
					opened := make(chan benchStream, flows*2)
					var wg sync.WaitGroup
					for i := 0; i < flows; i++ {
						wg.Add(2)
						go func() {
							defer wg.Done()
							s, err := peer.accept()
							if err != nil {
								errCh <- err
								return
							}
							defer func() { opened <- s }()
							buf := make([]byte, len(request))
							if _, err = io.ReadFull(s, buf); err == nil {
								_, err = s.Write(buf)
							}
							if err == nil {
								_, err = io.ReadFull(s, buf[:1])
							}
							if err != nil {
								errCh <- err
							}
						}()
						go func() {
							defer wg.Done()
							s, err := peer.open(ctx)
							if err != nil {
								errCh <- err
								return
							}
							defer func() { opened <- s }()
							if _, err = s.Write(request); err != nil {
								errCh <- err
								return
							}
							buf := make([]byte, len(request))
							if _, err = io.ReadFull(s, buf); err != nil {
								errCh <- err
								return
							}
							_, _ = s.Write([]byte{1})
						}()
					}
					wg.Wait()
					close(opened)
					for s := range opened {
						_ = s.Close()
					}
					close(errCh)
					for err := range errCh {
						b.Fatal(err)
					}
					if lossy {
						b.StopTimer()
						peer.close()
						b.StartTimer()
					}
				}
			})
		}
	}
}
