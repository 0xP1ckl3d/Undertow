package dns

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"undertow/internal/security"
	"undertow/internal/session"
)

// TestShapedDirectDNS is an opt-in, repeatable direct-DNS measurement. The
// proxy delays and drops whole DNS datagrams in both directions; it does not
// emulate a recursive resolver, an Internet route, or an operating-system TUN.
func TestShapedDirectDNS(t *testing.T) {
	if os.Getenv("UNDERTOW_PERF") != "1" {
		t.Skip("set UNDERTOW_PERF=1 to run the shaped DNS matrix")
	}
	for _, rtt := range perfValues(t, "UNDERTOW_PERF_RTT", "5,25,50,100,200") {
		for _, loss := range perfValues(t, "UNDERTOW_PERF_LOSS", "0,1,2,5,10,20") {
			for _, flows := range perfValues(t, "UNDERTOW_PERF_FLOWS", "1,10,100") {
				t.Run(fmt.Sprintf("rtt=%dms/loss=%d%%/flows=%d", rtt, loss, flows), func(t *testing.T) {
					measureDirectDNS(t, time.Duration(rtt)*time.Millisecond, loss, flows)
				})
			}
		}
	}
}

func perfValues(t *testing.T, name, fallback string) []int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		value = fallback
	}
	var out []int
	for _, item := range strings.Split(value, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || n < 0 {
			t.Fatalf("invalid %s value %q", name, item)
		}
		out = append(out, n)
	}
	return out
}

type perfResult struct {
	RTTms          int     `json:"rtt_ms"`
	LossPercent    int     `json:"loss_percent"`
	Flows          int     `json:"flows"`
	SmallP50ms     float64 `json:"small_p50_ms"`
	SmallP95ms     float64 `json:"small_p95_ms"`
	BulkKibps      float64 `json:"bulk_kib_per_sec"`
	BulkSeconds    float64 `json:"bulk_seconds"`
	QueriesPerSec  float64 `json:"queries_per_sec"`
	Retransmits    uint64  `json:"retransmits"`
	Duplicates     uint64  `json:"duplicates"`
	MaxWindow      int     `json:"max_congestion_window"`
	MaxOutstanding int     `json:"max_outstanding_queries"`
	FragmentSize   int     `json:"fragment_size"`
	Error          string  `json:"error,omitempty"`
}

func measureDirectDNS(t *testing.T, rtt time.Duration, loss, flows int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := Listen("127.0.0.1:0", "t.undertow.invalid", identity, bytes.Repeat([]byte{81}, 32))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	proxy, err := newPerfProxy(ctx, srv.Addr().(*net.UDPAddr), rtt, loss)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		select {
		case p := <-srv.Accepted():
			for {
				b, e := p.Session.Recv(ctx)
				if e != nil {
					return
				}
				for {
					e = p.Session.Send(ctx, b)
					if e != session.ErrQueueFull {
						break
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Millisecond):
					}
				}
				if e != nil {
					return
				}
			}
		case <-ctx.Done():
		}
	}()
	client, err := Dial(ctx, proxy.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), bytes.Repeat([]byte{81}, 32), clientKey)
	if err != nil {
		t.Fatalf("dial shaped DNS: %v", err)
	}
	defer client.Close()
	result := perfResult{RTTms: int(rtt.Milliseconds()), LossPercent: loss, Flows: flows}
	var maxWindow, maxOutstanding atomic.Int64
	stopSample := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSample:
				return
			case <-ticker.C:
				if v := int64(client.Session.Stats().CongestionWindow); v > maxWindow.Load() {
					maxWindow.Store(v)
				}
				if v := client.AdaptiveStats().Outstanding; v > maxOutstanding.Load() {
					maxOutstanding.Store(v)
				}
			}
		}
	}()
	defer close(stopSample)
	begin := time.Now()
	before, q0, _ := client.Stats()
	latencies, err := perfExchange(ctx, client, flows, 64)
	if err == nil {
		result.SmallP50ms = float64(perfPercentile(latencies, 50).Microseconds()) / 1000
		result.SmallP95ms = float64(perfPercentile(latencies, 95).Microseconds()) / 1000
		bulkBegin := time.Now()
		_, err = perfExchange(ctx, client, flows, 2048)
		result.BulkSeconds = time.Since(bulkBegin).Seconds()
		if err == nil && result.BulkSeconds > 0 {
			result.BulkKibps = float64(2*flows*2048) / 1024 / result.BulkSeconds
		}
	}
	if err != nil {
		result.Error = err.Error()
	}
	after, q1, _ := client.Stats()
	result.Retransmits = after.Retransmits - before.Retransmits
	result.Duplicates = after.Duplicates - before.Duplicates
	result.MaxWindow = int(maxWindow.Load())
	result.MaxOutstanding = int(maxOutstanding.Load())
	result.FragmentSize = after.FragmentSize
	result.QueriesPerSec = float64(q1-q0) / time.Since(begin).Seconds()
	encoded, _ := json.Marshal(result)
	t.Logf("PERF %s", encoded)
}

