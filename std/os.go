//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module os -lang buzz -out ../internal/interp/bindings/gen/os.go

func init() { Register(Os) }

// magusWarned holds each typed member warnIfMagusBinary has already named, so a script
// looping over one invocation hears about it once.
var magusWarned sync.Map

// warnIfMagusBinary warns when cmd is the magus binary and args name an invocation a typed
// magus\ member answers, naming that member: it returns a typed record where the exec
// returns text to parse back, and runs the build that loaded the script rather than
// whichever magus the path finds. An invocation no member answers (`job exec`, `queue ls`,
// `server start`) is what proc.exec is for, so it stays silent. Execution is never blocked.
func warnIfMagusBinary(ctx context.Context, cmd string, args []string) {
	if filepath.Base(cmd) != "magus" {
		return
	}
	member := typedMagusMember(args)
	if member == "" {
		return
	}
	if _, seen := magusWarned.LoadOrStore(member, true); seen {
		return
	}
	slog.WarnContext(ctx, "proc\\exec runs magus for an invocation a typed member answers",
		"invocation", strings.Join(append([]string{"magus"}, args...), " "),
		"hint", "call magus\\"+member+" instead")
}

// typedMagusMembers maps a magus invocation, by its subcommand and, where the subcommand
// alone does not decide it, the word after, to the magus\ member that answers it. The
// two-word key wins. `describe` takes its nouns singular or plural, so each is keyed
// both ways; a noun with no method (`describe job`) has no member. A subcommand whose
// member needs a particular shape of argv (`affected`, `ls`, `describe target <ref>`) is
// decided in typedMagusMember instead.
var typedMagusMembers = map[string]string{
	"run":                 "run",
	"describe charm":      "describe.charm",
	"describe charms":     "describe.charm",
	"describe file":       "describe.file",
	"describe files":      "describe.file",
	"describe graph":      "describe.graph",
	"describe harness":    "describe.harness",
	"describe mcp-tool":   "describe.mcpTool",
	"describe mcp-tools":  "describe.mcpTool",
	"describe module":     "describe.module",
	"describe modules":    "describe.module",
	"describe project":    "describe.project",
	"describe projects":   "describe.project",
	"describe rule":       "describe.rule",
	"describe rules":      "describe.rule",
	"describe spell":      "describe.spell",
	"describe spells":     "describe.spell",
	"describe tool":       "describe.tool",
	"describe tools":      "describe.tool",
	"describe workspace":  "describe.workspace",
	"describe workspaces": "describe.workspace",
	"doctor":              "doctor",
	"clean":               "clean",
	"diff":                "diff",
	"query":               "query",
	"query output":        "output",
	"explain":             "explain",
	"path":                "path",
	"refs":                "refs",
	"insight":             "insight",
	"graph stats":         "stats",
	"ls jobs":             "job.list",
	"ls targets":          "describe.graph",
	"job fork":            "job.put",
	"job exit":            "job.exit",
	"job wait":            "job.wait",
	"vcs checkpoint":      "vcs.checkpoint",
	"session attention":   "attention",
}

// typedMagusMember is the magus\ member that answers `magus <args>`, or "" when none does.
// Global flags ahead of the subcommand leave it undecided, and undecided is silent.
func typedMagusMember(args []string) string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return ""
	}
	sub, rest := args[0], args[1:]
	if len(rest) > 0 {
		if member, ok := typedMagusMembers[sub+" "+rest[0]]; ok {
			return member
		}
	}
	positional := hasPositional(rest)
	switch sub {
	case "ls":
		// A bare `ls` lists the projects; `ls <noun>` has a member only where the table says.
		if !positional {
			return "describe.project"
		}
		return ""
	case "describe":
		// `describe target` is the catalog; `describe target <ref>` evaluates one target.
		if len(rest) == 0 || (rest[0] != "target" && rest[0] != "targets") {
			return ""
		}
		if hasPositional(rest[1:]) {
			return "describe.evaluatedTarget"
		}
		return "describe.target"
	case "affected":
		// `affected <target>` runs the target across the affected set, which no member does.
		if positional {
			return ""
		}
		if slices.Contains(rest, "--impact") {
			return "impact"
		}
		return "affected"
	}
	return typedMagusMembers[sub]
}

