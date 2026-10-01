//go:build !windows || !amd64

package pivot

import (
	"context"
	"errors"
)

func executeBOFWorker(context.Context, []byte, []byte, func(bool, []byte) error) (int, error) {
	return -1, errors.New("BOF runtime supports Windows AMD64 only")
}
