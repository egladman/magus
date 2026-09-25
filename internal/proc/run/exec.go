package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc/environ"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/internal/secret"
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
	// TTY runs the child attached to a pseudo-terminal instead of pipes.
	//
	// The difference between the output a tool gives YOU and the output it gives a
	// pipe: nearly every modern CLI calls isatty() and drops color and progress
	// rendering, so without this magus shows and caches that degraded form.
	//
	// Two consequences. A terminal is ONE stream, so stdout and stderr arrive
	// interleaved and Capture returns both in Stdout with Stderr empty. And the
	// captured bytes contain escape sequences, so an animated progress bar records
	// every frame it drew.
	//
	// Unsupported outside unix (see pty_other.go), where it is an error rather than
	// a silent downgrade to pipes.
	TTY bool

	// TTYCols and TTYRows fix the pseudo-terminal's geometry. Zero inherits this
	// process's terminal, or 80x24 when it has none.
	//
	// Set them when the captured bytes are an ARTIFACT rather than a means to an
	// end. A tool lays its output out to the width it is told, so an inherited
	// size makes the capture a function of whoever ran the recorder, and two
	// machines produce two different files from the same command.
	//
	// Ignored unless TTY is set.
	TTYCols, TTYRows int

	// CancelGrace is how long a cancelled child and its process group have after SIGTERM
	// (CTRL_BREAK on Windows) before the child is killed, and the group with it. It also
	// bounds the wait, once the child exits, for output a process that left the group
	// still holds open. Zero is defaultCancelGrace.
	CancelGrace time.Duration
}

// defaultCancelGrace is ExecOptions.CancelGrace when unset.
const defaultCancelGrace = 5 * time.Second

// ExecResult is the outcome of Exec.
type ExecResult struct {
	Stdout  string // captured stdout, empty unless ExecOptions.Capture; not trimmed
	Stderr  string // captured stderr, empty unless ExecOptions.Capture; not trimmed
	Code    int    // exit code; -1 when the process was signaled or never started
	Started bool   // whether the process actually started; distinguishes a -1 exit from a start failure
	// MaxRSSBytes is the PEAK resident memory this process reached, in bytes, or 0 when
	// the host cannot report it (windows, wasm) or the process never ran. Zero means
	// UNKNOWN rather than "used nothing".
	//
	// The high-water mark, not the memory held at exit: a compile that sits at 200MB and
	// spikes to 4GB while linking is a 4GB process for the purpose of deciding what can
	// run alongside it, and the spike is the part that takes a machine down.
	//
	// Two readings folded to whichever is larger, because each is blind where the
	// other sees.
	//
	// The kernel's own figure costs nothing (it is read off the same ProcessState
	// the exit code comes from) and never misses a spike, but it folds a subtree as
	// a MAXIMUM: wait4 propagates ru_maxrss up by taking the largest process, never
	// the sum. Measured on darwin, a driver that forked four concurrent 800MB
	// children reports 801MB. That alone reports the biggest process in a tree
	// rather than what the tree held together.
	//
	// So a sampler also totals the live process tree while the command runs (see
	// tree_sample.go), which catches concurrency but only at the instants it looks.
	// The maximum of the two cannot be smaller than what magus reported before
	// sampling existed, and is a floor rather than a true peak either way.
	MaxRSSBytes int64
}

// BuzzObject renders the shared exec result shape.
func (r ExecResult) BuzzObject() types.BuzzObject {
	return types.BuzzObject{
		"stdout": strings.TrimSpace(r.Stdout),
		"stderr": strings.TrimSpace(r.Stderr),
		"code":   r.Code,
		"ok":     r.Code == 0,
	}
}

