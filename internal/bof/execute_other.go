//go:build !windows || !amd64

package bof

import (
	"context"
	"errors"
)

func Execute(context.Context,[]byte,[]byte,func(bool,[]byte)error)(int,error){ return -1,errors.New("BOF runtime supports Windows AMD64 only") }
