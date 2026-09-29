//go:build linux

package pivot

import (
	"fmt"
	"os"
	"strconv"
)

func setDownloadOwner(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	rawUID, rawGID := os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID")
	if rawUID == "" || rawGID == "" {
		return nil
	}
	uid, uidErr := strconv.Atoi(rawUID)
	gid, gidErr := strconv.Atoi(rawGID)
	if uidErr != nil || gidErr != nil || uid < 0 || gid < 0 {
		return fmt.Errorf("invalid sudo owner for downloaded file")
	}
	return os.Chown(path, uid, gid)
}
