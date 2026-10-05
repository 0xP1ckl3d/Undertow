// Package deployment holds carrier and agent callback settings shared by
// manual clients, servers, and embedded agent profiles.
package deployment

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("duration must be a quoted value such as 15s")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }
func (d Duration) Value() time.Duration         { return time.Duration(d) }

// Jitter spreads a callback wait symmetrically around its nominal delay.
func Jitter(delay time.Duration, percent int) time.Duration {
	if percent <= 0 {
		return delay
	}
	span := int64(delay) * int64(percent) / 100
	if span <= 0 {
		return delay
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return delay
	}
	return delay - time.Duration(span) + time.Duration(binary.LittleEndian.Uint64(random[:])%uint64(2*span+1))
}

type Profile struct {
	WebSocket struct {
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers,omitempty"`
	} `json:"websocket"`
	QUIC struct {
		ALPN        string   `json:"alpn"`
		IdleTimeout Duration `json:"idle_timeout"`
		KeepAlive   Duration `json:"keepalive"`
	} `json:"quic"`
	Reconnect struct {
		ManualDelay       Duration   `json:"manual_delay"`
		ProgressiveDelays []Duration `json:"progressive_delays"`
		HealthyAfter      Duration   `json:"healthy_after"`
		JitterPercent     int        `json:"jitter_percent"`
	} `json:"reconnect"`
}

func Default() Profile {
	var p Profile
	p.WebSocket.Path = "/undertow"
	p.QUIC.ALPN, p.QUIC.IdleTimeout, p.QUIC.KeepAlive = "undertow/1", Duration(2*time.Minute), Duration(15*time.Second)
	p.Reconnect.ManualDelay, p.Reconnect.HealthyAfter = Duration(2*time.Second), Duration(30*time.Second)
	for _, seconds := range []int{2, 5, 10, 30, 60, 120, 300} {
		p.Reconnect.ProgressiveDelays = append(p.Reconnect.ProgressiveDelays, Duration(time.Duration(seconds)*time.Second))
	}
	return p
}

func (p Profile) Resolved() Profile {
	if p.WebSocket.Path == "" && p.QUIC.ALPN == "" {
		return Default()
	}
	return p
}

func (p Profile) Validate() error {
	p = p.Resolved()
	if !strings.HasPrefix(p.WebSocket.Path, "/") || strings.ContainsAny(p.WebSocket.Path, "?#\r\n \t") {
		return errors.New("websocket.path must be an absolute URL path without a query or fragment")
	}
	for name, value := range p.WebSocket.Headers {
		if len(p.WebSocket.Headers) > 16 || name == "" || len(name) > 64 || len(value) > 1024 || strings.ContainsAny(name, ":\r\n \t") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("invalid websocket header %q", name)
		}
		for _, char := range name {
			if char < '!' || char > '~' {
				return fmt.Errorf("invalid websocket header %q", name)
			}
		}
		for _, char := range value {
			if char < ' ' || char == 127 {
				return fmt.Errorf("invalid websocket header %q", name)
			}
		}
		switch strings.ToLower(name) {
		case "host", "connection", "upgrade", "proxy-authorization", "authorization", "cookie":
			return fmt.Errorf("reserved websocket header %q", name)
		}
		if strings.HasPrefix(strings.ToLower(name), "sec-websocket-") {
			return fmt.Errorf("reserved websocket header %q", name)
		}
	}
	if p.QUIC.ALPN == "" || len(p.QUIC.ALPN) > 255 || strings.ContainsAny(p.QUIC.ALPN, "\r\n") {
		return errors.New("quic.alpn must be a nonempty TLS protocol name")
	}
	if p.QUIC.IdleTimeout <= 0 || p.QUIC.IdleTimeout > Duration(24*time.Hour) || p.QUIC.KeepAlive <= 0 || p.QUIC.KeepAlive >= p.QUIC.IdleTimeout {
		return errors.New("quic.keepalive must be positive and shorter than quic.idle_timeout")
	}
	if p.Reconnect.ManualDelay <= 0 || p.Reconnect.ManualDelay > Duration(24*time.Hour) || p.Reconnect.HealthyAfter <= 0 || p.Reconnect.HealthyAfter > Duration(24*time.Hour) || len(p.Reconnect.ProgressiveDelays) == 0 || len(p.Reconnect.ProgressiveDelays) > 16 {
		return errors.New("invalid reconnect delays")
	}
	for _, delay := range p.Reconnect.ProgressiveDelays {
		if delay <= 0 || delay > Duration(24*time.Hour) {
			return errors.New("reconnect.progressive_delays must be positive")
		}
	}
	if p.Reconnect.JitterPercent < 0 || p.Reconnect.JitterPercent > 50 {
		return errors.New("reconnect.jitter_percent must be between 0 and 50")
	}
	return nil
}

func Load(path string) (Profile, error) {
	p := Default()
	if path == "" {
		return p, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("deployment profile: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Profile{}, errors.New("deployment profile has trailing data")
	}
	return p, p.Validate()
}
