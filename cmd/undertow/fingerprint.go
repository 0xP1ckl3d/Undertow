package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"undertow/internal/transport/dns"
)

func normalizeFingerprint(value string) (string, error) {
	value = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), ":", "")
	b, err := hex.DecodeString(value)
	if err != nil || len(b) != 32 {
		return "", errors.New("server fingerprint must be 64 hexadecimal characters")
	}
	return hex.EncodeToString(b), nil
}

func resolveServerFingerprint(ctx context.Context, server, domain, explicit, path string, trustFirstUse bool) (string, bool, error) {
	if explicit != "" {
		if trustFirstUse {
			return "", false, errors.New("choose --fingerprint or --trust-on-first-use")
		}
		fingerprint, err := normalizeFingerprint(explicit)
		return fingerprint, false, err
	}
	if b, err := os.ReadFile(path); err == nil {
		fingerprint, err := normalizeFingerprint(string(b))
		return fingerprint, false, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if !trustFirstUse {
		return "", false, fmt.Errorf("no server fingerprint: pass --fingerprint, provide %s, or opt in with --trust-on-first-use", path)
	}
	discoverCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fingerprint, err := dns.DiscoverFingerprint(discoverCtx, server, domain)
	return fingerprint, true, err
}

func saveServerFingerprint(path, fingerprint string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.TrimSpace(string(b)) != fingerprint {
			return errors.New("saved server fingerprint changed during first connection")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(file, fingerprint); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	return file.Close()
}
