//go:build linux

package icmp

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

func checksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// Echo uses an unprivileged Linux ICMP datagram socket when ping_group_range
// allows it. Hosts that disable ping sockets return the OS permission error.
func Echo(ctx context.Context, target netip.Addr, data []byte) (time.Duration, error) {
	if !target.Is4() || len(data) == 0 || len(data) > 1400 {
		return 0, errors.New("invalid IPv4 echo request")
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_ICMP)
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			// Some hosts disable unprivileged ping sockets but ship ping with
			// the CAP_NET_RAW capability. Use it only as a reachability check.
			start := time.Now()
			if pingErr := exec.CommandContext(ctx, "ping", "-n", "-c", "1", "-W", "2", target.String()).Run(); pingErr == nil {
				return time.Since(start), nil
			} else {
				return 0, pingErr
			}
		}
		return 0, err
	}
	defer unix.Close(fd)
	deadline := 2 * time.Second
	if d, ok := ctx.Deadline(); ok {
		if remaining := time.Until(d); remaining < deadline {
			deadline = remaining
		}
	}
	if deadline <= 0 {
		return 0, context.DeadlineExceeded
	}
	timeout := unix.NsecToTimeval(deadline.Nanoseconds())
	if err = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		return 0, err
	}
	request := make([]byte, 8+len(data))
	request[0] = 8
	binary.BigEndian.PutUint16(request[6:8], 1)
	copy(request[8:], data)
	binary.BigEndian.PutUint16(request[2:4], checksum(request))
	addr := target.As4()
	start := time.Now()
	if err = unix.Sendto(fd, request, 0, &unix.SockaddrInet4{Addr: addr}); err != nil {
		return 0, err
	}
	buf := make([]byte, 1500)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return 0, err
		}
		if n >= 8 && buf[0] == 0 && buf[1] == 0 && binary.BigEndian.Uint16(buf[6:8]) == 1 {
			return time.Since(start), nil
		}
	}
}
