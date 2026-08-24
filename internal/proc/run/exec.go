package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
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
}

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
	// treesample.go), which catches concurrency but only at the instants it looks.
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

// Exec runs a subprocess with the current sandbox policy and output writers.
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
		// kind, so this site does not repeat it - that centralization is the point.
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
	env, withheldDaemon := childEnv(ctx, policy, opts.Env)
	c.Env = env
	sandbox.RecordEnvDropped(ctx, policy)
	if len(withheldDaemon) > 0 {
		slog.DebugContext(ctx, types.FormatDiagnostic(types.DaemonSocketWithheld,
			"withheld magus daemon pointer(s) from op subprocess (done regardless of sandbox.enabled)"),
			"vars", withheldDaemon)
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
	var runErr error
	if opts.TTY {
		runErr = runOnPTY(c, outW, &outBuf, opts, sampler.follow)
	} else {
		if runErr = c.Start(); runErr == nil {
			sampler.follow(c.Process.Pid)
			runErr = c.Wait()
		}
	}
	treePeak := sampler.stop()
	runErr = classifyMissingBinary(runErr, name, c.ProcessState != nil)
	if ctx.Err() != nil {
		KillGroup(c) // reap grandchildren that ignored the graceful signal
	}

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

// DaemonForwardVars never reach ordinary op subprocesses.
var DaemonForwardVars = []string{"MAGUS_DAEMON_SOCKET", "MAGUS_DAEMON_ADDRESS"}

// childEnv layers self-reference variables and caller overrides over the base environment.
func childEnv(ctx context.Context, policy *sandbox.Policy, overrides []string) (env, withheld []string) {
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
	// The ancestry is dropped from the base first, so a value inherited from whatever
	// started THIS process can never outlive the invocation that set it - which matters in
	// the daemon, where the process env belongs to nobody's invocation.
	env = withoutEnvVars(env, []string{AncestorsEnvVar})
	env = append(env, SelfVars(ctx)...)
	env = append(env, overrides...)
	return env, withheld
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
// script - or through several - still knows which invocations it is running underneath.
const AncestorsEnvVar = "MAGUS_INVOCATION_ANCESTORS"

// SelfVars returns the binary path, recursion depth, and invocation ancestry for child
// magus processes.
//
// The first two are read from this process's environment because they describe the
// PROCESS; the ancestry is read from ctx because it describes the INVOCATION, and the
// daemon runs many of those in one process.
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
// under the daemon, to other goroutines), so the variable is the only thing that can carry
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
