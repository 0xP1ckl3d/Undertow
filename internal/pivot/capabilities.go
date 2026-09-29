package pivot

import (
	"fmt"
	"strings"
)

// Capabilities control the operations implemented by the current agent.
// A false field rejects the matching operation before it starts.
type Capabilities struct {
	Pivot       bool
	Exec        bool
	HostOps     bool
	Interactive bool
	Scripts     bool
	Upload      bool
	Download    bool
	Listeners   bool
}

type CapabilityReport struct {
	Supported []string `json:"supported"`
	Allowed   []string `json:"allowed"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{Pivot: true, Exec: true, HostOps: true, Interactive: true, Scripts: true, Upload: true, Download: true, Listeners: true}
}

func ParseDenied(raw string) (Capabilities, error) {
	caps := DefaultCapabilities()
	if strings.TrimSpace(raw) == "" {
		return caps, nil
	}
	for _, item := range strings.Split(raw, ",") {
		switch strings.TrimSpace(item) {
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
		case "upload":
			caps.Upload = false
		case "download":
			caps.Download = false
		case "listeners":
			caps.Listeners = false
		default:
			return Capabilities{}, fmt.Errorf("unknown agent capability %q; supported: pivot,exec,hostops,interactive,scripts,upload,download,listeners", strings.TrimSpace(item))
		}
	}
	return caps, nil
}

func (c Capabilities) Report() CapabilityReport {
	report := CapabilityReport{Supported: []string{"pivot", "exec", "hostops", "interactive", "scripts", "upload", "download", "listeners"}, Allowed: make([]string, 0, 8)}
	for _, item := range []struct {
		name    string
		allowed bool
	}{{"pivot", c.Pivot}, {"exec", c.Exec}, {"hostops", c.HostOps}, {"interactive", c.Interactive}, {"scripts", c.Scripts}, {"upload", c.Upload}, {"download", c.Download}, {"listeners", c.Listeners}} {
		if item.allowed {
			report.Allowed = append(report.Allowed, item.name)
		}
	}
	return report
}
