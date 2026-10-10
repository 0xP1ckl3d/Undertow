//go:build windows && amd64

package control

import (
	"context"
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
)

func TestShellPowerLiveSessionThroughInteractiveRelay(t *testing.T) {
	module, err := os.ReadFile(filepath.Join("..", "..", "modules", "native", "shellpower", "shellpower.module"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	serverAgent, agent := forwardAuditAgent(t, ctx, manager, "shellpower-agent", 734, pivot.DefaultCapabilities())
	defer serverAgent.Close()
	defer agent.Close()
	serverVPN, clientVPN := forwardAuditPair(ctx)
	defer serverVPN.Close()
	defer clientVPN.Close()
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeInteractiveRelay(ctx, stream)
	})
	session, err := OpenClientNativeShell(ctx, clientVPN, "shellpower-agent", module, "")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Send([]byte("$relayValue = 41\rWrite-Output ($relayValue + 1)\rexit\r")); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for {
		kind, data, err := session.Read()
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case pivot.InteractiveOutput, pivot.InteractiveStderr:
			output.Write(data)
		case pivot.InteractiveError:
			t.Fatalf("ShellPower relay error: %s; output=%s", data, output.String())
		case pivot.InteractiveExit:
			if len(data) != 4 || int32(binary.BigEndian.Uint32(data)) != 0 || !strings.Contains(output.String(), "42") {
				t.Fatalf("exit=%x output=%s", data, output.String())
			}
			return
		}
	}
}
