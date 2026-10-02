package security

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	Hello           = 1
	Cookie          = 2
	ServerHello     = 3
	Auth            = 4
	AuthOK          = 5
	AuthReject      = 6
	PayloadProbe    = 7
	AdaptiveProfile = 2
)

var ErrHandshake = errors.New("invalid handshake")

// EnrollmentVerifier authenticates a handshake and identifies whether the
// credential belongs to a configured artifact. An empty artifact ID denotes
// the existing manual-agent/client credential.
type EnrollmentVerifier func(auth, transcript []byte) ([16]byte, string, error)

type Keys struct {
	ClientToServer [32]byte
	ServerToClient [32]byte
	ClientPrefix   [4]byte
	ServerPrefix   [4]byte
}

type ClientState struct {
	Ephemeral    *ecdh.PrivateKey
	Nonce        [32]byte
	Cookie       [40]byte
	Profile      byte
	FragmentSize uint16
	Transcript   []byte
	SessionID    uint64
}

func NewClient() (*ClientState, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	s := &ClientState{Ephemeral: k}
	_, err = rand.Read(s.Nonce[:])
	return s, err
}

func (s *ClientState) Hello() []byte {
	length := 1 + 32 + 32 + 1 + 40
	if s.Profile == AdaptiveProfile {
		length += 2
	}
	b := make([]byte, length)
	b[0] = Hello
	copy(b[1:33], s.Nonce[:])
	copy(b[33:65], s.Ephemeral.PublicKey().Bytes())
	b[65] = s.Profile
	cookieOffset := 66
	if s.Profile == AdaptiveProfile {
		binary.BigEndian.PutUint16(b[66:68], s.FragmentSize)
		cookieOffset = 68
	}
	copy(b[cookieOffset:], s.Cookie[:])
	return b
}

func helloCookieOffset(hello []byte) (int, bool) {
	if len(hello) == 106 && hello[0] == Hello && hello[65] <= 1 {
		return 66, true
	}
	if len(hello) == 108 && hello[0] == Hello && hello[65] == AdaptiveProfile {
		size := binary.BigEndian.Uint16(hello[66:68])
		if size >= 128 && size <= 800 {
			return 68, true
		}
	}
	return 0, false
}

func (s *ClientState) AcceptCookie(b []byte) error {
	if len(b) != 41 || b[0] != Cookie {
		return ErrHandshake
	}
	copy(s.Cookie[:], b[1:])
	return nil
}

func MakeCookie(secret []byte, source string, hello []byte, now time.Time) ([]byte, error) {
	cookieOffset, valid := helloCookieOffset(hello)
	if !valid {
		return nil, ErrHandshake
	}
	b := make([]byte, 41)
	b[0] = Cookie
	binary.BigEndian.PutUint64(b[1:9], uint64(now.Unix()/30))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(source))
	mac.Write(hello[1:cookieOffset])
	mac.Write(b[1:9])
	copy(b[9:], mac.Sum(nil))
	return b, nil
}

func CheckCookie(secret []byte, source string, hello []byte, now time.Time) bool {
	cookieOffset, valid := helloCookieOffset(hello)
	if !valid {
		return false
	}
	bucket := binary.BigEndian.Uint64(hello[cookieOffset : cookieOffset+8])
	current := uint64(now.Unix() / 30)
	if bucket > current || current-bucket > 1 {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(source))
	mac.Write(hello[1:cookieOffset])
	mac.Write(hello[cookieOffset : cookieOffset+8])
	return hmac.Equal(mac.Sum(nil), hello[cookieOffset+8:])
}

type ServerState struct {
	Ephemeral    *ecdh.PrivateKey
	Transcript   []byte
	SessionID    uint64
	Profile      byte
	FragmentSize uint16
}

// NewServerHello signs the complete negotiation context and returns ephemeral state.
func NewServerHello(identity ed25519.PrivateKey, hello []byte, sid uint64) ([]byte, *ServerState, error) {
	if _, valid := helloCookieOffset(hello); !valid || sid == 0 {
		return nil, nil, ErrHandshake
	}
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	b := make([]byte, 1+8+32+32+32+64)
	b[0] = ServerHello
	binary.BigEndian.PutUint64(b[1:9], sid)
	if _, err = rand.Read(b[9:41]); err != nil {
		return nil, nil, err
	}
	copy(b[41:73], k.PublicKey().Bytes())
	copy(b[73:105], identity.Public().(ed25519.PublicKey))
	transcript := append([]byte("undertow phase1 direct-dns handshake"), hello...)
	transcript = append(transcript, b[:105]...)
	copy(b[105:], ed25519.Sign(identity, transcript))
	state := &ServerState{Ephemeral: k, Transcript: transcript, SessionID: sid, Profile: hello[65]}
	if state.Profile == AdaptiveProfile {
		state.FragmentSize = binary.BigEndian.Uint16(hello[66:68])
	}
	return b, state, nil
}

