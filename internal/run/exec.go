package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

// ExecSpec configures a single Exec subprocess fork.
type ExecSpec struct {
	// Dir is the working directory; empty inherits the process cwd.
	Dir string
	// Env are per-call overrides as "KEY=value", layered after the sandbox's
	// frozen BaseEnv (or os.Environ when unsandboxed) so later entries win. When
	// empty and sandboxed the child runs under the frozen BaseEnv; when empty and
	// unsandboxed it inherits the process environment. Callers order the slice;
	// Exec does not sort it.
	Env []string
	// Stdin, when non-empty, is fed to the process as standard input (buffered).
	// This is the plumbing under pipe-style chaining: the captured stdout of one
	// call becomes the Stdin of the next.
	Stdin string
	// Capture also buffers stdout/stderr into the result, in addition to
	// streaming through the ctx OutputWriters. Captured text is not trimmed.
	Capture bool
}

// ExecResult is the outcome of Exec.
type ExecResult struct {
	Stdout  string // captured stdout, empty unless ExecSpec.Capture; not trimmed
	Stderr  string // captured stderr, empty unless ExecSpec.Capture; not trimmed
	Code    int    // exit code; -1 when the process was signaled or never started
	Started bool   // whether the process actually started — distinguishes a -1 exit from a start failure
}

// Record renders the result as the {stdout, stderr, code, ok} map that is the
// single shared shape of magus's exec surfaces — os.exec and captured spell
// targets both return it. stdout/stderr are trimmed of surrounding whitespace;
// ok reports a zero exit.
func (r ExecResult) Record() map[string]any {
	return map[string]any{
		"stdout": strings.TrimSpace(r.Stdout),
		"stderr": strings.TrimSpace(r.Stderr),
		"code":   r.Code,
		"ok":     r.Code == 0,
	}
}

// Exec is the shared subprocess primitive behind magus's exec surfaces — os.exec
// and fork spell targets. It applies the sandbox policy from ctx (exec-deny
// check + frozen BaseEnv), layers ExecSpec.Env overrides, streams output through
// the ctx OutputWriters, optionally captures it, and cancels with the same
// graceful signal + WaitDelay as Run. A sandbox exec denial is returned as an
// MGS2007 diagnostic before the process starts (Started=false). The returned
// error is the raw subprocess error (joined with ctx.Err on cancellation);
// callers format their own "exit N" / start-failure messages from ExecResult.
func Exec(ctx context.Context, name string, args []string, spec ExecSpec) (ExecResult, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = spec.Dir
	setCancel(c) // platform-specific graceful cancel; see run_unix.go / run_windows.go
	c.WaitDelay = 5 * time.Second

	policy := sandbox.FromContext(ctx)
	if policy != nil {
		resolved, err := exec.LookPath(name)
		if err != nil {
			resolved = name // let exec.Cmd surface the real lookup error
		}
		if err := policy.CheckExec(resolved); err != nil {
			sandbox.EmitDenyHint("ro", resolved)
			return ExecResult{Code: -1}, types.DiagnosticErrorf(types.ExecDenied, "exec denied: %s", resolved)
		}
	}
	c.Env = childEnv(policy, spec.Env)
	if spec.Stdin != "" {
		c.Stdin = strings.NewReader(spec.Stdin)
	}

	outW, errW := OutputWriters(ctx)
	var outBuf, errBuf bytes.Buffer
	if spec.Capture {
		c.Stdout = io.MultiWriter(outW, &outBuf)
		c.Stderr = io.MultiWriter(errW, &errBuf)
	} else {
		c.Stdout, c.Stderr = outW, errW
	}

	runErr := c.Run()
	if ctx.Err() != nil {
		killGroup(c) // reap grandchildren that ignored the graceful signal
	}

	res := ExecResult{}
	if c.ProcessState != nil {
		res.Started = true
		res.Code = c.ProcessState.ExitCode()
	} else {
		res.Code = -1 // process never started (binary not found, permission denied, etc.)
	}
	if spec.Capture {
		res.Stdout = outBuf.String()
		res.Stderr = errBuf.String()
	}
	// Surface ctx.Err() whenever the run was cancelled — even if the process won
	// the race and exited 0 — so callers can distinguish cancel from a clean
	// finish. errors.Join drops a nil runErr.
	if ctx.Err() != nil {
		runErr = errors.Join(ctx.Err(), runErr)
	}
	return res, runErr
}

// childEnv builds the subprocess environment: the sandbox's frozen BaseEnv (or
// the process environment when unsandboxed), then the magus self-reference vars
// (see SelfVars), then the caller's overrides — later entries win, so a caller may
// still override them explicitly.
func childEnv(policy *sandbox.Policy, overrides []string) []string {
	var base []string
	if policy != nil {
		base = policy.BaseEnv
	}
	root := base
	if root == nil {
		root = os.Environ()
	}
	env := append(slices.Clone(root), SelfVars()...)
	return append(env, overrides...)
}

// SelfVars returns the magus self-reference variables injected into every magus
// subprocess, mirroring GNU Make's exported $(MAKE) and $(MAKELEVEL):
//
//   - MAGUS — the running binary's resolved path. Lets a spell or recipe re-invoke
//     magus reliably (`"$MAGUS" buzz …`) with no dependence on PATH or an install,
//     including under `go run` (the temp build). The sandbox already grants exec on
//     this same resolved path (see sandbox defaults).
//   - MAGUS_LEVEL — the recursion depth this child runs at: the current process's
//     depth (read from its own MAGUS_LEVEL; absent ⇒ 0, the top-level invocation)
//     plus one. A nested magus reads it back as its own depth, so the counter
//     climbs by one per magus process; a recipe can read $MAGUS_LEVEL to tell how
//     deep it is or to guard against runaway recursion.
//
// Exported so the magus.cmd recursion path (which builds its child env by hand)
// injects the same pair.
func SelfVars() []string {
	out := make([]string, 0, 2)
	if exe := magusExe(); exe != "" {
		out = append(out, "MAGUS="+exe)
	}
	out = append(out, "MAGUS_LEVEL="+strconv.Itoa(CurrentLevel()+1))
	return out
}

// CurrentLevel is this process's magus recursion depth, read from MAGUS_LEVEL
// (absent or invalid ⇒ 0, the top-level invocation). A value > 0 means this
// process was spawned by another magus, so it must not stand up its own daemon —
// it forwards adoptable work to, and shares the pool of, the top-level process.
func CurrentLevel() int {
	if n, err := strconv.Atoi(os.Getenv("MAGUS_LEVEL")); err == nil && n >= 0 {
		return n
	}
	return 0
}

// magusExe resolves the running magus binary's path once, following symlinks so it
// matches the sandbox's exec grant. Empty if it cannot be determined.
var magusExe = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
})