// Exec runs a subprocess with the current sandbox policy and output writers. Under a
// policy the child starts confined by the kernel where the host has landlock (see
// sandbox.Command), and a required policy on a host that cannot confine it is refused
// (MGS2012) before anything starts.
//
// The child runs in a process group of its own. Off a TTY, on Linux, macOS and the
// BSDs, whatever of that group outlives the child is killed once the child exits and
// before it is reaped, so a background process it started ends with it; a process that
// left the group (setsid) is not reached.
func Exec(ctx context.Context, name string, args []string, opts ExecOptions) (ExecResult, error) {
	if types.Tracing(ctx) {
		slog.InfoContext(ctx, "run.exec", "cmd", name, "args", args, "dir", opts.Dir)
		return ExecResult{Started: true, Code: 0}, nil
	}
	// The --step gate, consulted here because Exec is what every caller forks through.
	// A skipped command reports as a start-that-never-happened rather than an error:
	// the user chose not to run it, which is not a failure of the target.
	if gate, ok := ctx.Value(stepGateKey{}).(StepGate); ok && gate != nil {
		switch gate(ctx, name, args, opts.Dir) {
		case StepActionSkip:
			return ExecResult{}, nil
		case StepActionAbort:
			return ExecResult{Code: -1}, ErrAborted
		case StepActionContinue, StepActionStep:
		}
	}
	if project, target, ok := journal.StepFromContext(ctx); ok {
		// The argv can contain a credential when a magusfile passes one as an argument
		// (`-p <token>`) instead of on stdin. journal.Emit redacts Text for every event
		// kind, so this site does not repeat it; that centralization is the point.
		journal.Emit(ctx, journal.Event{
			Kind: journal.KindExec, Project: project, Target: target, Text: commandLine(name, args),
		})
	}
	policy := sandbox.PolicyFromContext(ctx)
	confined, err := policy.KernelConfines()
	if err != nil {
		return ExecResult{Code: -1}, err
	}
	env, withheld := childEnv(ctx, policy, opts.Env)
	resolved, lookErr := lookPath(name, envValue(env, "PATH"))
	if lookErr != nil {
		return ExecResult{Code: -1}, classifyMissingBinary(lookErr, name, false)
	}
	if policy != nil {
		checked := resolved
		if !filepath.IsAbs(checked) && opts.Dir != "" {
			// exec runs a relative path from Dir, not from this process's directory.
			checked = filepath.Join(opts.Dir, checked)
		}
		if err := policy.CheckExec(ctx, checked); err != nil {
			sandbox.EmitDenyHint(policy, filesystem.Exec, resolved)
			return ExecResult{Code: -1}, types.DiagnosticErrorf(types.ExecDenied, "exec denied: %s", resolved)
		}
	}
	c, ruleset, err := command(ctx, policy, confined, name, resolved, args)
	if err != nil {
		return ExecResult{Code: -1}, err
	}
	c.Dir = opts.Dir
	group := setCancel(c) // platform-specific graceful cancel; see run_unix.go / run_windows.go
	c.WaitDelay = defaultCancelGrace
	if opts.CancelGrace > 0 {
		c.WaitDelay = opts.CancelGrace
	}
	c.Env = env
	if js := jobserverFrom(ctx); js != nil {
		c.ExtraFiles = append(c.ExtraFiles, js.files()...)
	}
	sandbox.RecordEnvDropped(ctx, policy, name)
	sandbox.EmitShimHint(policy, name)
	if len(withheld) > 0 {
		slog.DebugContext(ctx, types.FormatDiagnostic(types.ProcSocketWithheld,
			"withheld magus socket pointer(s) from op subprocess (done regardless of sandbox.mode)"),
			"vars", withheld)
	}
	if opts.Stdin != "" {
		// Wrapped so the TTY branch can recover the text and replay it through the
		// pty master; a plain strings.Reader on a pty slave would never be read.
		c.Stdin = &stringReaderMarker{Reader: strings.NewReader(opts.Stdin), s: opts.Stdin}
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

	slog.DebugContext(ctx, "run.exec", "cmd", name, "args", args, "dir", c.Dir, "tty", opts.TTY)

	// Start and Wait rather than Run, so the pid is known to THIS goroutine before
	// the sampler is handed it. Behaviorally identical (Run is Start plus Wait);
	// the split exists only so nothing reads c.Process concurrently.
	sampler := newTreeSampler()
	started := sampler.follow
	if ruleset != nil {
		// This process's copy of the ruleset serves nothing once the launcher holds its
		// own; the defer covers a Start that failed.
		defer ruleset.Close()
		started = func(pid int) {
			_ = ruleset.Close()
			sampler.follow(pid)
		}
	}
	var runErr error
	if opts.TTY {
		runErr = runOnPTY(ctx, c, outW, &outBuf, opts, started)
		if ctx.Err() != nil {
			KillGroup(c) // reap grandchildren that ignored the graceful signal
		}
	} else {
		if runErr = c.Start(); runErr == nil {
			started(c.Process.Pid)
			runErr = group.wait()
		}
	}
	treePeak := sampler.stop()
	runErr = classifyMissingBinary(runErr, name, c.ProcessState != nil)

	res := ExecResult{}
	if c.ProcessState != nil {
		res.Started = true
		res.Code = c.ProcessState.ExitCode()
		// The larger of the two readings, because each is blind where the other
		// sees. rusage is exact for one process and never misses a spike, but folds
		// a subtree as a maximum; the sampler sums the whole tree but only at the
		// instants it looked. Taking the max is strictly better than either alone
		// and cannot report less than magus reported before sampling existed.
		res.MaxRSSBytes = max(maxRSSBytes(c.ProcessState), treePeak)
		// Report it upward as well as returning it. The caller that wants this
		// number is the shard planner, which is several layers away and has no
		// path to an individual ExecResult; the context collector is what
		// carries a target's high-water mark to it. A no-op when nothing
		// installed a collector, which is every call outside a target run.
		types.RecordPeakRSS(ctx, res.MaxRSSBytes)
	} else {
		res.Code = -1 // process never started (binary not found, permission denied, etc.)
	}
	if opts.Capture {
		// Redacted on the WHOLE buffer, which is both simpler and strictly more accurate
		// than a streaming wrapper: it sees the complete output at once, so a secret split
		// across two writes is still caught.
		//
		// A hold-back writer on c.Stdout/c.Stderr was removed: it truncated this value
		// (the flush ran at function return, after these lines read the buffer, so
		// `printf abcdef` came back empty) and swallowed unterminated output, so a child
		// prompting `Password: ` displayed nothing until it exited. The live stream is
		// redacted per write by the tap in internal/cache/capture.go instead; a secret
		// split across two writes to the TERMINAL is a documented limit.
		res.Stdout = secret.RedactString(ctx, outBuf.String())
		res.Stderr = secret.RedactString(ctx, errBuf.String())
	}
	// Surface ctx.Err() whenever cancelled, even if the process won the race and
	// exited 0, so callers can distinguish cancel from a clean finish. errors.Join
	// drops a nil runErr.
	if ctx.Err() != nil {
		runErr = errors.Join(ctx.Err(), runErr)
	}
	return res, runErr
}

// classifyMissingBinary tags a failure to START the process as MGS3003, the same code
// std/os.go's proc\which() already gives a Buzz script for the same condition. An op's
// tool going missing used to surface here as a bare exec error with no code and no
// docs link.
//
// Two shapes, and only the first is exec.ErrNotFound: a BARE name PATH lookup missed,
// and a PATH-FORM name that the exec syscall reports as ENOENT without LookPath running.
// Matching only the first left every path-form invocation unclassified, and std/magus.go
// re-execs magus by absolute path. started guards the ENOENT arm: a process that ran and
// exited can fail for its own reasons that wrap ENOENT.
//
// The wrap is transparent: errors.Is still reaches the underlying error, so callers
// matching exec.ErrNotFound keep working.
func classifyMissingBinary(err error, name string, started bool) error {
	switch {
	case err == nil, started:
		return err
	case errors.Is(err, exec.ErrNotFound):
		return types.WrapDiagnostic(types.ToolNotOnPath, err, "%q is not on PATH", name)
	case errors.Is(err, fs.ErrNotExist):
		return types.WrapDiagnostic(types.ToolNotOnPath, err, "%q does not exist", name)
	}
	return err
}

// ProcForwardVars never reach ordinary op subprocesses: the proc-server socket a magus
// child forwards to, the token that socket demands, and the address `magus server`
// listens on. Only a recursive magus is handed them.
var ProcForwardVars = []string{"MAGUS_PROC_SOCKET", "MAGUS_PROC_TOKEN", "MAGUS_SERVER_ADDRESS"}

// SandboxEnvVar carries a sandboxed run's mode to every child, so a nested magus runs
// under that mode or a stronger one: it is the sandbox.mode setting's own variable, and
// a nested magus refuses a flag that weakens it (MGS2010). childEnv sets it after the
// caller's overrides.
const SandboxEnvVar = "MAGUS_SANDBOX"

// command builds the Cmd that runs resolved as name. A confined child starts through
// the sandbox launcher, and ruleset is this process's copy of the descriptor it rides
// in, for the caller to close once the child has started. A policy the kernel does not
// confine runs the child plainly, under the binding checks and env allowlist alone.
func command(ctx context.Context, policy *sandbox.Policy, confined bool, name, resolved string, args []string) (c *exec.Cmd, ruleset *os.File, err error) {
	if !confined {
		if policy != nil {
			sandbox.RecordLaunch(ctx, 0, "unsupported")
		}
		c = exec.CommandContext(ctx, resolved, args...)
		c.Args[0] = name
		return c, nil, nil
	}
	start := time.Now()
	c, err = sandbox.Command(ctx, policy, resolved, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: confine %s: %w", name, err)
	}
	sandbox.RecordLaunch(ctx, time.Since(start).Seconds(), "applied")
	// Args ends with the command's own argv; its argv[0] is the name as the target wrote it.
	c.Args[len(c.Args)-len(args)-1] = name
	return c, c.ExtraFiles[0], nil
}

// jobserverFD is the descriptor a jobserver pipe appended to a child's ExtraFiles lands
// on: after the launcher's ruleset when the child is confined.
func jobserverFD(confined bool) int {
	if confined {
		return firstExtraFD + sandbox.LauncherFiles
	}
	return firstExtraFD
}

// childEnv layers self-reference variables and caller overrides over the base environment.
//
// Under a policy the base is its BaseEnv even when that is empty: a sandboxed child
// never falls back to the host's environment.
func childEnv(ctx context.Context, policy *sandbox.Policy, overrides []string) (env, withheld []string) {
	root := os.Environ()
	if policy != nil {
		root = policy.BaseEnv
	}
	root = environ.From(ctx).Apply(root)
	for _, name := range ProcForwardVars {
		if hasEnvVar(root, name) && !hasEnvVar(overrides, name) {
			withheld = append(withheld, name)
		}
	}
	env = withoutEnvVars(root, ProcForwardVars)
	// The ancestry is dropped from the base first, so a value inherited from whatever
	// started THIS process can never outlive the invocation that set it, which matters in
	// the server, where the process env belongs to nobody's invocation.
	env = withoutEnvVars(env, []string{AncestorsEnvVar})
	env = append(env, SelfVars(ctx)...)
	// Ahead of the overrides, so a target that sets MAKEFLAGS itself keeps its own.
	if js := jobserverFrom(ctx); js != nil {
		// An error here is a required policy the kernel cannot confine; Exec refuses
		// that child before it would start, so the answer is never used.
		confined, _ := policy.KernelConfines()
		env = append(env, js.environ(env, jobserverFD(confined))...)
	}
	env = append(env, overrides...)
	if policy != nil {
		// Last, after the caller's overrides, so a target cannot hand a child a weaker value.
		env = append(withoutEnvVars(env, []string{SandboxEnvVar}), SandboxEnvVar+"="+policy.Mode.String())
		for _, name := range nestedMagusVars {
			if v, ok := environ.Lookup(ctx, name); ok && !hasEnvVar(env, name) {
				env = append(env, name+"="+v)
			}
		}
	}
	return env, withheld
}

// nestedMagusVars are what a magus nested in a sandboxed run needs to act on the same
// cache and job store, and that the scrubbed BaseEnv drops: a different cache dir is a
// different cache and lease marker, and the state dir holds the job store the lease
// narrowing reads. A value the caller set wins; these locate, they do not confine.
//
// Not in the sandbox's env allowlist, which testkit also isolates tests with: a stray
// MAGUS_CACHE_DIR reaching a test is the failure that list exists to stop.
var nestedMagusVars = []string{"MAGUS_CACHE_DIR", "MAGUS_CACHE_WRITE_ENABLED", "XDG_STATE_HOME"}

// WithEnvOverlay gives ctx an environment overlay of its own; see environ.With. It is
// here so an entry point that already starts runs through this package need not import
// a second one to begin a run.
func WithEnvOverlay(ctx context.Context) context.Context { return environ.With(ctx) }

// LookPath resolves name against the PATH a child of this run would get, the same answer
// Exec acts on.
func LookPath(ctx context.Context, name string) (string, error) {
	env, _ := childEnv(ctx, sandbox.PolicyFromContext(ctx), nil)
	return lookPath(name, envValue(env, "PATH"))
}

// envValue is name's value in env, the last entry winning as it does for exec.Cmd.
func envValue(env []string, name string) string {
	prefix := name + "="
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], prefix); ok {
			return v
		}
	}
	return ""
}

