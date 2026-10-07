//go:build !windows

package pivot

import "errors"

func withWindowsCredential(credential *WindowsCredential, action func()) error {
	if credential != nil {
		return errors.New("supplied Windows credentials require a Windows source agent")
	}
	action()
	return nil
}
