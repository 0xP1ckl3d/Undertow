package agentprofile

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"undertow/internal/agent"
	"undertow/internal/control"
)

const MaxProfileSize = 16 << 10
const EmbeddedFormatVersion uint32 = 4

var magic = [8]byte{0xd4, 0x93, 0xa8, 0x5f, 0x6c, 4, 0x0b, 0xe2}
var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type Profile struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Created         time.Time    `json:"created"`
	UndertowVersion string       `json:"undertow_version"`
	Config          agent.Config `json:"config"`
}

type Embedded struct {
	ProfileID  string
	ArtifactID string
	Config     agent.Config
}

func ID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (p Profile) Validate() error {
	if !safeName.MatchString(p.Name) {
		return errors.New("profile name must contain only letters, digits, dot, underscore, or hyphen")
	}
	if p.ID == "" || len(p.ID) > 64 {
		return errors.New("invalid profile ID")
	}
	if p.Created.IsZero() {
		return errors.New("profile creation time is required")
	}
	return p.Config.Validate()
}

func (e Embedded) Validate() error {
	if e.ProfileID == "" || len(e.ProfileID) > 64 || e.ArtifactID == "" || len(e.ArtifactID) > 64 {
		return errors.New("incomplete embedded agent artifact")
	}
	if e.Config.AuthMode != "artifact" {
		return errors.New("embedded authentication mode must be artifact-scoped")
	}
	if err := e.Config.Validate(); err != nil {
		return err
	}
	return nil
}

func (e Embedded) Identity() control.ArtifactIdentity {
	return control.ArtifactIdentity{ProfileID: e.ProfileID, ArtifactID: e.ArtifactID}
}

// Stamp appends a bounded, hashed profile overlay to an immutable template.
func Stamp(template, destination string, e Embedded) (string, int64, error) {
	if err := e.Validate(); err != nil {
		return "", 0, err
	}
	payload, err := encodeEmbedded(e)
	if err != nil {
		return "", 0, err
	}
	if len(payload) > MaxProfileSize {
		return "", 0, errors.New("embedded agent profile exceeds 16 KiB")
	}
	in, err := os.Open(template)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(destination)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(payload)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	for _, part := range [][]byte{payload, sum[:], length[:], magic[:]} {
		if _, err = out.Write(part); err != nil {
			return "", 0, err
		}
		_, _ = h.Write(part)
		n += int64(len(part))
	}
	if err = out.Sync(); err != nil {
		return "", 0, err
	}
	if err = out.Close(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func Read(path string) (Embedded, error) {
	f, err := os.Open(path)
	if err != nil {
		return Embedded{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return Embedded{}, err
	}
	if stat.Size() < 44 {
		return Embedded{}, errors.New("missing embedded configuration")
	}
	footer := make([]byte, 44)
	if _, err := f.ReadAt(footer, stat.Size()-44); err != nil {
		return Embedded{}, err
	}
	if !bytes.Equal(footer[36:41], magic[:5]) {
		return Embedded{}, errors.New("missing embedded configuration")
	}
	if footer[41] != magic[5] {
		return Embedded{}, fmt.Errorf("unsupported embedded format version %d", footer[41])
	}
	if !bytes.Equal(footer[42:44], magic[6:]) {
		return Embedded{}, errors.New("invalid embedded footer")
	}
	length := binary.BigEndian.Uint32(footer[32:36])
	if length == 0 || length > MaxProfileSize || int64(length) > stat.Size()-44 {
		return Embedded{}, errors.New("invalid embedded length")
	}
	payload := make([]byte, length)
	if _, err := f.ReadAt(payload, stat.Size()-44-int64(length)); err != nil {
		return Embedded{}, err
	}
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], footer[:32]) {
		return Embedded{}, errors.New("embedded integrity check failed")
	}
	embedded, err := decodeEmbedded(payload)
	if err != nil {
		return Embedded{}, fmt.Errorf("invalid embedded configuration: %w", err)
	}
	if err := embedded.Validate(); err != nil {
		return Embedded{}, err
	}
	return embedded, nil
}
