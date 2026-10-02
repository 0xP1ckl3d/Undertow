package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/transport/dns"
	"undertow/internal/transport/quic"
	"undertow/internal/transport/relay"
	"undertow/internal/transport/websocket"
)

const ConfigVersion uint32 = 1

// Config is the resolved connection configuration shared by CLI and embedded agents.
// Credential is the 32-byte enrollment secret, never the agent's identity key.
type Config struct {
	Version               uint32                   `json:"version"`
	Server                string                   `json:"server"`
	Transport             string                   `json:"transport"`
	Domain                string                   `json:"domain,omitempty"`
	Fingerprint           string                   `json:"fingerprint"`
	AuthMode              string                   `json:"auth_mode"`
	Credential            []byte                   `json:"credential"`
	PayloadProfile        string                   `json:"payload_profile,omitempty"`
	WebSocketPath         string                   `json:"websocket_path,omitempty"`
	TLSServerName         string                   `json:"tls_server_name,omitempty"`
	TLSInsecureSkipVerify bool                     `json:"tls_insecure_skip_verify,omitempty"`
	AdvertisedRoutes      []string                 `json:"advertised_routes,omitempty"`
	DeniedCapabilities    string                   `json:"denied_capabilities,omitempty"`
	IdentityPath          string                   `json:"-"`
	IdentityKey           ed25519.PrivateKey       `json:"-"`
	Metadata              control.ArtifactIdentity `json:"-"`
	Packaged              bool                     `json:"-"`
}

func (c Config) Validate() error {
	if c.Version != ConfigVersion {
		return fmt.Errorf("unsupported configuration version %d", c.Version)
	}
	host, port, err := net.SplitHostPort(c.Server)
	if err != nil {
		return fmt.Errorf("invalid agent server: %w", err)
	}
	number, err := strconv.Atoi(port)
	if host == "" || host == "0.0.0.0" || host == "::" || err != nil || number < 1 || number > 65535 {
		return errors.New("agent server must be a reachable HOST:PORT")
	}
	switch c.Transport {
	case "dns", "websocket", "quic", "relay":
	default:
		return fmt.Errorf("unsupported agent transport %q", c.Transport)
	}
	if c.Transport == "dns" {
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() == nil {
			return errors.New("DNS agent server must be numeric IPv4")
		}
	}
	raw, err := hex.DecodeString(c.Fingerprint)
	if err != nil || len(raw) != 32 || c.Fingerprint != hex.EncodeToString(raw) {
		return errors.New("agent server fingerprint must be 64 hexadecimal characters")
	}
	if len(c.Credential) != 32 {
		return errors.New("agent enrollment credential must be 32 bytes")
	}
	if c.AuthMode != "token" && c.AuthMode != "password" && c.AuthMode != "none" && c.AuthMode != "artifact" {
		return errors.New("invalid agent authentication mode")
	}
	if c.PayloadProfile != "" && c.PayloadProfile != "auto" && c.PayloadProfile != "small" && c.PayloadProfile != "large" {
		return errors.New("invalid DNS payload profile")
	}
	if c.Transport != "dns" && c.PayloadProfile != "" && c.PayloadProfile != "auto" {
		return errors.New("DNS payload profile requires DNS transport")
	}
	if len(c.AdvertisedRoutes) > 16 {
		return errors.New("too many advertised routes")
	}
	for _, route := range c.AdvertisedRoutes {
		p, err := netip.ParsePrefix(route)
		if err != nil || !p.Addr().Is4() || p != p.Masked() {
			return fmt.Errorf("invalid advertised IPv4 route %q", route)
		}
	}
	if _, err := pivot.ParseDenied(c.DeniedCapabilities); err != nil {
		return err
	}
	if c.WebSocketPath != "" && (!strings.HasPrefix(c.WebSocketPath, "/") || strings.ContainsAny(c.WebSocketPath, "?#")) {
		return errors.New("websocket path must begin with /")
	}
	return nil
}

