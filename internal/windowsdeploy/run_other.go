//go:build !windows

package windowsdeploy

import (
	"context"
	"errors"
	"io"
)

func run(context.Context, string, string, string, string, string, io.Writer) error {
	return errors.New("Windows deployment worker requires Windows")
}
