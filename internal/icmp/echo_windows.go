//go:build windows

package icmp

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var iphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")
var icmpCreate = iphlpapi.NewProc("IcmpCreateFile")
var icmpClose = iphlpapi.NewProc("IcmpCloseHandle")
var icmpSend = iphlpapi.NewProc("IcmpSendEcho")

// Echo uses Windows' ICMP API, which does not require a raw socket on the agent.
func Echo(ctx context.Context, target netip.Addr, data []byte) (time.Duration, error) {
	if !target.Is4() || len(data) == 0 || len(data) > 1400 {
		return 0, errors.New("invalid IPv4 echo request")
	}
	deadline := 2 * time.Second
	if d, ok := ctx.Deadline(); ok {
		if remaining := time.Until(d); remaining < deadline {
			deadline = remaining
		}
	}
	if deadline <= 0 {
		return 0, context.DeadlineExceeded
	}
	handle, _, callErr := icmpCreate.Call()
	if handle == ^uintptr(0) {
		return 0, callErr
	}
	defer icmpClose.Call(handle)
	addr := target.As4()
	reply := make([]byte, 64+len(data)+8)
	start := time.Now()
	count, _, callErr := icmpSend.Call(handle, uintptr(binary.LittleEndian.Uint32(addr[:])), uintptr(unsafe.Pointer(&data[0])), uintptr(uint16(len(data))), 0, uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(deadline.Milliseconds()))
	if count == 0 {
		return 0, callErr
	}
	if status := binary.LittleEndian.Uint32(reply[4:8]); status != 0 {
		return 0, errors.New("ICMP echo failed")
	}
	return time.Since(start), nil
}
