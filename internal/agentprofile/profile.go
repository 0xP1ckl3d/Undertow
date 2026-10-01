package agentprofile

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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

var magic = [16]byte{'U', 'N', 'D', 'E', 'R', 'T', 'O', 'W', '-', 'A', 'G', 'E', 'N', 'T', 1, 0}
var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type Profile struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Created         time.Time    `json:"created"`
	UndertowVersion string       `json:"undertow_version"`
	Config          agent.Config `json:"config"`
}

type Embedded struct {
	Profile         Profile   `json:"profile"`
	ArtifactID      string    `json:"artifact_id"`
	Platform        string    `json:"platform"`
	Architecture    string    `json:"architecture"`
	Created         time.Time `json:"created"`
	UndertowVersion string    `json:"undertow_version"`
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
	if err := e.Profile.Validate(); err != nil {
		return err
	}
	if e.ArtifactID == "" || e.Created.IsZero() || e.Platform == "" || e.Architecture == "" {
		return errors.New("incomplete embedded agent artifact")
	}
	return nil
}

func (e Embedded) Identity() control.ArtifactIdentity {
	return control.ArtifactIdentity{ProfileID: e.Profile.ID, Profile: e.Profile.Name, ArtifactID: e.ArtifactID, UndertowVersion: e.UndertowVersion}
}

// Stamp appends a bounded, hashed profile overlay to an immutable template.
func Stamp(template, destination string, e Embedded) (string, int64, error) {
	if err := e.Validate(); err != nil {
		return "", 0, err
	}
	payload, err := json.Marshal(e)
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
	if stat.Size() < 52 {
		return Embedded{}, errors.New("missing embedded agent profile")
	}
	footer := make([]byte, 52)
	if _, err := f.ReadAt(footer, stat.Size()-52); err != nil {
		return Embedded{}, err
	}
	if !bytes.Equal(footer[36:50], magic[:14]) {
		return Embedded{}, errors.New("missing embedded agent profile")
	}
	if footer[50] != magic[14] {
		return Embedded{}, fmt.Errorf("unsupported embedded agent profile version %d", footer[50])
	}
	if footer[51] != 0 {
		return Embedded{}, errors.New("invalid embedded agent profile footer")
	}
	length := binary.BigEndian.Uint32(footer[32:36])
	if length == 0 || length > MaxProfileSize || int64(length) > stat.Size()-52 {
		return Embedded{}, errors.New("invalid embedded agent profile length")
	}
	payload := make([]byte, length)
	if _, err := f.ReadAt(payload, stat.Size()-52-int64(length)); err != nil {
		return Embedded{}, err
	}
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], footer[:32]) {
		return Embedded{}, errors.New("embedded agent profile integrity check failed")
	}
	var embedded Embedded
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&embedded); err != nil {
		return Embedded{}, fmt.Errorf("invalid embedded agent profile: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Embedded{}, errors.New("embedded agent profile has trailing data")
	}
	if err := embedded.Validate(); err != nil {
		return Embedded{}, err
	}
	return embedded, nil
}