func (s *ClientState) VerifyServerHello(b []byte, pinnedFingerprint string) (Keys, error) {
	var zero Keys
	if len(b) != 169 || b[0] != ServerHello {
		return zero, ErrHandshake
	}
	pub := ed25519.PublicKey(b[73:105])
	sum := sha256.Sum256(pub)
	expected, err := hex.DecodeString(strings.ReplaceAll(strings.ToLower(pinnedFingerprint), ":", ""))
	if err != nil || !hmac.Equal(expected, sum[:]) {
		return zero, errors.New("server identity fingerprint mismatch")
	}
	transcript := append([]byte("undertow phase1 direct-dns handshake"), s.Hello()...)
	transcript = append(transcript, b[:105]...)
	if !ed25519.Verify(pub, transcript, b[105:]) {
		return zero, ErrHandshake
	}
	peer, err := ecdh.X25519().NewPublicKey(b[41:73])
	if err != nil {
		return zero, err
	}
	shared, err := s.Ephemeral.ECDH(peer)
	if err != nil {
		return zero, err
	}
	s.Transcript = transcript
	s.SessionID = binary.BigEndian.Uint64(b[1:9])
	return derive(shared, transcript)
}

// ServerHelloFingerprint returns the identity advertised in a server hello.
// The value is untrusted until VerifyServerHello checks its signature, and a
// first-use pin remains vulnerable to interception during that first contact.
func ServerHelloFingerprint(b []byte) (string, error) {
	if len(b) != 169 || b[0] != ServerHello {
		return "", ErrHandshake
	}
	sum := sha256.Sum256(b[73:105])
	return hex.EncodeToString(sum[:]), nil
}

func (s *ServerState) Keys(hello []byte) (Keys, error) {
	var zero Keys
	peer, err := ecdh.X25519().NewPublicKey(hello[33:65])
	if err != nil {
		return zero, err
	}
	shared, err := s.Ephemeral.ECDH(peer)
	if err != nil {
		return zero, err
	}
	return derive(shared, s.Transcript)
}

func derive(shared, transcript []byte) (Keys, error) {
	var keys Keys
	h := sha256.Sum256(transcript)
	b, err := hkdf.Key(sha256.New, shared, h[:], "undertow phase1 directional keys", 72)
	if err != nil {
		return keys, err
	}
	copy(keys.ClientToServer[:], b[:32])
	copy(keys.ServerToClient[:], b[32:64])
	copy(keys.ClientPrefix[:], b[64:68])
	copy(keys.ServerPrefix[:], b[68:72])
	return keys, nil
}

func MakeAuth(token []byte, agentKey ed25519.PrivateKey, transcript []byte) []byte {
	b := make([]byte, 1+32+64+32)
	b[0] = Auth
	pub := agentKey.Public().(ed25519.PublicKey)
	copy(b[1:33], pub)
	copy(b[33:97], ed25519.Sign(agentKey, transcript))
	mac := hmac.New(sha256.New, token)
	mac.Write(transcript)
	mac.Write(pub)
	copy(b[97:], mac.Sum(nil))
	return b
}

func CheckAuth(token, b, transcript []byte) ([16]byte, error) {
	id, err := VerifyAuthSignature(b, transcript)
	if err != nil || !CheckEnrollmentMAC(token, b, transcript) {
		return [16]byte{}, ErrHandshake
	}
	return id, nil
}

func VerifyAuthSignature(b, transcript []byte) ([16]byte, error) {
	var id [16]byte
	if len(b) != 129 || b[0] != Auth {
		return id, ErrHandshake
	}
	pub := ed25519.PublicKey(b[1:33])
	if !ed25519.Verify(pub, transcript, b[33:97]) {
		return id, ErrHandshake
	}
	sum := sha256.Sum256(pub)
	copy(id[:], sum[:16])
	return id, nil
}

func CheckEnrollmentMAC(token, b, transcript []byte) bool {
	if len(b) != 129 || b[0] != Auth {
		return false
	}
	mac := hmac.New(sha256.New, token)
	mac.Write(transcript)
	mac.Write(b[1:33])
	return hmac.Equal(mac.Sum(nil), b[97:])
}

func LoadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("invalid private key %s", path)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, err
		}
		return ed25519.PrivateKey(raw), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return LoadOrCreateKey(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err = f.Write([]byte(hex.EncodeToString(k) + "\n")); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err = f.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	return k, nil
}

func Fingerprint(k ed25519.PrivateKey) string {
	sum := sha256.Sum256(k.Public().(ed25519.PublicKey))
	return hex.EncodeToString(sum[:])
}

func ReadToken(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Windows PowerShell commonly writes redirected text as UTF-16LE.
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		if (len(b)-2)%2 != 0 {
			return nil, errors.New("token file contains incomplete UTF-16LE text")
		}
		text := make([]byte, 0, (len(b)-2)/2)
		for i := 2; i < len(b); i += 2 {
			if b[i+1] != 0 {
				return nil, errors.New("token file must contain hexadecimal text")
			}
			text = append(text, b[i])
		}
		b = text
	} else {
		b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
	}
	t, err := ParseToken(string(b))
	if err != nil {
		return nil, fmt.Errorf("invalid token file %s: expected at least 64 hexadecimal characters from the server's token.key", path)
	}
	return t, nil
}

// ParseToken accepts the same hexadecimal value used by token.key without
// including the secret in validation errors.
func ParseToken(value string) ([]byte, error) {
	token, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil || len(token) < 32 {
		return nil, errors.New("token must contain at least 64 hexadecimal characters")
	}
	return token, nil
}