func perfExchange(ctx context.Context, client *Client, flows, size int) ([]time.Duration, error) {
	phase, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	start := make([]time.Time, flows)
	latency := make([]time.Duration, flows)
	for i := 0; i < flows; i++ {
		payload := make([]byte, size)
		binary.BigEndian.PutUint32(payload[:4], uint32(i))
		for j := 4; j < len(payload); j++ {
			payload[j] = byte(i + 1)
		}
		start[i] = time.Now()
		if err := client.Send(phase, payload); err != nil {
			return nil, err
		}
	}
	for received := 0; received < flows; received++ {
		payload, err := client.Recv(phase)
		if err != nil {
			return nil, err
		}
		if len(payload) != size {
			return nil, fmt.Errorf("echo size %d, expected %d", len(payload), size)
		}
		i := int(binary.BigEndian.Uint32(payload[:4]))
		if i >= flows || latency[i] != 0 {
			return nil, fmt.Errorf("duplicate or unknown echo %d", i)
		}
		for _, b := range payload[4:] {
			if b != byte(i+1) {
				return nil, fmt.Errorf("corrupt echo %d", i)
			}
		}
		latency[i] = time.Since(start[i])
	}
	return latency, nil
}

func perfPercentile(values []time.Duration, percentile int) time.Duration {
	copyOf := append([]time.Duration(nil), values...)
	for i := 1; i < len(copyOf); i++ {
		for j := i; j > 0 && copyOf[j] < copyOf[j-1]; j-- {
			copyOf[j], copyOf[j-1] = copyOf[j-1], copyOf[j]
		}
	}
	return copyOf[(len(copyOf)-1)*percentile/100]
}

type perfProxy struct {
	conn    *net.UDPConn
	cancel  context.CancelFunc
	server  *net.UDPAddr
	rtt     time.Duration
	loss    int
	counter atomic.Uint64
	wg      sync.WaitGroup
}

func newPerfProxy(ctx context.Context, server *net.UDPAddr, rtt time.Duration, loss int) (*perfProxy, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	p := &perfProxy{conn: conn, cancel: cancel, server: server, rtt: rtt, loss: loss}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		<-runCtx.Done()
		conn.Close()
	}()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		var buf [maxDNS]byte
		for {
			n, addr, err := conn.ReadFromUDP(buf[:])
			if err != nil {
				return
			}
			packet := append([]byte(nil), buf[:n]...)
			p.wg.Add(1)
			go func() { defer p.wg.Done(); p.exchange(runCtx, addr, packet) }()
		}
	}()
	return p, nil
}

func (p *perfProxy) Addr() net.Addr { return p.conn.LocalAddr() }
func (p *perfProxy) Close() error   { p.cancel(); err := p.conn.Close(); p.wg.Wait(); return err }

func (p *perfProxy) dropped() bool {
	if p.loss == 0 {
		return false
	}
	n := p.counter.Add(1)
	// A fixed-seed bit mixer makes the result repeatable without periodic loss.
	n ^= n >> 30
	n *= 0xbf58476d1ce4e5b9
	n ^= n >> 27
	n *= 0x94d049bb133111eb
	n ^= n >> 31
	return n%100 < uint64(p.loss)
}

func (p *perfProxy) exchange(ctx context.Context, addr *net.UDPAddr, request []byte) {
	if p.dropped() {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(p.rtt / 2):
	}
	upstream, err := net.DialUDP("udp", nil, p.server)
	if err != nil {
		return
	}
	defer upstream.Close()
	if _, err = upstream.Write(request); err != nil {
		return
	}
	_ = upstream.SetReadDeadline(time.Now().Add(2 * time.Second))
	var buf [maxDNS]byte
	n, err := upstream.Read(buf[:])
	if err != nil || p.dropped() {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(p.rtt / 2):
	}
	_, _ = p.conn.WriteToUDP(buf[:n], addr)
}
