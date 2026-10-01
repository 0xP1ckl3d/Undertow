//go:build !windows || !amd64

package pivot

import (
	"context"
	"errors"
)

func executeNative(context.Context, []byte, []byte, func(byte, []byte) error) (int, error) {
	return -1, errors.New("native runtime supports windows-amd64 only")
}
