//go:build !windows || !amd64

package pivot

import (
	"context"
	"errors"
)

func executeAssembly(context.Context, []byte, []string, func(byte, []byte) error) (int, error) {
	return -1, errors.New("assembly runtime supports windows-amd64 only")
}
