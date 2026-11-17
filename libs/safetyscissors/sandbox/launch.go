package sandbox

import (
	"context"
	"os/exec"
	"path/filepath"
)

// RequiredABI is the lowest landlock ABI a Required policy accepts on Linux. Below v3
// the kernel cannot deny truncation, so a confined child could empty any file its user
// owns, rules or not (https://docs.kernel.org/userspace-api/landlock.html#file-truncation-abi-3).
const RequiredABI = 3

// launcherName is argv[0] of a process Command starts on Linux. argv[0] is never
// parsed as a verb, so the launcher adds nothing a user can type.
const launcherName = "magus-sandbox-launch"

// Command returns a Cmd that runs name confined to p's rules. A nil p returns a plain
// exec.CommandContext.
//
// Required (the default) on a host that cannot confine the child is an
// *UnconfinedError; BestEffort there returns a plain Cmd, so a caller that must know
// which it got asks Confines. A bare name is resolved against PATH now, as exec.Command
// does.
//
// Callers may set Env, Dir, stdio and SysProcAttr, and append to ExtraFiles: the
// command sees appended entry i at descriptor 3+LauncherFiles+i. Args ends with the
// command's argv. Replacing ExtraFiles, Path or the rest of Args makes the confinement
// refuse to run name.
//
// On Linux the Cmd re-executes the running binary as a launcher, which must call
// Relaunch first in main; the launcher applies a landlock ruleset to itself and execs
// name, and from ABI v6 also scopes signals and abstract unix sockets to the child. A
// caller may rewrite the argv[0] in Args to change the one the command sees. On macOS
// the Cmd runs /usr/bin/sandbox-exec with a profile generated from the rules (see
// seatbeltProfile), and the command's argv[0] is the resolved path sandbox-exec runs.
func Command(ctx context.Context, p *Policy, name string, args ...string) (*exec.Cmd, error) {
	if p == nil {
		return exec.CommandContext(ctx, name, args...), nil
	}
	ok, err := p.Confines()
	if err != nil {
		return nil, err
	}
	if !ok {
		return exec.CommandContext(ctx, name, args...), nil
	}
	path := name
	if filepath.Base(name) == name {
		if path, err = exec.LookPath(name); err != nil {
			return nil, err
		}
	}
	return confine(ctx, p, path, name, args)
}

// Relaunch is the launcher half of Command, called with the process's whole argv
// (os.Args) first in main, before any configuration, logging or connection is set up.
// It reports false at once unless this process was started by Command. Then it
// confines itself and execs the requested command and never returns, except with the
// error that kept it from doing either; the caller then exits without running the
// command (126 is the shell's "found but could not execute").
func Relaunch(args []string) (handled bool, err error) {
	if len(args) == 0 || args[0] != launcherName {
		return false, nil
	}
	return true, launch(args[1:])
}

// Confines reports whether Command confines p's children on this host. It is false
// with a nil error for a nil p and for a BestEffort p on a host that cannot confine.
// A Required p on such a host, or below RequiredABI on Linux, is an *UnconfinedError.
func (p *Policy) Confines() (bool, error) {
	if p == nil {
		return false, nil
	}
	probe := p.probe
	if probe == nil {
		probe = defaultProbe
	}
	level, err := probe()
	if p.mode == BestEffort {
		return err == nil && level >= 1, nil
	}
	switch {
	case err != nil:
		return false, &UnconfinedError{Err: err}
	case level < requiredLevel:
		return false, &UnconfinedError{Have: level, Need: requiredLevel}
	}
	return true, nil
}