// valueFlags are the flags `ls` and `affected` take whose value is the next argument, so
// that value is not mistaken for a positional one.
var valueFlags = []string{"-o", "--output", "--base", "--root"}

// hasPositional reports whether args carry a positional argument once flags and their
// values are set aside.
func hasPositional(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains(valueFlags, args[i]):
			i++
		case !strings.HasPrefix(args[i], "-"):
			return true
		}
	}
	return false
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
	Doc:  "The machine and this process: platform triple, CPU count, hostname, the running magus binary, and the two members that shadow Buzz's own (exit, sleep). Running OTHER processes lives in the proc module.",
	Methods: []Method{
		{
			Name: "with_env",
			Doc:  "Add env vars to subprocesses `proc\\exec` / `proc\\shell` start inside callback. Never touches the process's own environment - a lookup like os\\env inside callback does not see them.",
			Args: []Arg{
				{Name: "env", Type: TypeStringMap},
				{Name: "callback", Type: TypeFunc, Func: "fun () > void !> any"},
			},
			Returns: nil,
			Raises:  true,
			Impl:    OsWithEnv,
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
			Doc:     "Abort the current run with the given exit code - typically after logging an error. Does NOT call os.Exit (that would kill a shared server); it raises, ending the target, and the code becomes magus's process exit status.",
			Args:    []Arg{{Name: "code", Type: TypeInt}},
			Returns: nil,
			// NOT Raises, though the binding fails to end the run: upstream declares
			// exit with no error set, so a script calling it from a function with no
			// `!>` checks clean under both.
			Impl: OsExit,
		},
		{
			Name:    "sleep",
			Doc:     "Pause for the given number of milliseconds (fractional allowed), matching Buzz's os\\sleep. Cancellable: if the run is interrupted it returns early with the cancellation error rather than blocking.",
			Args:    []Arg{{Name: "ms", Type: TypeFloat}},
			Returns: nil,
			Raises:  true,
			Impl:    OsSleep,
		},
		{
			Name:    "num_cpu",
			Doc:     "Return the number of logical CPUs available, for sizing a command's own internal parallelism (see os\\with_slots).",
			Args:    nil,
			Returns: []Ret{{Type: TypeInt}},
			Impl:    OsNumCPU,
		},
		{
			Name:    "hostname",
			Doc:     "Return the host machine's name.",
			Args:    nil,
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    OsHostname,
		},
		{
			Name:    "executable",
			Doc:     "Return the absolute path of the running magus binary. Pair it with fs\\stat inside a long-lived watch loop to detect that the binary was rebuilt or upgraded underneath the process, which means any output it goes on to generate would be stale.",
			Args:    nil,
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    OsExecutable,
		},
		{
			Name: "retry",
			Doc:  "Call fn up to max times, retrying on error with exponential backoff; returns fn's value on success. opts: {backoff_ms:float (default 500), max_backoff_ms:float (default 30000)}.",
			Args: []Arg{
				{Name: "max", Type: TypeInt},
				{Name: "fn", Type: TypeFunc, Func: "fun () > any !> any"},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny}},
			Raises:  true,
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

// OsExecutable returns the absolute path of the running binary. It resolves symlinks so
// two paths pointing at the same file compare equal, which matters for the intended use:
// a long-lived watcher stats this path each tick and treats a change as "I am no longer
// the current build, so anything I generate from here is stale".
func OsExecutable(_ context.Context) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("os.executable: %w", err)
	}
	// EvalSymlinks so a rebuild through a symlinked path is still seen as the same file,
	// and so the returned path is the one fs.stat will actually resolve.
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// The binary can legitimately be gone already: `go run` deletes its temp build
		// once the process is up. The unresolved path is still the best answer.
		//
		//nolint:nilerr
		return exe, nil
	}
	return resolved, nil
}

