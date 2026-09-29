//go:build !linux && !windows

package icmp

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

func Echo(ctx context.Context, target netip.Addr, data []byte) (time.Duration, error) {
	return 0, errors.New("unprivileged ICMP echo is unavailable on this platform")
}
