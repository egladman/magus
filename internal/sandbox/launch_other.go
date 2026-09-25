//go:build !linux

package sandbox

import (
	"context"
	"os/exec"
)

// Command returns a plain exec.CommandContext for a nil p and ErrUnsupported for any
// other: this platform has no kernel sandbox to confine the command with.
func Command(ctx context.Context, p *Policy, name string, args ...string) (*exec.Cmd, error) {
	if p != nil {
		return nil, ErrUnsupported
	}
	return exec.CommandContext(ctx, name, args...), nil
}

// launch is unreachable here: Command never starts a launcher on this platform.
func launch([]string) error {
	return ErrUnsupported
}
