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

func runNTHash(context.Context, string, string, string, string, string, HashCredential, io.Writer) error {
	return errors.New("NT-hash Windows deployment requires Windows")
}
