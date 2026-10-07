//go:build !windows

package pivot

import (
	"bufio"
	"errors"

	"undertow/internal/mux"
)

func serveUploadNTHash(stream *mux.Stream, reader *bufio.Reader, _ FileMessage, _ *WindowsCredential) {
	fileError(stream, reader, errors.New("NT-hash Jump delivery requires a Windows source agent"))
}
