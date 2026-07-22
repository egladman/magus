package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

// ExecOptions configures a single Exec subprocess fork.
type ExecOptions struct {
	// Dir is the working directory; empty inherits the process cwd.
	Dir string
	// Env are per-call overrides as "KEY=value", layered after the sandbox's
	// frozen BaseEnv (or os.Environ when unsandboxed) so later entries win. Empty
	// runs under the frozen BaseEnv when sandboxed, or the process environment when
	// not. Callers order the slice; Exec does not sort it.
	Env []string
	// Stdin, when non-empty, is fed to the process as standard input (buffered).
	// This is the plumbing under pipe-style chaining: one call's captured stdout
	// becomes the next call's Stdin.
	Stdin string
	// Capture also buffers stdout/stderr into the result, on top of streaming
	// through the ctx OutputWriters. Captured text is not trimmed.
	Capture bool
	// Quiet suppresses live streaming to the ctx OutputWriters. Pair it with
	// Capture to read output without echoing it (e.g. stdout captured into a
	// variable and written to a file); without Capture the output is discarded.
	Quiet bool
}

// ExecResult is the outcome of Exec.
type ExecResult struct {
	Stdout  string // captured stdout, empty unless ExecOptions.Capture; not trimmed
	Stderr  string // captured stderr, empty unless ExecOptions.Capture; not trimmed
	Code    int    // exit code; -1 when the process was signaled or never started
	Started bool   // whether the process actually started; distinguishes a -1 exit from a start failure
}

// ToMap renders the result as the {stdout, stderr, code, ok} map that is the
// single shared shape of magus's exec surfaces (os.exec and captured spell
// targets both return it). stdout/stderr are trimmed of surrounding whitespace;
// ok reports a zero exit.
func (r ExecResult) ToMap() map[string]any {
	return map[string]any{
		"stdout": strings.TrimSpace(r.Stdout),
		"stderr": strings.TrimSpace(r.Stderr),
		"code":   r.Code,
		"ok":     r.Code == 0,
	}
}

// Exec is the shared subprocess primitive behind magus's exec surfaces (os.exec
// and fork spell targets). It applies the sandbox policy from ctx (exec-deny
// check plus frozen BaseEnv), layers ExecOptions.Env overrides, streams output
// through the ctx OutputWriters, optionally captures it, and cancels with the
// same graceful signal and WaitDelay as Run. A sandbox exec denial returns an
// MGS2007 diagnostic before the process starts (Started=false). The returned
// error is the raw subprocess error (joined with ctx.Err on cancellation);
// callers format their own "exit N" / start-failure messages from ExecResult.
func Exec(ctx context.Context, name string, args []string, opts ExecOptions) (ExecResult, error) {
	if types.Tracing(ctx) {
		// A dry run skips execution and returns a benign success, tracing the command
		// at info so the planned command shows without -v; a normal run logs it at
		// debug below, after the sandbox check, then executes it.
		slog.InfoContext(ctx, "run.exec", "cmd", name, "args", args, "dir", opts.Dir)
		return ExecResult{Started: true, Code: 0}, nil
	}
	// Emit a structured exec event (the command about to run) when inside a captured target
	// step, so the log viewer groups the output that follows under its command without
	// parsing a "$ cmd" echo out of the text. Only fires within a step, not internal probes.
	if project, target, ok := journal.StepFromContext(ctx); ok {
		journal.Emit(ctx, journal.Event{
			Kind: journal.KindExec, Project: project, Target: target, Text: commandLine(name, args),
		})
	}
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = opts.Dir
	setCancel(c) // platform-specific graceful cancel; see run_unix.go / run_windows.go
	c.WaitDelay = 5 * time.Second

	policy := sandbox.FromContext(ctx)
	if policy != nil {
		resolved, err := exec.LookPath(name)
		if err != nil {
			resolved = name // let exec.Cmd surface the real lookup error
		}
		if err := policy.CheckExecCtx(ctx, resolved); err != nil {
			sandbox.EmitDenyHint("ro", resolved)
			return ExecResult{Code: -1}, types.DiagnosticErrorf(types.ExecDenied, "exec denied: %s", resolved)
		}
	}
	env, withheldDaemon := childEnv(policy, opts.Env)
	c.Env = env
	// Count the env vars this sandbox withheld from the child (magus's own allowlist
	// scrub, not a kernel action); attributed to the current step's project.
	sandbox.RecordEnvDropped(ctx, policy)
	if len(withheldDaemon) > 0 {
		// Transparency breadcrumb. magus withholds its OWN daemon/pool pointers from every op
		// subprocess: they are unauthenticated (MGS2008) and their mere presence makes a program
		// that links proc - including magus's own binaries - believe it is adopted under a parent.
		// This is INDEPENDENT of sandbox.enabled, so it is the answer to the otherwise-baffling
		// "the sandbox is off, why is MAGUS_DAEMON_SOCKET missing in my recipe/test?". Debug, not
		// Info: it fires on most ops during a run, so reach for it with -v when chasing a missing
		// var. A nested magus (runMagus) re-injects these, so they never show as withheld there.
		slog.DebugContext(ctx, types.FormatDiagnostic(types.DaemonSocketWithheld,
			"withheld magus daemon pointer(s) from op subprocess (done regardless of sandbox.enabled)"),
			"vars", withheldDaemon)
	}
	if opts.Stdin != "" {
		c.Stdin = strings.NewReader(opts.Stdin)
	}

	outW, errW := OutputWriters(ctx)
	if opts.Quiet {
		outW, errW = io.Discard, io.Discard // capture-only / no live streaming
	}
	var outBuf, errBuf bytes.Buffer
	if opts.Capture {
		c.Stdout = io.MultiWriter(outW, &outBuf)
		c.Stderr = io.MultiWriter(errW, &errBuf)
	} else {
		c.Stdout, c.Stderr = outW, errW
	}

	// The single record of every subprocess magus spawns, with its working
	// directory: the first thing to reach for when a target behaves differently
	// than its command run by hand. dir is the resolved cwd ("" inherits ours).
	slog.DebugContext(ctx, "run.exec", "cmd", name, "args", args, "dir", c.Dir)

	runErr := c.Run()
	if ctx.Err() != nil {
		KillGroup(c) // reap grandchildren that ignored the graceful signal
	}

	res := ExecResult{}
	if c.ProcessState != nil {
		res.Started = true
		res.Code = c.ProcessState.ExitCode()
	} else {
		res.Code = -1 // process never started (binary not found, permission denied, etc.)
	}
	if opts.Capture {
		res.Stdout = outBuf.String()
		res.Stderr = errBuf.String()
	}
	// Surface ctx.Err() whenever cancelled, even if the process won the race and
	// exited 0, so callers can distinguish cancel from a clean finish. errors.Join
	// drops a nil runErr.
	if ctx.Err() != nil {
		runErr = errors.Join(ctx.Err(), runErr)
	}
	return res, runErr
}

