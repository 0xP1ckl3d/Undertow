package security

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnrollmentSecret returns the key used to prove that a client is allowed to
// open a session. Open enrollment still uses the authenticated, encrypted
// transport and requires clients to pin the server identity fingerprint.
func EnrollmentSecret(mode, tokenFile, password, passwordFile, fingerprint string) ([]byte, error) {
	if password != "" && passwordFile != "" {
		return nil, errors.New("choose either --password or --password-file")
	}
	switch mode {
	case "token":
		if password != "" || passwordFile != "" {
			return nil, errors.New("password options require --auth password")
		}
		return ReadToken(tokenFile)
	case "password":
		if passwordFile != "" {
			b, err := os.ReadFile(passwordFile)
			if err != nil {
				return nil, err
			}
			password = strings.TrimRight(string(b), "\r\n")
		}
		if len(password) < 12 {
			return nil, errors.New("password must be at least 12 bytes; use --password or --password-file")
		}
		fingerprintBytes, err := hex.DecodeString(strings.ReplaceAll(strings.ToLower(fingerprint), ":", ""))
		if err != nil || len(fingerprintBytes) != sha256.Size {
			return nil, fmt.Errorf("invalid server fingerprint for password authentication")
		}
		salt := append([]byte("undertow-password-enrollment-v1"), fingerprintBytes...)
		return pbkdf2.Key(sha256.New, password, salt, 600_000, 32)
	case "none":
		if password != "" || passwordFile != "" {
			return nil, errors.New("password options require --auth password")
		}
		return make([]byte, 32), nil
	default:
		return nil, errors.New("--auth must be token, password, or none")
	}
}
