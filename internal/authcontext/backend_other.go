//go:build !windows

package authcontext

import (
	"context"
	"errors"
)

type WindowsBackend struct{}

func (WindowsBackend) Discover(context.Context) ([]Token, error) {
	return nil, errors.New("authentication contexts require a Windows agent")
}
func (WindowsBackend) Logon(context.Context, LogonRequest) (Token, error) {
	return nil, errors.New("authentication contexts require a Windows agent")
}
