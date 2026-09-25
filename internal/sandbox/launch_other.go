//go:build !linux

package sandbox

import (
	"context"
	"os/exec"
)

// Command returns a plain exec.CommandContext: this platform has no kernel sandbox,
// so the command runs UNCONFINED whatever p says. Callers that must not run
// unconfined check Supported first.
func Command(ctx context.Context, _ *Policy, name string, args ...string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, name, args...), nil
}

// launch is unreachable here: Command never starts a launcher on this platform.
func launch([]string) error {
	return ErrUnsupported
}
