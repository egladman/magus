//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module os -lang buzz -out ../host/gen/os.go

func init() { Register(Os) }

// magusWarnOnce fires at most once per process when a magusfile execs the magus
// binary, directing the author to magus.cmd instead.
var magusWarnOnce sync.Once

// warnIfMagusBinary emits a one-shot slog warning when cmd resolves to the
// magus binary. Execution is not blocked; the escape hatch stays open.
func warnIfMagusBinary(cmd string) {
	if filepath.Base(cmd) != "magus" {
		return
	}
	magusWarnOnce.Do(func() {
		slog.Warn("magusfile: os.exec called with 'magus' binary",
			"hint", "use magus.cmd({...}) instead - in-process, version-pinned, no arg-quoting issues")
	})
}

// The cwd/path helpers (cwdKey, WithCwd, cwdFromContext, CwdFromContext,
// resolvePath, EffectiveCwd) live in cwd.go so the pure-compute modules can share
// them without pulling this IO-heavy file into the wasm build. resolveDir stays
// here since only the exec primitives use it.

// resolveDir computes the effective working directory for an exec primitive:
// an explicit dir argument wins (joined onto the context cwd when relative);
// otherwise the context cwd is used; "" means inherit the process cwd.
func resolveDir(ctx context.Context, dir string) string {
	base := cwdFromContext(ctx)
	if dir == "" {
		return base
	}
	if filepath.IsAbs(dir) || base == "" {
		return dir
	}
	return filepath.Join(base, dir)
}

