//go:build !windows || !amd64

package pivot

import (
	"context"
	"errors"
)

func executeNativeShell(context.Context, []byte, <-chan []byte, func(byte, []byte) error) (int, error) {
	return -1, errors.New("native live shell modules require a Windows amd64 agent")
}