func (c Config) Dial(ctx context.Context, key ed25519.PrivateKey) (transport.Connection, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	switch c.Transport {
	case "websocket":
		path := c.WebSocketPath
		if path == "" {
			path = "/undertow"
		}
		return websocket.Dial(ctx, websocket.DialOptions{Address: c.Server, Path: path, TLSServerName: c.TLSServerName, TLSInsecureSkipVerify: c.TLSInsecureSkipVerify}, c.Fingerprint, c.Credential, key)
	case "quic":
		return quic.Dial(ctx, quic.DialOptions{Address: c.Server, TLSServerName: c.TLSServerName, TLSInsecureSkipVerify: c.TLSInsecureSkipVerify}, c.Fingerprint, c.Credential, key)
	case "relay":
		return relay.Dial(ctx, c.Server, c.Fingerprint, c.Credential, key)
	default:
		domain := c.Domain
		if domain == "" {
			domain = "t.undertow.invalid"
		}
		if c.PayloadProfile == "" || c.PayloadProfile == "auto" {
			return dns.DialAdaptive(ctx, c.Server, domain, c.Fingerprint, c.Credential, key)
		}
		profile := byte(0)
		if c.PayloadProfile == "small" {
			profile = 1
		}
		return dns.DialProfile(ctx, c.Server, domain, c.Fingerprint, c.Credential, key, profile)
	}
}

// Run is the single agent connection, inventory, service, and reconnect loop.
func Run(ctx context.Context, c Config, ready func() error) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var key ed25519.PrivateKey
	if c.Packaged {
		if len(c.IdentityKey) != ed25519.PrivateKeySize || !ed25519.NewKeyFromSeed(c.IdentityKey[:ed25519.SeedSize]).Equal(c.IdentityKey) {
			return errors.New("invalid embedded identity")
		}
		key = append(ed25519.PrivateKey(nil), c.IdentityKey...)
	} else {
		if c.IdentityPath == "" {
			return errors.New("manual agent identity path is required")
		}
		var err error
		key, err = security.LoadOrCreateKey(c.IdentityPath)
		if err != nil {
			return err
		}
	}
	caps, err := pivot.ParseDenied(c.DeniedCapabilities)
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	first := true
	failures := 0
	for {
		conn, err := c.Dial(ctx, key)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("connect failed: %v", err)
		} else {
			if first && ready != nil {
				if err := ready(); err != nil {
					conn.Close()
					return err
				}
			}
			first = false
			started := time.Now()
			if d, ok := conn.(*dns.Client); ok {
				log.Printf("connected: session=%d agent=%s fragment=%d", conn.ID(), security.Fingerprint(key), d.Session.Stats().FragmentSize)
			} else {
				log.Printf("connected: session=%d agent=%s transport=%s", conn.ID(), security.Fingerprint(key), c.Transport)
			}
			streamMux := mux.New(ctx, conn, false)
			metadata := c.Metadata
			metadata.ReconnectAttempts = uint32(failures)
			if c.Packaged {
				metadata.ReconnectPolicy = "progressive"
			}
			inventoryReady := true
			if err := control.SendInventoryWithIdentity(ctx, streamMux, c.AdvertisedRoutes, caps, metadata); err != nil {
				inventoryReady = false
				log.Printf("inventory: %v", err)
			}
			var shutdown func()
			if c.Packaged {
				shutdown = stop
			}
			pivot.ServeAgentWithLifecycle(ctx, streamMux, caps, shutdown)
			streamMux.Close()
			conn.Close()
			if ctx.Err() != nil {
				return nil
			}
			failures = failureIndexAfterSession(failures, time.Since(started), inventoryReady)
			log.Print("agent session ended; reconnecting")
		}
		if !waitReconnect(ctx, reconnectDelay(c.Packaged, failures)) {
			return nil
		}
		failures++
	}
}

var progressiveDelays = [...]time.Duration{2, 5, 10, 30, 60, 120, 300}

func reconnectDelay(packaged bool, failures int) time.Duration {
	if !packaged {
		return 2 * time.Second
	}
	if failures < 0 {
		failures = 0
	}
	if failures >= len(progressiveDelays) {
		failures = len(progressiveDelays) - 1
	}
	return progressiveDelays[failures] * time.Second
}

func failureIndexAfterSession(failures int, duration time.Duration, inventoryReady bool) int {
	if inventoryReady && duration >= 30*time.Second {
		return 0
	}
	return failures
}

func waitReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
