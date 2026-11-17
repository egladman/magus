//go:build darwin

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// LauncherFiles is how many ExtraFiles a confining Cmd from Command carries ahead of
// any a caller appends: none on macOS, where the profile rides in argv.
const LauncherFiles = 0

// sandboxExec runs a command under an SBPL profile. It is deprecated in the macOS
// man page and still ships; there is no cgo-free replacement.
const sandboxExec = "/usr/bin/sandbox-exec"

// requiredLevel is the level defaultProbe reports where sandbox-exec exists.
const requiredLevel = 1

// defaultProbe is 1 where sandbox-exec exists, an *UnsupportedError where it does not.
func defaultProbe() (int, error) {
	if _, err := os.Stat(sandboxExec); err != nil {
		return 0, &UnsupportedError{Err: err}
	}
	return 1, nil
}

// confine runs path under sandbox-exec with p's profile. sandbox-exec execs path
// itself, so the command's argv[0] is path and the caller's name is not used.
func confine(ctx context.Context, p *Policy, path, _ string, args []string) (*exec.Cmd, error) {
	argv := append([]string{"-p", seatbeltProfile(p.rules.Rules), path}, args...)
	return exec.CommandContext(ctx, sandboxExec, argv...), nil
}

// launch is unreachable here: Command never starts a launcher on macOS.
func launch([]string) error {
	return errors.New("sandbox: launcher: macOS confines through sandbox-exec, never a launcher")
}