// Os is the "os" host module: direct-exec primitives (no shell invocation).
var Os = Module{
	Name: "os",
	Doc:  "Process execution. os.exec runs a command directly (no shell); os.exec_sh runs a line through the shell. Both stream output live and return a result {stdout, stderr, code, ok}.",
	Methods: []Method{
		{
			Name: "exec",
			Doc:  "Run cmd directly (no shell; args are never shell-interpolated). Output streams live and is captured. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. Optional dir runs cmd there (relative to the target's cwd). opts.stdin is fed to the process as standard input - pipe by passing a prior call's stdout.",
			Args: []Arg{
				{Name: "cmd", Type: TypeString},
				{Name: "args", Type: TypeStringSlice, Optional: true},
				{Name: "dir", Type: TypeString, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    OsExec,
		},
		{
			Name: "exec_sh",
			Doc:  "Run line through a shell - for pipes, redirection, globs, and variable expansion. Default shell is /bin/sh (cmd on Windows); pass opts.shell (e.g. \"bash\") to override, resolved via PATH. A shell line is written in the platform shell's dialect, so sh and cmd lines are not portable across OSes - for cross-platform logic prefer os.exec plus the fs/os helpers. Same result and raise semantics as exec (opts.stdin and opts.allow_failure included); optional dir runs the shell there.",
			Args: []Arg{
				{Name: "line", Type: TypeString},
				{Name: "dir", Type: TypeString, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    OsExecSh,
		},
		{
			Name: "with_env",
			Doc:  "Set env vars for the duration of callback; restore after.",
			Args: []Arg{
				{Name: "env", Type: TypeStringMap},
				{Name: "callback", Type: TypeFunc},
			},
			Returns: nil,
			Impl:    OsWithEnv,
		},
		{
			Name: "with_slots",
			Doc:  "Reserve n slots from magus's concurrency budget for the duration of callback. Use when callback runs a command with its own internal parallelism (make -j, a test runner) that magus can't see, so the global budget is not oversubscribed.",
			Args: []Arg{
				{Name: "n", Type: TypeInt},
				{Name: "callback", Type: TypeFunc},
			},
			Returns: nil,
			Impl:    OsWithSlots,
		},
		{
			Name:    "platform",
			Doc:     "Return the Docker/OCI platform triple: (os, arch, variant).",
			Args:    nil,
			Returns: []Ret{{Type: TypeString}, {Type: TypeString}, {Type: TypeString}},
			Impl:    OsPlatform,
		},
		{
			Name:    "exit",
			Doc:     "Abort the current run with the given exit code - typically after logging an error. Does NOT call os.Exit (that would kill a shared daemon); it raises, ending the target, and the code becomes magus's process exit status.",
			Args:    []Arg{{Name: "code", Type: TypeInt}},
			Returns: nil,
			Impl:    OsExit,
		},
		{
			Name:    "sleep",
			Doc:     "Pause for the given number of milliseconds (fractional allowed), matching Buzz's os.sleep. Cancellable: if the run is interrupted it returns early with the cancellation error rather than blocking.",
			Args:    []Arg{{Name: "ms", Type: TypeFloat}},
			Returns: nil,
			Impl:    OsSleep,
		},
		{
			Name:    "which",
			Doc:     "Resolve cmd against PATH and return its absolute path, or \"\" if it is not found. Use it to check a tool is installed before running it (and emit a clear hint/error instead of a cryptic exec failure).",
			Args:    []Arg{{Name: "cmd", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    OsWhich,
		},
		{
			Name:    "stdin_is_terminal",
			Doc:     "Report whether standard input is a terminal (TTY) rather than a pipe, file, or /dev/null. Use it to fail fast with a clear message instead of blocking on a read of stdin that will never receive piped input.",
			Args:    nil,
			Returns: []Ret{{Type: TypeBool}},
			Impl:    OsStdinIsTerminal,
		},
		{
			Name:    "num_cpu",
			Doc:     "Return the number of logical CPUs available, for sizing a command's own internal parallelism (see os.with_slots).",
			Args:    nil,
			Returns: []Ret{{Type: TypeInt}},
			Impl:    OsNumCPU,
		},
		{
			Name:    "hostname",
			Doc:     "Return the host machine's name.",
			Args:    nil,
			Returns: []Ret{{Type: TypeString}},
			Impl:    OsHostname,
		},
		{
			Name: "retry",
			Doc:  "Call fn up to max times, retrying on error with exponential backoff; returns fn's value on success. opts: {backoff_ms:float (default 500), max_backoff_ms:float (default 30000)}.",
			Args: []Arg{
				{Name: "max", Type: TypeInt},
				{Name: "fn", Type: TypeFunc},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    OsRetry,
		},
	},
}

// OsNumCPU returns the number of logical CPUs usable by the process.
func OsNumCPU(_ context.Context) (int, error) {
	return goruntime.NumCPU(), nil
}

// OsHostname returns the host machine's name.
func OsHostname(_ context.Context) (string, error) {
	name, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("os.hostname: %w", err)
	}
	return name, nil
}

// OsStdinIsTerminal reports whether stdin is a TTY, reusing the shared terminal
// check (see internal/interactive/tty).
func OsStdinIsTerminal(_ context.Context) (bool, error) {
	return tty.StdinIsTerminal(), nil
}

// OsWhich resolves cmd against PATH. A missing command is reported as "" (not an
// error) so a magusfile can branch on `os.which(cmd) == ""`.
func OsWhich(_ context.Context, cmd string) (string, error) {
	path, _ := exec.LookPath(cmd) // missing command reported as "", per the doc above
	return path, nil
}

// OsSleep pauses for ms milliseconds (matching Buzz's os.sleep), honoring context
// cancellation: a one-shot timer races ctx.Done() so an interrupted run wakes
// immediately with ctx.Err() rather than blocking for the full duration. A
// non-positive duration is a no-op; an absurdly large one is clamped to 24h to
// avoid overflowing the nanosecond time.Duration.
func OsSleep(ctx context.Context, ms float64) error {
	if ms <= 0 {
		return nil
	}
	const maxMillis = float64(24 * time.Hour / time.Millisecond)
	if ms > maxMillis {
		ms = maxMillis
	}
	t := time.NewTimer(time.Duration(ms * float64(time.Millisecond)))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// OsExit aborts the current run by returning a types.ExitError carrying code. It
// deliberately does not call os.Exit: a target may run inside a daemon serving
// other workspaces, where os.Exit would kill unrelated work. The error propagates
// to the CLI (and daemon), which translate it into the process exit status. It
// also records the code on ctx (types.CaptureExit) so it survives when the engine
// stringifies the error type away; the interpreter reads it back. See types.ExitError.
func OsExit(ctx context.Context, code int) error {
	types.CaptureExit(ctx, code)
	return types.ExitError{Code: code}
}

// OsPlatform wraps HostPlatform for use as a Method Impl.
func OsPlatform(_ context.Context) (string, string, string, error) {
	osName, arch, variant := HostPlatform()
	return osName, arch, variant, nil
}

// optBoolDefault reads a boolean option from opts, returning def when absent.
func optBoolDefault(opts map[string]any, key string, def bool) bool {
	if opts == nil {
		return def
	}
	if v, ok := opts[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// optStringDefault reads a string option from opts, returning def when absent.
func optStringDefault(opts map[string]any, key, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// runResult forks name+args through run.Exec (sharing the fork/sandbox/env/cancel
// core with fork spell targets), capturing output for the returned {stdout,
// stderr} record while still streaming it to `magus tail` and the report. Per-call
// os.with_env overrides ride ctx (withEnvKey). label/cmd name the command in the
// raise message. A non-zero exit raises unless opts.allow_failure is true, which
// returns the record instead; a sandbox exec denial always propagates.
func runResult(ctx context.Context, name string, args []string, dir, label, cmd string, opts map[string]any) (types.ExecResult, error) {
	overrides, _ := ctx.Value(withEnvKey{}).([]string)
	res, err := run.Exec(ctx, name, args, run.ExecOptions{
		Dir:     dir,
		Env:     overrides,
		Stdin:   optStringDefault(opts, "stdin", ""),
		Capture: true,
	})
	if err != nil && errors.Is(err, types.ExecDenied) {
		return types.ExecResult{}, err
	}
	if res.Code != 0 && !optBoolDefault(opts, "allow_failure", false) {
		if !res.Started {
			// Process never started. Common footgun: os.exec runs a single program
			// with no shell, so a command line ("a | b", "cd x", "$VAR") fails to
			// start as a literal program name. Nudge toward os.exec_sh, but only on
			// the not-found failure of a shell-shaped command (os.exec stays the
			// right, faster default for a plain program).
			if label == "os.exec" && looksLikeShellCommand(cmd) {
				interactive.Emit(os.Stderr, fmt.Sprintf(
					"%q looks like a shell command line, but os.exec runs a single program directly with no shell; "+
						"use os.exec_sh for pipes, redirection, globs, && / ||, or variable expansion", cmd))
			}
			return types.ExecResult{}, fmt.Errorf("%s %s: %w", label, cmd, err)
		}
		return types.ExecResult{}, fmt.Errorf("%s %s: exit %d", label, cmd, res.Code)
	}
	return types.ExecResult{
		Stdout: strings.TrimSpace(res.Stdout),
		Stderr: strings.TrimSpace(res.Stderr),
		Code:   res.Code,
		OK:     res.Code == 0,
	}, nil
}

// looksLikeShellCommand reports whether cmd looks like a shell command line
// (shell metacharacters or a shell builtin) rather than a single program name:
// the signature of an os.exec call that should have been os.exec_sh.
func looksLikeShellCommand(cmd string) bool {
	// A space (a command line, not a program name), pipe/redirect/sequence
	// operator, glob, variable, subshell, or brace expansion: things a shell
	// interprets that os.exec passes literally as a program name.
	if strings.ContainsAny(cmd, " \t|&;<>$`*?(){}\n") {
		return true
	}
	switch cmd {
	case "cd", "export", "source", "alias", "set", "unset", "eval", "umask", "pushd", "popd":
		return true
	}
	return false
}

// OsExec runs cmd with args directly (no shell). Output streams live and is
// captured; it returns {stdout, stderr, code, ok}, raising on a non-zero exit
// unless opts.allow_failure is true. The optional dir runs cmd in that directory
// (relative to the context cwd); omitted, it inherits the context (or process) cwd.
func OsExec(ctx context.Context, cmd string, args []string, dir string, opts map[string]any) (types.ExecResult, error) {
	warnIfMagusBinary(cmd)
	wd := resolveDir(ctx, dir)
	if wd != "" {
		if err := checkRead(ctx, wd); err != nil {
			return types.ExecResult{}, err
		}
	}
	return runResult(ctx, cmd, args, wd, "os.exec", cmd, opts)
}

// OsExecSh runs line through a shell. The default is /bin/sh (cmd on Windows);
// opts.shell overrides it (e.g. "bash"), resolved via PATH like any other command.
// Same streaming, capture, and raise semantics as OsExec. See OsExec for dir.
func OsExecSh(ctx context.Context, line string, dir string, opts map[string]any) (types.ExecResult, error) {
	shell, flag := shellExe(optStringDefault(opts, "shell", ""))
	wd := resolveDir(ctx, dir)
	if wd != "" {
		if err := checkRead(ctx, wd); err != nil {
			return types.ExecResult{}, err
		}
	}
	return runResult(ctx, shell, []string{flag, line}, wd, "os.exec_sh", line, opts)
}

// shellExe returns the shell program and its command flag. The default is
// hardcoded (/bin/sh on unix, cmd on Windows), deliberately NOT $SHELL, so a
// magusfile runs the same shell on every machine (the user's interactive shell
// varies and its dialect may not be POSIX). An override (opts.shell) wins and is
// declared in the magusfile, keeping the choice reproducible; bare names resolve
// via PATH at exec time, absolute paths are used as-is.
func shellExe(override string) (string, string) {
	shell := override
	if shell == "" {
		if goruntime.GOOS == "windows" {
			shell = "cmd"
		} else {
			shell = "/bin/sh"
		}
	}
	return shell, shellFlag(shell)
}

// shellFlag returns the "run this command string" flag for shell: /c for cmd,
// -c for every POSIX-style shell (sh, bash, zsh, dash, ...).
func shellFlag(shell string) string {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(shell)), ".exe")
	if base == "cmd" {
		return "/c"
	}
	return "-c"
}

// withEnvKey is used to thread per-command env overrides through ctx so that
// sh.* functions spawned inside a sh.with_env callback receive the extra vars.
type withEnvKey struct{}

// OsWithEnv injects extra env vars for subprocesses spawned during the
// callback without mutating the daemon's process-global environment.
// Overrides are propagated via ctx and merged at exec time in applySandboxPolicy.
func OsWithEnv(ctx context.Context, env map[string]string, cb Callback) error {
	// Merge with any outer with_env overrides already on ctx.
	outer, _ := ctx.Value(withEnvKey{}).([]string)
	merged := make([]string, len(outer), len(outer)+len(env))
	copy(merged, outer)
	for k, v := range env {
		merged = append(merged, k+"="+v)
	}
	ctx = context.WithValue(ctx, withEnvKey{}, merged)
	_, callErr := cb.Call(ctx)
	return callErr
}

// OsRetry calls fn up to max times, retrying on error with exponential backoff.
// On success it returns fn's first return value; on exhaustion it errors.
// opts keys: backoff_ms (initial delay, default 500), max_backoff_ms (cap, default 30000).
func OsRetry(ctx context.Context, max int, fn Callback, opts map[string]any) (any, error) {
	if fn == nil {
		return nil, fmt.Errorf("os.retry: fn must not be nil")
	}
	backoffMs := 500.0
	maxBackoffMs := 30000.0
	if opts != nil {
		if v, ok := opts["backoff_ms"]; ok {
			if f, ok := retryFloat(v); ok {
				backoffMs = f
			}
		}
		if v, ok := opts["max_backoff_ms"]; ok {
			if f, ok := retryFloat(v); ok {
				maxBackoffMs = f
			}
		}
	}
	var lastErr error
	for i := 0; i < max; i++ {
		ret, err := fn.Call(ctx)
		if err == nil {
			if len(ret) > 0 {
				return ret[0], nil
			}
			return nil, nil //nolint:nilnil // fn succeeded with no return value; nil is the no-value result
		}
		lastErr = err
		if i < max-1 {
			delay := backoffMs * math.Pow(2, float64(i))
			if delay > maxBackoffMs {
				delay = maxBackoffMs
			}
			t := time.NewTimer(time.Duration(delay * float64(time.Millisecond)))
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, ctx.Err()
			case <-t.C:
			}
		}
	}
	return nil, fmt.Errorf("os.retry: %d attempt(s): %w", max, lastErr)
}

// retryFloat extracts a float64 from a Go any value (int, int64, or float64).
func retryFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// OsWithSlots reserves n slots from magus's concurrency limiter for the duration
// of cb, so a callback running its own internally-parallel work (make -j, a test
// runner) does not oversubscribe the global budget. With no limiter on ctx (e.g. a
// standalone run) it just invokes cb. Mirrors archive.*: the build slot already
// held is handed back while the n are reserved, so peak in-flight stays within the
// cap rather than cap+n.
func OsWithSlots(ctx context.Context, n int, cb Callback) error {
	if lim := cache.LimiterFromContext(ctx); lim != nil && n > 0 {
		// Hand back every slot we hold (a weighted step holds more than one) so
		// reserving n cannot deadlock on slots we pin ourselves.
		if held := cache.SlotsHeld(ctx); held > 0 {
			lim.ReleaseN(held)
			defer func() { _ = lim.AcquireN(context.WithoutCancel(ctx), held) }()
		}
		if err := lim.AcquireN(ctx, n); err != nil {
			return fmt.Errorf("os.with_slots: %w", err)
		}
		defer lim.ReleaseN(n)
	}
	_, callErr := cb.Call(ctx)
	return callErr
}
