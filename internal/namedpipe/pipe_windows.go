//go:build windows

package namedpipe

import (
	"context"
	"net"

	winio "github.com/Microsoft/go-winio"
)

func Listen(path string) (net.Listener, error) {
	if err := ValidateLocal(path); err != nil {
		return nil, err
	}
	// Remote SMB clients must authenticate to Windows before they can open the
	// pipe. Undertow then performs its own end-to-end server authentication and
	// enrollment handshake over the byte stream.
	return winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)"})
}

func Dial(ctx context.Context, path string) (net.Conn, error) {
	if err := ValidateRemote(path); err != nil {
		return nil, err
	}
	return winio.DialPipeContext(ctx, path)
}