// DaemonForwardVars are magus's internal daemon/pool pointers. They must never reach an
// arbitrary op subprocess: the socket is unauthenticated (MGS2008), and a program that links
// proc - magus's own test binaries - would mistake an inherited socket for "already adopted
// under a parent magus". The sandbox allowlist (internal/sandbox/env) already omits them, but
// the sandbox is off by default, so childEnv strips them from the base env UNCONDITIONALLY.
// A legitimate nested magus (runMagus in std/magus.go) re-injects them as Env overrides, which
// layer last and win - so the withholding here and the re-injection there must name the same set.
var DaemonForwardVars = []string{"MAGUS_DAEMON_SOCKET", "MAGUS_DAEMON_ADDRESS"}

// childEnv builds the subprocess environment: the sandbox's frozen BaseEnv (or the process
// environment when unsandboxed) with the daemon pool pointers withheld (see DaemonForwardVars),
// then the magus self-reference vars (see SelfVars), then the caller's overrides. Later entries
// win, so a caller may still override the self-reference vars or re-add a daemon pointer. The
// second return value names the daemon pointers actually withheld from the child - present in the
// base and not re-added by an override - so the caller can surface a breadcrumb (see Exec).
func childEnv(policy *sandbox.Policy, overrides []string) (env, withheld []string) {
	var base []string
	if policy != nil {
		base = policy.BaseEnv
	}
	root := base
	if root == nil {
		root = os.Environ()
	}
	for _, name := range DaemonForwardVars {
		if hasEnvVar(root, name) && !hasEnvVar(overrides, name) {
			withheld = append(withheld, name)
		}
	}
	env = withoutEnvVars(root, DaemonForwardVars)
	env = append(env, SelfVars()...)
	env = append(env, overrides...)
	return env, withheld
}

// withoutEnvVars returns a fresh copy of env with every "NAME=value" entry whose NAME is in drop
// removed. Malformed entries (no '=') are treated as a bare name and kept unless dropped.
func withoutEnvVars(env, drop []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if !slices.Contains(drop, name) {
			out = append(out, kv)
		}
	}
	return out
}

// hasEnvVar reports whether env holds a "name" or "name=..." entry.
func hasEnvVar(env []string, name string) bool {
	prefix := name + "="
	for _, kv := range env {
		if kv == name || strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

// SelfVars returns the magus self-reference variables injected into every magus
// subprocess, mirroring GNU Make's exported $(MAKE) and $(MAKELEVEL):
//
//   - MAGUS - the running binary's resolved path. Lets a spell or recipe re-invoke
//     magus reliably (`"$MAGUS" buzz ...`) with no dependence on PATH or an install,
//     including under `go run` (the temp build). The sandbox already grants exec on
//     this same resolved path (see sandbox defaults).
//   - MAGUS_LEVEL - the recursion depth this child runs at: the current process's
//     depth (from its own MAGUS_LEVEL; absent means 0, the top-level invocation)
//     plus one. So the counter climbs by one per magus process; a recipe can read
//     $MAGUS_LEVEL to tell how deep it is or guard against runaway recursion.
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
// (absent or invalid means 0, the top-level invocation). A value > 0 means this
// process was spawned by another magus, so it must not stand up its own daemon:
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
