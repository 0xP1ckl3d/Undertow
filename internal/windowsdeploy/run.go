package windowsdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

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
