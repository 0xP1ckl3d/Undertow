//go:build !windows

package namedpipe

import (
	"context"
	"errors"
	"net"
)

func Listen(path string) (net.Listener, error) {
	return nil, errors.New("SMB named-pipe relay listeners require a Windows parent agent")
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	return nil, errors.New("SMB named-pipe relay connections require Windows")
}
