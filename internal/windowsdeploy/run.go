package windowsdeploy

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

type HashCredential struct {
	Username string `json:"username"`
	Domain   string `json:"domain,omitempty"`
	NTHash   string `json:"nt_hash"`
}

// Run executes the agent-side half of one Windows deployment method. The
// artifact has already been streamed and verified before this worker starts.
func Run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 5 {
		return errors.New("invalid Windows deployment worker request")
	}
	method := strings.TrimSpace(args[0])
	target := strings.TrimSpace(args[1])
	path := strings.TrimSpace(args[2])
	executionContext := strings.TrimSpace(args[3])
	shortID := strings.TrimSpace(args[4])
	if target == "" || path == "" || shortID == "" {
		return errors.New("incomplete Windows deployment worker request")
	}
	fmt.Fprintf(output, "Jump method=%s target=%s path=%s\n", method, target, path)
	return run(ctx, method, target, path, executionContext, shortID, output)
}

func RunNTHash(ctx context.Context, args []string, credential HashCredential, output io.Writer) error {
	if len(args) != 5 {
		return errors.New("invalid Windows deployment worker request")
	}
	if credential.Username == "" || credential.NTHash == "" {
		return errors.New("incomplete NT-hash credential")
	}
	decoded, err := hex.DecodeString(credential.NTHash)
	if err != nil || len(decoded) != 16 {
		return errors.New("invalid NT hash")
	}
	method, target, path := strings.TrimSpace(args[0]), strings.TrimSpace(args[1]), strings.TrimSpace(args[2])
	executionContext, shortID := strings.TrimSpace(args[3]), strings.TrimSpace(args[4])
	if target == "" || path == "" || shortID == "" {
		return errors.New("incomplete Windows deployment worker request")
	}
	fmt.Fprintf(output, "Jump method=%s target=%s path=%s authentication=nt-hash\n", method, target, path)
	return runNTHash(ctx, method, target, path, executionContext, shortID, credential, output)
}
