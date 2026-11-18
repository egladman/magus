package sandbox

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
)

// RequiredABI is the lowest landlock ABI a Required policy accepts on Linux. Below v3
// the kernel cannot deny truncation, so a confined child could empty any file its user
// owns, rules or not (https://docs.kernel.org/userspace-api/landlock.html#file-truncation-abi-3).
const RequiredABI = 3

// Command returns a Cmd that runs name confined to p's rules. A nil p is a
// *[NilPolicyError]: a caller that means to run a child unconfined says so by not
// calling Command.
//
// Required (the default) on a host that cannot confine the child is an
// *UnconfinedError; BestEffort there returns a plain Cmd after handing that error to
// the policy's [WithUnconfined]. A bare name is resolved against PATH now, as
// exec.Command does.
//
// Callers may set Env, Dir, stdio and SysProcAttr, and append to ExtraFiles: the
// command sees appended entry i at descriptor 3+LauncherFiles+i. Args ends with the
// command's argv. Replacing ExtraFiles, Path or the rest of Args makes the confinement
// refuse to run name.
//
// On Linux the Cmd re-executes the running binary as a launcher named by
// [WithLauncherName], which must call Relaunch first in main; the launcher applies a landlock ruleset to itself and execs
// name, and from ABI v6 also scopes signals and abstract unix sockets to the child. A
// caller may rewrite the argv[0] in Args to change the one the command sees. On macOS
// the Cmd runs /usr/bin/sandbox-exec with a profile generated from the rules (see
// seatbeltProfile), and the command's argv[0] is the PATH-resolved path sandbox-exec
// runs.
func Command(ctx context.Context, p *Policy, name string, args ...string) (*exec.Cmd, error) {
	if p == nil {
		return nil, &NilPolicyError{Op: "Command"}
	}
	if _, err := p.Confines(); err != nil {
		var unconfined *UnconfinedError
		if p.mode != BestEffort || !errors.As(err, &unconfined) {
			return nil, err
		}
		p.unconfined(ctx, err)
		return exec.CommandContext(ctx, name, args...), nil
	}
	path := name
	var err error
	if filepath.Base(name) == name {
		if path, err = exec.LookPath(name); err != nil {
			return nil, err
		}
	}
	return confine(ctx, p, path, name, args)
}

// Relaunch is the launcher half of Command, called with the process's whole argv
// (os.Args) first in main, before any configuration, logging or connection is set up.
// opts must name the launcher as the policies Command ran under do ([WithLauncherName]);
// the rest of a policy's options are ignored here. It reports false at once unless this
// process was started by Command. Then it confines itself and execs the requested
// command and never returns, except with the error that kept it from doing either; the
// caller then exits without running the command (126 is the shell's "found but could
// not execute"). An empty launcher name is an [*OptionError], not handled.
func Relaunch(args []string, opts ...Option) (handled bool, err error) {
	p := &Policy{launcher: defaultLauncherName}
	for _, o := range opts {
		o(p)
	}
	if p.launcher == "" {
		return false, &OptionError{Option: "WithLauncherName", Problem: "the name is empty"}
	}
	if len(args) == 0 || args[0] != p.launcher {
		return false, nil
	}
	return true, launch(args[1:])
}

// Confines reports whether Command confines p's children on this host. A nil p is a
// *[NilPolicyError]. On a host that cannot confine, or below RequiredABI on
// Linux, it is false with an *UnconfinedError whose Mode is p's: a Required p refuses
// there, and a BestEffort p's children run unconfined.
func (p *Policy) Confines() (bool, error) {
	if p == nil {
		return false, &NilPolicyError{Op: "Confines"}
	}
	probe := p.probe
	if probe == nil {
		probe = defaultProbe
	}
	need := requiredLevel
	if p.mode == BestEffort {
		need = 1
	}
	level, err := probe()
	switch {
	case err != nil:
		return false, &UnconfinedError{Mode: p.mode, Err: err}
	case level < need:
		return false, &UnconfinedError{Mode: p.mode, Have: level, Need: need}
	}
	return true, nil
}
