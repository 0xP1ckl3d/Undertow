//go:build linux

package control

import "os"

func currentPrivilege() string {
	if os.Geteuid() == 0 {
		return "high"
	}
	return "low"
}
