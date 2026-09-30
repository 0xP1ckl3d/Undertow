package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"

	"undertow/internal/transport"
	"undertow/internal/transport/dns"
	"undertow/internal/transport/quic"
	"undertow/internal/transport/websocket"
)

type carrierFlags struct {
	kind, path, cert, key, serverName *string
	skipTLSVerify, selfSigned         *bool
}

func addCarrierFlags(f *flag.FlagSet) carrierFlags {
	return carrierFlags{
		kind:          f.String("transport", "dns", "carrier: dns, websocket, or quic"),
		path:          f.String("websocket-path", "/undertow", "WebSocket HTTP path"),
		cert:          f.String("tls-cert", "", "server TLS certificate PEM for WebSocket or QUIC"),
		key:           f.String("tls-key", "", "server TLS private key PEM for WebSocket or QUIC"),
		selfSigned:    f.Bool("tls-self-signed", false, "generate an ephemeral self-signed server TLS certificate"),
		serverName:    f.String("tls-server-name", "", "TLS certificate server name for WebSocket"),
		skipTLSVerify: f.Bool("tls-insecure-skip-verify", false, "skip TLS certificate verification (Undertow fingerprint is still required)"),
	}
}

func (f carrierFlags) validate() error {
	if *f.kind != "dns" && *f.kind != "websocket" && *f.kind != "quic" {
		return errors.New("--transport must be dns, websocket, or quic")
	}
	if *f.kind == "dns" && (*f.cert != "" || *f.key != "" || *f.serverName != "" || *f.skipTLSVerify || *f.selfSigned) {
		return errors.New("TLS flags require --transport websocket or quic")
	}
	if *f.selfSigned && (*f.cert != "" || *f.key != "") {
		return errors.New("--tls-self-signed cannot be combined with --tls-cert or --tls-key")
	}
	return nil
}

func (f carrierFlags) webOptions(server string) websocket.DialOptions {
	return websocket.DialOptions{Address: server, Path: *f.path, TLSServerName: *f.serverName, TLSInsecureSkipVerify: *f.skipTLSVerify}
}

func (f carrierFlags) quicOptions(server string) quic.DialOptions {
	return quic.DialOptions{Address: server, TLSServerName: *f.serverName, TLSInsecureSkipVerify: *f.skipTLSVerify}
}

func (f carrierFlags) listen(addr, domain string, identity ed25519.PrivateKey, token []byte) (transport.Listener, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	if addr == "" {
		if *f.kind == "dns" {
			addr = "0.0.0.0:53"
		} else {
			addr = "0.0.0.0:443"
		}
	}
	if *f.kind == "websocket" {
		return websocket.Listen(addr, *f.path, *f.cert, *f.key, *f.selfSigned, identity, token)
	}
	if *f.kind == "quic" {
		return quic.Listen(addr, *f.cert, *f.key, *f.selfSigned, identity, token)
	}
	return dns.Listen(addr, domain, identity, token)
}

func (f carrierFlags) dial(ctx context.Context, server, domain, fingerprint string, token []byte, key ed25519.PrivateKey, profileFlag string) (transport.Connection, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	if *f.selfSigned {
		return nil, errors.New("--tls-self-signed is a server-only flag")
	}
	if *f.kind == "websocket" {
		return websocket.Dial(ctx, f.webOptions(server), fingerprint, token, key)
	}
	if *f.kind == "quic" {
		return quic.Dial(ctx, f.quicOptions(server), fingerprint, token, key)
	}
	if profileFlag == "auto" {
		return dns.DialAdaptive(ctx, server, domain, fingerprint, token, key)
	}
	profile := byte(0)
	if profileFlag == "small" {
		profile = 1
	}
	return dns.DialProfile(ctx, server, domain, fingerprint, token, key, profile)
}

func (f carrierFlags) fingerprint(ctx context.Context, server, domain, explicit, path string, trustFirstUse bool) (string, bool, error) {
	if err := f.validate(); err != nil {
		return "", false, err
	}
	if *f.selfSigned {
		return "", false, errors.New("--tls-self-signed is a server-only flag")
	}
	if *f.kind == "dns" {
		return resolveServerFingerprint(ctx, server, domain, explicit, path, trustFirstUse)
	}
	if *f.kind == "quic" {
		return resolveFingerprint(ctx, explicit, path, trustFirstUse, func(ctx context.Context) (string, error) {
			return quic.DiscoverFingerprint(ctx, f.quicOptions(server))
		})
	}
	return resolveFingerprint(ctx, explicit, path, trustFirstUse, func(ctx context.Context) (string, error) {
		return websocket.DiscoverFingerprint(ctx, f.webOptions(server))
	})
}

func (f carrierFlags) describe() string {
	if *f.kind == "dns" {
		return "direct DNS"
	}
	if *f.kind == "quic" {
		return "QUIC"
	}
	return fmt.Sprintf("WebSocket TLS path=%s", *f.path)
}
