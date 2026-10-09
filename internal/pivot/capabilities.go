package pivot

import (
	"fmt"
	"runtime"
	"strings"
)

// Capabilities control the operations implemented by the current agent.
// A false field rejects the matching operation before it starts.
type Capabilities struct {
	TokenContexts   bool
	Pivot           bool
	Exec            bool
	HostOps         bool
	Interactive     bool
	Scripts         bool
	WASM            bool
	Native          bool
	Upload          bool
	Download        bool
	Listeners       bool
	Relay           bool
	JumpCredentials bool
	JumpNTHash      bool
}

type CapabilityReport struct {
	Supported []string `json:"supported"`
	Allowed   []string `json:"allowed"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{TokenContexts: true, Pivot: true, Exec: true, HostOps: true, Interactive: true, Scripts: true, WASM: true, Native: true, Upload: true, Download: true, Listeners: true, Relay: true, JumpCredentials: true, JumpNTHash: true}
}

func ParseDenied(raw string) (Capabilities, error) {
	caps := DefaultCapabilities()
	if strings.TrimSpace(raw) == "" {
		return caps, nil
	}
	for _, item := range strings.Split(raw, ",") {
		switch strings.TrimSpace(item) {
		case "tokens":
			caps.TokenContexts = false
		case "pivot":
			caps.Pivot = false
		case "exec":
			caps.Exec = false
		case "hostops":
			caps.HostOps = false
		case "interactive":
			caps.Interactive = false
		case "scripts":
			caps.Scripts = false
		case "wasm":
			caps.WASM = false
		case "native":
			caps.Native = false
		case "upload":
			caps.Upload = false
		case "download":
			caps.Download = false
		case "listeners":
			caps.Listeners = false
		case "relay":
			caps.Relay = false
		case "jump-credentials":
			caps.JumpCredentials = false
		case "jump-nt-hash":
			caps.JumpNTHash = false
		default:
			return Capabilities{}, fmt.Errorf("unknown agent capability %q; supported: tokens,pivot,exec,hostops,interactive,scripts,wasm,native,upload,download,listeners,relay,jump-credentials,jump-nt-hash", strings.TrimSpace(item))
		}
	}
	return caps, nil
}

func (c Capabilities) Report() CapabilityReport {
	report := CapabilityReport{Supported: []string{"pivot", "exec", "hostops", "interactive", "scripts", "wasm", "native", "upload", "download", "listeners", "relay", "jump-credentials", "jump-nt-hash"}, Allowed: make([]string, 0, 13)}
	for _, item := range []struct {
		name    string
		allowed bool
	}{{"pivot", c.Pivot}, {"exec", c.Exec}, {"hostops", c.HostOps}, {"interactive", c.Interactive}, {"scripts", c.Scripts}, {"wasm", c.WASM}, {"native", c.Native}, {"upload", c.Upload}, {"download", c.Download}, {"listeners", c.Listeners}, {"relay", c.Relay}, {"jump-credentials", c.JumpCredentials}, {"jump-nt-hash", c.JumpNTHash}} {
		if item.allowed {
			report.Allowed = append(report.Allowed, item.name)
		}
	}
	if runtime.GOOS == "windows" {
		report.Supported = append(report.Supported, "tokens")
		if c.TokenContexts {
			report.Allowed = append(report.Allowed, "tokens")
		}
	}
	return report
}

func (r *CapabilityReport) Allows(name string) bool {
	if r == nil || !containsCapability(r.Supported, name) {
		return false
	}
	return containsCapability(r.Allowed, name)
}

func containsCapability(values []string, name string) bool {
	for _, value := range values {
		if value == name {
			return true
		}
	}
	return false
}
