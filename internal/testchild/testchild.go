// Package testchild starts the processes the tests run.
package testchild

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// Deadline is what a process a test starts may take: a test binary killed
// at its own timeout does not kill its children.
const Deadline = time.Minute

// Command is a command that is killed at Deadline, or when its test ends.
func Command(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), Deadline)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- a test names the command it runs
	cmd.WaitDelay = time.Second

	return cmd
}
