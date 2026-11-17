//go:build windows

package sandbox

import (
	"context"
	"errors"
	"os/exec"
)

// LauncherFiles is how many ExtraFiles a confining Cmd from Command carries ahead of
// any a caller appends: none, since Windows has no kernel sandbox to confine one with.
const LauncherFiles = 0

// requiredLevel is never reached: defaultProbe always fails here.
const requiredLevel = 1

// defaultProbe is an *UnsupportedError: Windows has no kernel sandbox.
func defaultProbe() (int, error) { return 0, &UnsupportedError{} }

// confine refuses: Confines reports false here, so Command reaches it only through a
// WithKernelProbe that claims a sandbox the host does not have.
func confine(context.Context, *Policy, string, string, []string) (*exec.Cmd, error) {
	return nil, &UnsupportedError{}
}

// launch is unreachable here: Command never starts a launcher on this platform.
func launch([]string) error {
	return errors.New("sandbox: launcher: Windows has no kernel sandbox")
}
