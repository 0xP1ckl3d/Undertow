//go:build !windows

package pivot

import "errors"

func listScreens() ([]ScreenInfo, error) {
	return nil, errors.New("screen capture is available on Windows agents")
}

func captureScreen(int) ([]byte, error) {
	return nil, errors.New("screen capture is available on Windows agents")
}
