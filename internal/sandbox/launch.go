package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/egladman/magus/internal/log/attr"
	scissors "github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// launcherName is argv[0] of the launcher scissors.Command starts on Linux, so a process
// listing names magus.
const launcherName = "magus-sandbox-launch"

// launchFailedExit is the launcher's exit status when it could not confine itself
// or exec the command: the shell's "found but could not execute".
const launchFailedExit = 126

// MaybeLaunch returns at once unless this process was started by Command. Then it
// confines itself and execs the requested command, never returning: on failure it
// writes the reason to stderr and exits 126 without running the command. Call it
// first in main, before any configuration, logging or connection is set up.
func MaybeLaunch() {
	handled, err := scissors.Relaunch(os.Args, scissors.WithLauncherName(launcherName))
	if !handled {
		return
	}
	fmt.Fprintf(os.Stderr, "magus: confined launch failed: %v\n", err)
	os.Exit(launchFailedExit)
}

// Command returns a Cmd that runs name confined by the kernel to p's filesystem rules,
// through scissors.Command. A nil p means the sandbox is off and returns a plain
// exec.CommandContext.
//
// magus confines with landlock alone: on a host without it, macOS included, Command
// is the error ABI gave (a *scissors.UnsupportedError), and the caller runs the child
// under the binding checks or refuses it (KernelConfines). A required p below
// RequiredABI is the module's *scissors.UnconfinedError; KernelConfines refuses it
// first, as MGS2012.
func Command(ctx context.Context, p *Policy, name string, args ...string) (*exec.Cmd, error) {
	if p == nil {
		return exec.CommandContext(ctx, name, args...), nil
	}
	if _, err := p.kernelABI(); err != nil {
		return nil, err
	}
	confined, err := p.confinement()
	if err != nil {
		return nil, err
	}
	return scissors.Command(ctx, confined, name, args...)
}

// kernelABI is the running kernel's landlock ABI, or the probe a test gave p.
func (p *Policy) kernelABI() (int, error) {
	if p.probe != nil {
		return p.probe()
	}
	return scissors.ABI()
}

// confinement is p's rules as the module policy Command starts children under: the
// one built with p, or for a policy assembled by hand, one built now.
func (p *Policy) confinement() (*scissors.Policy, error) {
	if p.confined != nil || p.confinedErr != nil {
		return p.confined, p.confinedErr
	}
	return newConfinement(p)
}

// withConfinement builds p's module policy from its rules once, where p is built,
// so a link planted at a rule path after that is never followed by a later Command.
func (p *Policy) withConfinement() *Policy {
	p.confined, p.confinedErr = newConfinement(p)
	return p
}

// newConfinement converts p's rules into module binds under p's mode and lease.
func newConfinement(p *Policy) (*scissors.Policy, error) {
	binds := make([]scissors.Bind, 0, len(p.FS.Rules))
	for _, r := range p.FS.Rules {
		mode, err := bindMode(r)
		if err != nil {
			return nil, err
		}
		binds = append(binds, scissors.Bind{Path: r.Path, Mode: mode, Create: r.Create})
	}
	opts := []scissors.Option{scissors.WithLease(p.Lease, p.LeaseFrom), scissors.WithLauncherName(launcherName)}
	switch p.Mode.Resolved() {
	case types.SandboxModeRequired:
	case types.SandboxModeBestEffort, types.SandboxModeOff:
		// Off reaches here only on a policy assembled by hand: a workspace whose mode is
		// off attaches none.
		opts = append(opts, scissors.WithMode(scissors.BestEffort), scissors.WithUnconfined(warnUnconfined))
	}
	if p.probe != nil {
		opts = append(opts, scissors.WithKernelProbe(p.probe))
	}
	return scissors.New(binds, opts...)
}

// warnUnconfined says that a best-effort child runs without the kernel layer, and why.
func warnUnconfined(ctx context.Context, err error) {
	slog.With(attr.Component("magus")).WarnContext(ctx, "a best-effort sandbox started a child unconfined", "reason", err.Error())
}

// bindMode spells r's flags as a bind mode. Every rule magus builds reads; one that
// grants write or exec without read has no mode to spell it.
func bindMode(r filesystem.Rule) (string, error) {
	switch {
	case !r.Read && (r.Write || r.Exec):
		return "", fmt.Errorf("sandbox: the rule on %s grants write or exec without read, which no bind mode spells", r.Path)
	case r.Write && r.Exec:
		return "rwx", nil
	case r.Write:
		return "rw", nil
	case r.Exec:
		return "rx", nil
	}
	return "ro", nil
}

// KernelConfines reports whether p's children run under the kernel layer, which they
// do wherever the host has landlock. It is false with a nil error for a nil p and for
// a best-effort p on a host without landlock, whose children then run under the
// binding checks and the env allowlist alone. A required p on a host below
// RequiredABI is an MGS2012 error.
func (p *Policy) KernelConfines() (bool, error) {
	if p == nil {
		return false, nil
	}
	abi, err := p.kernelABI()
	if p.Mode.Resolved() != types.SandboxModeRequired {
		return err == nil && abi >= 1, nil
	}
	switch {
	case err != nil:
		return false, ErrorRequired(p.Workspace, "the kernel cannot confine its children: "+err.Error())
	case abi < scissors.RequiredABI:
		return false, ErrorRequired(p.Workspace, fmt.Sprintf("the kernel's landlock ABI is %d, below the %d it needs", abi, scissors.RequiredABI))
	}
	return true, nil
}

// ErrorRequired returns the MGS2012 refusal of a required sandbox for the workspace at
// root, why saying what stands in the way. root may be empty.
func ErrorRequired(root, why string) error {
	subject := "sandbox mode is required"
	if root != "" {
		subject += " for " + root
	}
	return types.DiagnosticErrorf(types.SandboxRequired,
		"%s, and %s; run it on Linux 6.2 or newer with landlock enabled (/sys/kernel/security/landlock)", subject, why)
}