// lookPath resolves name against path, the PATH the child will run with, rather than
// this process's: in the server one run's PATH is not another's (see environ.Overlay). A
// name holding a separator is returned as given, for exec to resolve against the dir.
func lookPath(name, path string) (string, error) {
	if runtime.GOOS == "windows" {
		// PATHEXT resolution is exec.LookPath's alone to get right.
		return exec.LookPath(name)
	}
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	for _, dir := range filepath.SplitList(path) {
		// A relative entry is skipped, as exec.LookPath refuses one with ErrDot.
		if !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

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

func hasEnvVar(env []string, name string) bool {
	prefix := name + "="
	for _, kv := range env {
		if kv == name || strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

// AncestorsEnvVar names the variable that carries an invocation's ancestry to every child
// process, oldest first. Inherited like MAGUS_LEVEL, so a magus reached through a shell
// script (or through several) still knows which invocations it is running underneath.
const AncestorsEnvVar = "MAGUS_INVOCATION_ANCESTORS"

// SelfVars returns the binary path, recursion depth, and invocation ancestry for child
// magus processes.
//
// The first two are read from this process's environment because they describe the
// PROCESS; the ancestry is read from ctx because it describes the INVOCATION, and the
// server runs many of those in one process.
func SelfVars(ctx context.Context) []string {
	out := make([]string, 0, 3)
	if exe := magusExe(); exe != "" {
		out = append(out, "MAGUS="+exe)
	}
	out = append(out, "MAGUS_LEVEL="+strconv.Itoa(CurrentLevel()+1))
	if refs := types.InvocationAncestorsFromContext(ctx); len(refs) > 0 {
		out = append(out, AncestorsEnvVar+"="+strings.Join(refs, ","))
	}
	return out
}

// CurrentLevel returns this process's magus recursion depth.
func CurrentLevel() int {
	if n, err := strconv.Atoi(os.Getenv("MAGUS_LEVEL")); err == nil && n >= 0 {
		return n
	}
	return 0
}

// AncestorsFromEnv reads the ancestry a parent process passed down. This is the entry
// point for a fresh magus process: the refs belong to invocations in OTHER processes (or,
// under the server, to other goroutines), so the variable is the only thing that can carry
// them across the boundary.
func AncestorsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv(AncestorsEnvVar))
	if raw == "" {
		return nil
	}
	var out []string
	for _, ref := range strings.Split(raw, ",") {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

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
