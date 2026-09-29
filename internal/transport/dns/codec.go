package dns

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Private-use EDNS option. Only direct UDP/53 peers interpret its contents.
const optionCode = 65001
const maxDNS = 1232

var ErrMalformed = errors.New("malformed undertow DNS message")

type Message struct {
	ID       uint16
	Name     string
	Response bool
	Payload  []byte
	RCode    uint8
}

func RandomName(domain string) (string, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]) + "." + strings.TrimSuffix(domain, ".") + ".", nil
}

func Encode(m Message) ([]byte, error) {
	if len(m.Payload) > 1050 {
		return nil, fmt.Errorf("DNS payload too large: %d", len(m.Payload))
	}
	name, err := encodeName(m.Name)
	if err != nil {
		return nil, err
	}
	n := 12 + len(name) + 4 + 11 + 4 + len(m.Payload)
	if n > maxDNS {
		return nil, fmt.Errorf("DNS message too large: %d", n)
	}
	b := make([]byte, n)
	binary.BigEndian.PutUint16(b[0:2], m.ID)
	if m.Response {
		b[2] = 0x84
		b[3] = m.RCode & 15
	} else {
		b[2] = 0x01
	}
	binary.BigEndian.PutUint16(b[4:6], 1)
	binary.BigEndian.PutUint16(b[10:12], 1)
	o := 12
	copy(b[o:], name)
	o += len(name)
	binary.BigEndian.PutUint16(b[o:o+2], 16)
	o += 2 // TXT
	binary.BigEndian.PutUint16(b[o:o+2], 1)
	o += 2 // IN
	b[o] = 0
	o++ // OPT root name
	binary.BigEndian.PutUint16(b[o:o+2], 41)
	o += 2
	binary.BigEndian.PutUint16(b[o:o+2], maxDNS)
	o += 2
	o += 4 // extended RCODE, version, flags
	binary.BigEndian.PutUint16(b[o:o+2], uint16(4+len(m.Payload)))
	o += 2
	binary.BigEndian.PutUint16(b[o:o+2], optionCode)
	o += 2
	binary.BigEndian.PutUint16(b[o:o+2], uint16(len(m.Payload)))
	o += 2
	copy(b[o:], m.Payload)
	return b, nil
}

func Decode(b []byte) (Message, error) {
	return decode(b, true)
}

// decode with copyPayload=false borrows the caller's packet buffer. Callers
// must finish processing the payload before reusing that buffer.
func decode(b []byte, copyPayload bool) (Message, error) {
	var m Message
	if len(b) < 12 || len(b) > maxDNS {
		return m, ErrMalformed
	}
	m.ID = binary.BigEndian.Uint16(b[:2])
	m.Response = b[2]&0x80 != 0
	m.RCode = b[3] & 15
	if b[2]&0x78 != 0 || binary.BigEndian.Uint16(b[4:6]) != 1 || binary.BigEndian.Uint16(b[6:8]) != 0 || binary.BigEndian.Uint16(b[8:10]) != 0 || binary.BigEndian.Uint16(b[10:12]) != 1 {
		return m, ErrMalformed
	}
	name, o, err := decodeName(b, 12)
	if err != nil || o+4 > len(b) {
		return m, ErrMalformed
	}
	m.Name = name
	if binary.BigEndian.Uint16(b[o:o+2]) != 16 || binary.BigEndian.Uint16(b[o+2:o+4]) != 1 {
		return m, ErrMalformed
	}
	o += 4
	if o+11 > len(b) || b[o] != 0 || binary.BigEndian.Uint16(b[o+1:o+3]) != 41 {
		return m, ErrMalformed
	}
	o += 9
	rdlen := int(binary.BigEndian.Uint16(b[o : o+2]))
	o += 2
	if o+rdlen != len(b) || rdlen < 4 || binary.BigEndian.Uint16(b[o:o+2]) != optionCode || int(binary.BigEndian.Uint16(b[o+2:o+4])) != rdlen-4 {
		return m, ErrMalformed
	}
	m.Payload = b[o+4:]
	if copyPayload {
		m.Payload = append([]byte(nil), m.Payload...)
	}
	return m, nil
}

func encodeName(name string) ([]byte, error) {
	if !strings.HasSuffix(name, ".") || len(name) > 254 {
		return nil, ErrMalformed
	}
	parts := strings.Split(strings.TrimSuffix(name, "."), ".")
	b := make([]byte, 0, len(name)+1)
	for _, p := range parts {
		if len(p) == 0 || len(p) > 63 {
			return nil, ErrMalformed
		}
		b = append(b, byte(len(p)))
		for i := 0; i < len(p); i++ {
			c := p[i]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return nil, ErrMalformed
			}
			b = append(b, c)
		}
	}
	return append(b, 0), nil
}

func decodeName(b []byte, o int) (string, int, error) {
	var parts []string
	start := o
	for {
		if o >= len(b) || o-start > 254 {
			return "", 0, ErrMalformed
		}
		l := int(b[o])
		o++
		if l == 0 {
			break
		}
		if l > 63 || o+l > len(b) {
			return "", 0, ErrMalformed
		}
		for _, c := range b[o : o+l] {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return "", 0, ErrMalformed
			}
		}
		parts = append(parts, strings.ToLower(string(b[o:o+l])))
		o += l
	}
	if len(parts) == 0 {
		return "", 0, ErrMalformed
	}
	return strings.Join(parts, ".") + ".", o, nil
}