// OsStdinIsTerminal reports whether stdin is a TTY, reusing the shared terminal
// check (see internal/interactive/tty).
func OsStdinIsTerminal(_ context.Context) (bool, error) {
	return tty.StdinIsTerminal(), nil
}

// OsWhich resolves cmd against PATH, RAISING when it is not there.
//
// It used to return "" so a magusfile could branch on `proc.which(cmd) == ""`. That is the
// same sentinel the vcs accessors carried, with the same two problems: the check is
// optional, so forgetting it hands the empty string on to an exec or a path join; and it
// is untyped, so nothing tells a reader the value needs testing at all. Raising makes the
// missing tool a case the author has to answer, in the same shape as every other failure
// in these modules:
//
//	try { final vhs = proc\which("vhs"); ... } catch (e) { magus\log.info("vhs not installed"); }
func OsWhich(ctx context.Context, cmd string) (string, error) {
	path, err := run.LookPath(ctx, cmd)
	if err != nil {
		return "", types.WrapDiagnostic(types.ToolNotOnPath, err, "%q is not on PATH", cmd)
	}
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
// deliberately does not call os.Exit: a target may run inside a server serving
// other workspaces, where os.Exit would kill unrelated work. The error propagates
// to the CLI (and server), which translate it into the process exit status. It
// also records the code on ctx (types.CaptureExit) so it survives when the engine
// stringifies the error type away; the interpreter reads it back. See types.ExitError.
func OsExit(ctx context.Context, code int) error {
	code = types.NormalizeExitCode(code)
	types.CaptureExit(ctx, code)
	return types.ExitError{Code: code}
}

// OsPlatform wraps HostPlatform for use as a Method Impl.
func OsPlatform(_ context.Context) (string, string, string, error) {
	osName, arch, variant := HostPlatform()
	return osName, arch, variant, nil
}

// optBool reads a boolean option from opts: false when it is absent or not a bool.
func optBool(opts map[string]any, key string) bool {
	b, _ := opts[key].(bool)
	return b
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
// stderr} object while still streaming it to `magus tail` and the report. Per-call
// os.with_env overrides ride ctx (withEnvKey). label/cmd name the command in the
// raise message. A non-zero exit raises unless opts.allow_failure is true, which
// returns the object instead; a sandbox exec denial always propagates.
//
// opts.quiet keeps the capture and drops the live streaming, which is what a caller
// consuming a value rather than watching a build wants. It is read HERE, the one path
// proc.exec, proc.shell, and vcs.cmd all share, so the three cannot offer different
// option sets by accident, and it spells the option the same way the magus.* methods
// already do.
//
// Without it a chatty command has no quiet form at all: `git ls-remote --tags` against
// nodejs/node is 954 lines, streamed in full before the caller sees its own output.
// Capture stays on regardless, so a quiet call still returns stdout.
func runResult(ctx context.Context, name string, args []string, dir, label, cmd string, opts map[string]any) (types.ExecResult, error) {
	overrides, _ := ctx.Value(withEnvKey{}).([]string)
	res, err := run.Exec(ctx, name, args, run.ExecOptions{
		Dir:     dir,
		Env:     overrides,
		Stdin:   optStringDefault(opts, "stdin", ""),
		Capture: true,
		Quiet:   optBool(opts, "quiet"),
		TTY:     optBool(opts, "tty"),
	})
	if err != nil && errors.Is(err, types.ExecDenied) {
		return types.ExecResult{}, err
	}
	if res.Code != 0 && !optBool(opts, "allow_failure") {
		if !res.Started {
			// Process never started. Common footgun: proc.exec runs a single program
			// with no shell, so a command line ("a | b", "cd x", "$VAR") fails to
			// start as a literal program name. Nudge toward proc.shell, but only on
			// the not-found failure of a shell-shaped command (proc.exec stays the
			// right, faster default for a plain program).
			if label == `proc\exec` && looksLikeShellCommand(cmd) {
				interactive.Emit(os.Stderr, fmt.Sprintf(
					"%q looks like a shell command line, but proc\\exec runs a single program directly with no shell; "+
						"use proc\\shell for pipes, redirection, globs, && / ||, or variable expansion", cmd))
			}
			return types.ExecResult{}, fmt.Errorf("%s %s: %w", label, cmd, err)
		}
		// ExitCode() is -1 for a process killed by a SIGNAL, never a real status, so
		// "exit -1" is the one message here that tells a reader nothing. Go already put
		// the reason in err ("signal: killed", or the cancellation) and this used to
		// discard it, which is how one real failure surfaced as several mystery ones.
		if res.Code == -1 && err != nil {
			// SIGKILL is uncatchable, so the process itself can never explain this. On
			// CI it is nearly always the OOM killer picking the largest RSS in the job,
			// and the job then disappears with no other trace; name the suspicion here
			// or nobody gets one.
			if strings.Contains(err.Error(), "signal: killed") && !errors.Is(err, context.Canceled) {
				return types.ExecResult{}, fmt.Errorf(
					"%s %s: %w (SIGKILL: nothing in the process asked for this - on CI it is usually the OOM killer)",
					label, cmd, err)
			}
			return types.ExecResult{}, fmt.Errorf("%s %s: %w", label, cmd, err)
		}
		return types.ExecResult{}, fmt.Errorf("%s %s: exit %d", label, cmd, res.Code)
	}
	// A process killed by a signal has no status and wrote nothing, so the reason lives
	// only in err; an allow_failure caller would otherwise read a bare -1.
	if res.Code == -1 && err != nil && strings.TrimSpace(res.Stderr) == "" {
		res.Stderr = err.Error()
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
// the signature of an proc.exec call that should have been proc.shell.
func looksLikeShellCommand(cmd string) bool {
	// A space (a command line, not a program name), pipe/redirect/sequence
	// operator, glob, variable, subshell, or brace expansion: things a shell
	// interprets that proc.exec passes literally as a program name.
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
	warnIfMagusBinary(ctx, cmd, args)
	wd := resolveDir(ctx, dir)
	if wd != "" {
		if err := checkRead(ctx, wd); err != nil {
			return types.ExecResult{}, err
		}
	}
	return runResult(ctx, cmd, args, wd, `proc\exec`, cmd, opts)
}

// OsShell builds the argv that runs line through the platform shell and returns
// it, rather than running it. It replaced proc.shell, which had zero callers in
// this tree: where a shell was genuinely wanted, people wrote proc.exec("sh",
// ["-c", ...]) by hand, which says out loud what exec_sh hid.
//
// Two verbs for "run a process" cost more than they bought. Every option worth
// having (stdin, quiet, allow_failure, tty) had to exist and be documented twice,
// and the wrapper's only real content was ten lines of platform selection. That
// selection IS worth having, so it survives here as a pure function: the shell
// choice becomes a value you can print before anything executes, instead of a
// decision taken inside a call you cannot see into.
func OsShell(_ context.Context, line string, shell string) (types.ShellCommand, error) {
	bin, flag := shellExe(shell)
	return types.ShellCommand{Bin: bin, Args: []string{flag, line}}, nil
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
// callback without mutating the server's process-global environment.
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
		return nil, fmt.Errorf(`os\retry: fn must not be nil`)
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
	return nil, fmt.Errorf(`os\retry: %d attempt(s): %w`, max, lastErr)
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
// cap rather than cap+n. Processes cb starts share a jobserver of n slots
// (run.SeatJobserver), so a `make` or cargo given no -j of its own runs n wide.
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
		// The step's own pool was sized to the slots just handed back.
		seated, closeJobserver, err := run.SeatJobserver(ctx, n)
		if err != nil {
			return fmt.Errorf("os.with_slots: %w", err)
		}
		defer closeJobserver()
		ctx = seated
	}
	_, callErr := cb.Call(ctx)
	return callErr
}
