package pivot

import (
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"unicode"
	"unicode/utf8"
)

// WindowsCredential carries an optional outbound Windows identity inside the
// authenticated agent session. Secrets are never included in Job or Jump
// records and callers must not format this value in errors or logs.
type WindowsCredential struct {
	Username string `json:"username"`
	Domain   string `json:"domain,omitempty"`
	Password string `json:"password,omitempty"`
	NTHash   string `json:"nt_hash,omitempty"`
}

func NewWindowsCredential(target, account, password string) (*WindowsCredential, error) {
	return newWindowsCredential(target, account, password, "")
}

// NewWindowsHashCredential creates a network-only Windows credential. The NT
// hash is normalized to 32 lowercase hexadecimal characters. It must never be
// formatted into an error or persisted in a Job, Jump, audit, or queue record.
func NewWindowsHashCredential(target, account, ntHash string) (*WindowsCredential, error) {
	return newWindowsCredential(target, account, "", ntHash)
}

func NewWindowsCredentialSecret(target, account, password, ntHash string) (*WindowsCredential, error) {
	return newWindowsCredential(target, account, password, ntHash)
}

func newWindowsCredential(target, account, password, ntHash string) (*WindowsCredential, error) {
	account = strings.TrimSpace(account)
	var err error
	ntHash, err = normalizeNTHash(ntHash)
	if err != nil {
		return nil, err
	}
	if account == "" && password == "" && ntHash == "" {
		return nil, nil
	}
	if password != "" && ntHash != "" {
		return nil, errors.New("choose either a Windows password or NT hash")
	}
	if account == "" || password == "" && ntHash == "" {
		return nil, errors.New("Windows username and one credential secret are required")
	}
	if ntHash != "" {
		decoded, err := hex.DecodeString(ntHash)
		if err != nil || len(decoded) != 16 {
			return nil, errors.New("NT hash must be exactly 32 hexadecimal characters")
		}
	}
	if !utf8.ValidString(account) || !utf8.ValidString(password) || len(account) > 256 || len(password) > 512 {
		return nil, errors.New("Windows credentials exceed the supported length")
	}
	if strings.IndexFunc(account, func(r rune) bool { return r == 0 || unicode.IsControl(r) }) >= 0 || strings.ContainsRune(password, 0) {
		return nil, errors.New("Windows credentials contain invalid characters")
	}
	credential := &WindowsCredential{Password: password, NTHash: ntHash}
	if slash := strings.IndexByte(account, '\\'); slash >= 0 {
		if slash == 0 || slash == len(account)-1 || strings.IndexByte(account[slash+1:], '\\') >= 0 {
			return nil, errors.New("Windows username must be USER, DOMAIN\\USER, or USER@DOMAIN")
		}
		credential.Domain, credential.Username = account[:slash], account[slash+1:]
	} else if strings.Contains(account, "@") {
		if strings.HasPrefix(account, "@") || strings.HasSuffix(account, "@") || strings.Count(account, "@") != 1 {
			return nil, errors.New("Windows username must be USER, DOMAIN\\USER, or USER@DOMAIN")
		}
		if ntHash != "" {
			credential.Username, credential.Domain, _ = strings.Cut(account, "@")
		} else {
			credential.Username = account
		}
	} else {
		domain := strings.TrimSpace(strings.Trim(target, "[]"))
		if dot := strings.IndexByte(domain, '.'); net.ParseIP(domain) == nil && dot > 0 {
			domain = domain[:dot]
		}
		if domain == "" {
			return nil, errors.New("target is required for a local Windows username")
		}
		credential.Domain, credential.Username = domain, account
	}
	return credential, nil
}

func normalizeNTHash(value string) (string, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return "", errors.New("NT hash must be NT or LM:NT format")
	}
	if len(parts) == 2 {
		if parts[0] != "" {
			decoded, err := hex.DecodeString(parts[0])
			if err != nil || len(decoded) != 16 {
				return "", errors.New("LM portion must be exactly 32 hexadecimal characters")
			}
		}
		value = parts[len(parts)-1]
	}
	return strings.ToLower(strings.TrimSpace(value)), nil
}

func (c *WindowsCredential) UsesNTHash() bool { return c != nil && c.NTHash != "" }

func (c *WindowsCredential) Account() string {
	if c == nil {
		return ""
	}
	if c.Domain != "" {
		return c.Domain + `\` + c.Username
	}
	return c.Username
}
