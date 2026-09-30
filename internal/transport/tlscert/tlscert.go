package tlscert

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Load returns a supplied certificate or generates an ephemeral, self-signed
// certificate for direct-IP connections. The Undertow identity pin is separate.
func Load(certFile, keyFile string, selfSigned bool) (tls.Certificate, error) {
	if selfSigned {
		if certFile != "" || keyFile != "" {
			return tls.Certificate{}, errors.New("--tls-self-signed cannot be combined with --tls-cert or --tls-key")
		}
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("generate TLS key: %w", err)
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("generate TLS serial: %w", err)
		}
		now := time.Now()
		template := &x509.Certificate{
			SerialNumber:          serial,
			Subject:               pkix.Name{CommonName: "Undertow temporary TLS"},
			NotBefore:             now.Add(-5 * time.Minute),
			NotAfter:              now.Add(7 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("generate TLS certificate: %w", err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("parse generated TLS certificate: %w", err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private, Leaf: leaf}, nil
	}
	if certFile == "" || keyFile == "" {
		return tls.Certificate{}, errors.New("WebSocket and QUIC servers require --tls-cert and --tls-key or --tls-self-signed")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load TLS certificate: %w", err)
	}
	return certificate, nil
}
