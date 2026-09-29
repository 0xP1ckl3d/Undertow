//go:build !linux && !windows

package netstack

import (
	"context"
	"errors"
	"io"
	"net/netip"

	"undertow/internal/mux"
)

type Selector func(destination netip.Addr) *mux.Mux

func Serve(ctx context.Context, device io.ReadWriteCloser, proxyAddress netip.Prefix, choose Selector) error {
	return errors.New("proxy netstack is currently supported on Linux only")
}
