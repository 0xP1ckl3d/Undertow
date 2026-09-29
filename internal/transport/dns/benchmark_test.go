package dns

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"undertow/internal/security"
)

// BenchmarkDirectDNS measures an actual localhost DNS/UDP carrier and the
// encrypted reliable session. It does not represent a shaped network path.
func BenchmarkDirectDNS(b *testing.B) {
	for _, size := range []int{64, 512, 4096} {
		b.Run(fmt.Sprintf("echo-%d", size), func(b *testing.B) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, identity, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				b.Fatal(err)
			}
			_, clientKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				b.Fatal(err)
			}
			token := bytes.Repeat([]byte{88}, 32)
			srv, err := Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
			if err != nil {
				b.Fatal(err)
			}
			go func() { _ = srv.Serve(ctx) }()
			go func() {
				select {
				case p := <-srv.Accepted():
					for {
						data, e := p.Session.Recv(ctx)
						if e != nil {
							return
						}
						if p.Session.Send(ctx, data) != nil {
							return
						}
					}
				case <-ctx.Done():
				}
			}()
			client, err := Dial(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, clientKey)
			if err != nil {
				b.Fatal(err)
			}
			defer client.Close()
			payload := bytes.Repeat([]byte{byte(size)}, size)
			b.SetBytes(int64(2 * size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				exchangeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				if err := client.Send(exchangeCtx, payload); err != nil {
					stop()
					b.Fatal(err)
				}
				reply, err := client.Recv(exchangeCtx)
				stop()
				if err != nil {
					b.Fatal(err)
				}
				if !bytes.Equal(reply, payload) {
					b.Fatal("DNS echo mismatch")
				}
			}
		})
	}
}
