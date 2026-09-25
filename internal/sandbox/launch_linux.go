//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// Command returns a Cmd that runs name confined to p's filesystem rules and, from
// ABI v6, to its own signal and abstract unix socket scope. A nil p means the
// sandbox is off and returns a plain exec.CommandContext.
//
// The Cmd re-executes the running binary as a launcher, which must call MaybeLaunch
// first in main; the launcher confines itself and execs name with the Cmd's Env,
// Dir and descriptors. The compiled ruleset rides in ExtraFiles, closed in the
// launcher before the exec, so nothing of the policy reaches name. This process's
// copy closes when the Cmd is collected; close cmd.ExtraFiles[0] after Start to
// release it sooner. Callers may set
// Env, Dir, stdio and SysProcAttr, and append to ExtraFiles; replacing ExtraFiles,
// Path or Args makes the launcher refuse to run name. name is resolved against PATH
// now, as exec.Command does.
//
// It returns an error wrapping ErrUnsupported when landlock is unavailable. It does
// not enforce RequiredABI; callers that need a floor check ABI themselves.
func Command(ctx context.Context, p *Policy, name string, args ...string) (*exec.Cmd, error) {
	if p == nil {
		return exec.CommandContext(ctx, name, args...), nil
	}
	abi, err := ABI()
	if err != nil {
		return nil, err
	}
	path := name
	if filepath.Base(name) == name {
		if path, err = exec.LookPath(name); err != nil {
			return nil, err
		}
	}
	rulesetFD, err := buildRuleset(p, abi, handledScopes(abi))
	if err != nil {
		return nil, err
	}

	// /proc/self/exe, not os.Executable: it names the running image even after a
	// rebuild replaces the file on disk, so the launcher always speaks this protocol.
	cmd := exec.CommandContext(ctx, "/proc/self/exe")
	cmd.ExtraFiles = []*os.File{os.NewFile(uintptr(rulesetFD), "landlock-ruleset")}
	// ExtraFiles[0] is descriptor 3 in the child.
	cmd.Args = append([]string{launcherName, "3", path, name}, args...)
	return cmd, nil
}

// launch is the launcher half of Command. args is the ruleset descriptor, the
// resolved path, then the command's argv.
func launch(args []string) error {
	if len(args) < 3 {
		return errors.New("sandbox: launcher: want a ruleset descriptor, a path and an argv")
	}
	rulesetFD, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("sandbox: launcher: ruleset descriptor: %w", err)
	}
	path, argv := args[1], args[2:]

	// Only this thread is confined, and that is enough: it is the one that execs, and
	// execve leaves the new image with this thread alone. Confining every thread
	// instead would fail under cgo and, on kernels without landlock erratum 2,
	// deadlock on the signal scope.
	runtime.LockOSThread()
	if err := restrictSelf(rulesetFD, false); err != nil {
		return err
	}
	if err := unix.Close(rulesetFD); err != nil {
		return fmt.Errorf("sandbox: launcher: close ruleset: %w", err)
	}
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		return fmt.Errorf("sandbox: launcher: exec %s: %w", path, err)
	}
	return nil
}
