package pivot

import (
	"fmt"
	"strings"
)

// Capabilities control the operations implemented by the current agent.
// A false field rejects the matching operation before it starts.
type Capabilities struct {
	Pivot    bool
	Exec     bool
	Upload   bool
	Download bool
}

type CapabilityReport struct {
	Supported []string `json:"supported"`
	Allowed   []string `json:"allowed"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{Pivot: true, Exec: true, Upload: true, Download: true}
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
		case "upload":
			caps.Upload = false
		case "download":
			caps.Download = false
		default:
			return Capabilities{}, fmt.Errorf("unknown agent capability %q; supported: pivot,exec,upload,download", strings.TrimSpace(item))
		}
	}
	return caps, nil
}

func (c Capabilities) Report() CapabilityReport {
	report := CapabilityReport{Supported: []string{"pivot", "exec", "upload", "download"}, Allowed: make([]string, 0, 4)}
	for _, item := range []struct {
		name    string
		allowed bool
	}{{"pivot", c.Pivot}, {"exec", c.Exec}, {"upload", c.Upload}, {"download", c.Download}} {
		if item.allowed {
			report.Allowed = append(report.Allowed, item.name)
		}
	}
	return report
}
