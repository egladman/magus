package bindings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/service/identity"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// commandOpts carries the per-invocation options a magusfile passes to a command
// spell target (e.g. go.build({...})). cwd is the directory to run in (empty means
// the process cwd, "."). args append as raw tokens after the target's base command -
// so a build the op's bare base can't express (custom -ldflags, -o, a single package)
// is reachable through the spell rather than proc.exec. env overlays the subprocess
// environment (KEY=value; later entries win per Go's exec duplicate-key rule). hasArgs
// distinguishes "no args key" (fall back to project.ExtraArgs) from an explicit empty
// list; consulted only on the Buzz path. ignoreDirs are the issuing spell's own
// mgs_listIgnoreDirs, threaded through so a Command.Sources expansion (see
// runSourcedCommand) inherits them instead of the op re-declaring them. refs
// adds op-specific runner-computed values (e.g. a per-run cache destination) to
// what a $NAME token in Bin/Args may resolve to, alongside run.SelfVars - see
// resolveRunnerRefs. It is deliberately separate from env: env can carry a
// resolved Secret, and a Secret must never be reachable through Args.
type commandOpts struct {
	op         string // the op name, for error attribution; "" only on paths with no name to give
	cwd        string
	args       []string
	env        map[string]string
	stdin      string
	hasArgs    bool
	ignoreDirs []string
	refs       map[string]string
}

// runCommand runs tgt.Bin as a subprocess with the base argv as reshaped by the
// active charms (see resolveCharmArgs). Empty Cmd is a no-op. opts.cwd defaults to
// "." (the process cwd). Write-mode rides along as the "rw" charm on ctx, so no
// separate write flag is needed.
func runCommand(ctx context.Context, tgt spells.Op, opts commandOpts) (run.ExecResult, error) {
	if tgt.Bin == "" {
		return run.ExecResult{}, nil
	}
	dir := opts.cwd
	if dir == "" {
		dir = "."
	}
	// Resolve a relative dir against the context working directory (the project dir
	// the magusfile runner set via std.WithCwd), matching proc.exec's resolvePath. Without
	// this, a spell op invoked from a subproject target (e.g. go["go-run"] from docs/)
	// would run in the process cwd, not the project dir, so its relative paths would
	// miss. Absolute dirs pass through unchanged (the scip op passes one).
	if !filepath.IsAbs(dir) {
		base, err := std.EffectiveCwd(ctx)
		if err != nil {
			return run.ExecResult{}, err
		}
		dir = filepath.Join(base, dir)
	}
	args, err := resolveCharmArgs(ctx, tgt.Args, tgt.Charms)
	if err != nil {
		return run.ExecResult{}, err
	}
	// Warn on the real execution path only (not the describe/render path, which
	// resolveCharmArgs also serves and which surfaces conflicts in its own output).
	warnCharmConflicts(ctx, tgt.Args, tgt.Charms)
	args = append(args, opts.args...)
	// A static op is resolved to {bin, args} ONCE, with no Target (see recordOp in
	// internal/spellruntime/resolve.go), so it cannot compute a value only the
	// runner knows (its own binary path, a per-run cache destination) - which is
	// why a spell used to shell out to `sh -c` just to let the shell expand a
	// variable magus itself set ($MAGUS et al). Resolving a bare $NAME token here,
	// against exactly what the runner controls for this invocation, replaces that
	// shell with no shell at all.
	bin, args, err := resolveRunnerRefs(opts.op, tgt.Bin, args, runnerRefs(ctx, opts))
	if err != nil {
		return run.ExecResult{}, err
	}
	// Secrets resolve HERE, in the one function every command spawn funnels through,
	// so `magus run publish` (dispatchOp) and a magusfile body's `npm.publish(ctx)`
	// (runBuzzCommand) inject the same env. Resolving in dispatchOp alone made the
	// same op spawn with its secret on one path and silently without it on the
	// other. Service ops never reach this: decode rejects secrets on them.
	if len(tgt.Secrets) > 0 {
		env, err := resolveSecretEnv(ctx, opts.op, tgt.Secrets, opts.env)
		if err != nil {
			return run.ExecResult{}, err
		}
		opts.env = env
	}
	// A service op reached as a dependency is supervised in the background (started,
	// readiness-gated, deduped by fingerprint) instead of forked to completion, which
	// would block the run forever. A directly-run service (no supervisor active) falls
	// through and foregrounds, blocking as intended (Ctrl-C stops it).
	if tgt.IsService() && tgt.Service != nil {
		svc := spells.Service{
			Command:   spells.Command{Bin: bin, Args: args},
			Readiness: tgt.Service.Readiness,
			Stop:      tgt.Service.Stop,
			Idle:      tgt.Service.Idle,
			Distinct:  tgt.Service.Distinct,
		}
		// Scoped to the workspace: the daemon hosts services for every workspace on
		// the machine, so a bare fingerprint would share one instance across them.
		var root string
		if ws := types.WorkspaceFromContext(ctx); ws != nil {
			root = ws.Root()
		}
		if handled, serr := service.TrySupervise(ctx, identity.InstanceKey(root, svc), svc); handled {
			return run.ExecResult{}, serr
		}
	}
	// Failure advice tees a bounded TAIL of each stream so a non-zero exit can be
	// classified into a next step. Teeing rather than capturing keeps the tool's output
	// streaming live, which is the whole point of a non-capture op; the cost is paid only
	// by an op that actually declares hints.
	//
	// ONE TAIL PER STREAM, deliberately. os/exec drives stdout and stderr with a copy
	// goroutine each, so a shared buffer would hold an arbitrary interleaving of two
	// documents that never existed as one - splitting a real message with a chunk of the
	// other stream, or abutting two chunks into a substring that appeared in neither. A
	// shared buffer would also make the two compete for one budget, so stdout progress
	// chatter could evict the stderr error the advice exists to catch.
	//
	// The tail is FIRST in each MultiWriter: it cannot fail, and MultiWriter aborts on the
	// first error, so putting the real stream first would let a broken pipe silently stop
	// feeding the classifier.
	var outTail, errTail *outputTail
	if len(tgt.Hints) > 0 {
		outTail, errTail = newOutputTail(hintTailBytes), newOutputTail(hintTailBytes)
		stdout, stderr := run.OutputWriters(ctx)
		ctx = run.WithOutputWriters(ctx, io.MultiWriter(outTail, stdout), io.MultiWriter(errTail, stderr))
	}
	var res run.ExecResult
	if len(tgt.Sources) == 0 {
		res, err = execCommand(ctx, dir, bin, args, opts.env, opts.stdin, tgt.Capture)
	} else {
		res, err = runSourcedCommand(ctx, tgt, bin, dir, args, opts)
	}
	// A REAL failure only: the process started and exited non-zero, and nothing cancelled
	// it. run.Exec joins ctx.Err() into its error, so gating on err alone would advise a
	// user who pressed Ctrl-C to go and authenticate, diagnosing a failure that never
	// happened.
	if outTail != nil && res.Started && res.Code != 0 && ctx.Err() == nil {
		// Each stream is matched on its own; see the tee comment above. res.Stdout/Stderr
		// are deliberately NOT consulted - a capturing op streams through these same tees
		// (run.Exec buffers on top of the writers rather than instead of them), so reading
		// the result too would only add a duplicate and an unbounded copy of the whole log.
		if advice := adviceFor(tgt.Hints, outTail.String(), errTail.String()); advice != "" {
			// Through the run's own stderr writer, not os.Stderr. That writer is the tap
			// that mirrors into the persisted log and the output ref, so advice printed
			// around it is invisible to exactly the reader it is for - someone reading a
			// CI failure after the fact. It also keeps the advice attributed and ordered
			// with the target's own output when several projects run at once.
			_, stderr := run.OutputWriters(ctx)
			interactive.Emit(stderr, advice)
		}
	}
	return res, err
}

// runnerRefPattern matches a Bin or Args token that is a REFERENCE to a
// runner-computed value: a bare $NAME with no other characters - the same
// $VAR syntax a spell already wrote inside an `sh -c` one-liner, just without
// the shell to expand it. It deliberately does NOT match ${NAME} or a $NAME
// embedded inside a longer string ("--output=$NAME"): Buzz's own string
// interpolation already owns {...}, and a substring match would leave where a
// reference starts and ends ambiguous. A whole token is unambiguous and is
// exactly the shape every current use needs ("$MAGUS", "$MAGUS_SYMBOL_INDEX").
var runnerRefPattern = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`)

// runnerRefs returns the values a static op's Bin/Args may reference via a bare
// $NAME token (see resolveRunnerRefs): run.SelfVars for this invocation (MAGUS,
// MAGUS_LEVEL, the ancestor chain - the same values every spell subprocess
// already receives in its own environment) plus whatever opts.refs adds
// explicitly for one op (e.g. the per-run cache destination dispatchOp sets for
// the scip op via MAGUS_SYMBOL_INDEX). It is NOT the process environment or
// the sandbox's BaseEnv, and it is NOT commandOpts.env: env can carry a
// resolved Secret (runCommand's tgt.Secrets), and Secrets exists precisely so
// that value never reaches Args - folding env in here would defeat that the
// moment a spell wrote $A_SECRET_NAME.
func runnerRefs(ctx context.Context, opts commandOpts) map[string]string {
	refs := make(map[string]string, len(opts.refs)+2)
	for _, kv := range run.SelfVars(ctx) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			refs[k] = v
		}
	}
	for k, v := range opts.refs {
		refs[k] = v
	}
	return refs
}

// resolveRunnerRefs replaces every token in bin/args matching runnerRefPattern
// with its value in refs. A token that is not shaped like a reference passes
// through unchanged. A token that IS shaped like one but is absent from refs is
// a hard error naming the op and the reference: this is deliberately not "" on
// a miss - a silently empty substitution would turn `--output` (its value
// dropped) into a confusing downstream tool failure instead of a diagnosis
// naming the actual problem, right where the mistake was made.
//
// This is NOT shell expansion: it never reads the process environment, never
// interpolates inside a larger string, and it is exec-time engine behavior, not
// a text substitution a spell could observe or a charm could patch around.
func resolveRunnerRefs(opName, bin string, args []string, refs map[string]string) (string, []string, error) {
	resolve := func(tok string) (string, error) {
		if !runnerRefPattern.MatchString(tok) {
			return tok, nil
		}
		if v, ok := refs[tok[1:]]; ok {
			return v, nil
		}
		where := ""
		if opName != "" {
			where = fmt.Sprintf("op %q: ", opName)
		}
		return "", fmt.Errorf("spell: %s%q is not a value the runner provides", where, tok)
	}
	rbin, err := resolve(bin)
	if err != nil {
		return "", nil, err
	}
	rargs := make([]string, len(args))
	for i, a := range args {
		rargs[i], err = resolve(a)
		if err != nil {
			return "", nil, err
		}
	}
	return rbin, rargs, nil
}

// hintTailBytes bounds the output kept per stream for classification. A tool's failure
// explanation is the last thing it prints, and holding a whole build log in memory to
// classify one exit code is the wrong trade - so this keeps the tail and forgets the rest.
const hintTailBytes = 8 << 10

// adviceFor returns the Advise of the first declared hint whose Contains appears in any of
// sources, or "" when none matches. Declaration order is the precedence, so a spell author
// orders specific before general and reads the outcome off the file rather than guessing at
// a scoring rule.
//
// Sources are matched INDEPENDENTLY rather than joined: a Contains spanning the seam of two
// separate streams would fire on text that appeared in neither of them.
func adviceFor(hints []spells.Hint, sources ...string) string {
	for _, h := range hints {
		// An empty Contains matches every string, so it would advise on every failure of
		// this command. decodeCommand rejects that, which covers every spell-authored
		// value; this guards one built in Go, where nothing does.
		if h.Contains == "" || h.Advise == "" {
			continue
		}
		for _, src := range sources {
			if strings.Contains(src, h.Contains) {
				return h.Advise
			}
		}
	}
	return ""
}

// outputTail keeps the LAST limit bytes written to it and discards the rest.
//
// It is written from a subprocess copy goroutine, so Write must never fail: it drops old
// bytes rather than growing, and reports every write as fully accepted so the MultiWriter
// it sits in never short-circuits the real output stream on its account.
//
// The bytes it holds are pre-redaction - the capture tap redacts inside its own Write, and
// MultiWriter hands each writer the same original slice. Nothing here is ever printed (only
// the spell author's own Advise text is), and the buffer dies with the call, but it is a
// window where resolved secrets exist outside the redaction boundary and that is worth
// knowing before anything is added that surfaces it.
type outputTail struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

// newOutputTail returns a tail bounded to limit bytes. A zero limit would make Write
// discard everything and advice silently never fire, so it is floored rather than trusted.
func newOutputTail(limit int) *outputTail {
	if limit <= 0 {
		limit = hintTailBytes
	}
	return &outputTail{limit: limit}
}

func (t *outputTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *outputTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// resolveCharmArgs reshapes base by the charms active on ctx. Each active charm
// contributes an RFC 6902 JSON Patch over the argv; the patches are concatenated in
// sorted-name order (so the result is deterministic and immune to CLI activation order
// or duplicate charms) and applied as one sequential patch. Because charms are
// element-level (no root replace), disjoint edits compose freely; overlapping
// positions resolve by name order. The result is always a fresh slice (callers may
// mutate it; base is the shared cached spec).
func resolveCharmArgs(ctx context.Context, base []string, charms map[string]spells.Charm) ([]string, error) {
	var activeNames []string
	for name := range charms {
		if types.HasCharm(ctx, name) {
			activeNames = append(activeNames, name)
		}
	}
	return spellruntime.ApplyCharms(base, charms, activeNames)
}

// charmConflictWarned dedups the run-time conflict warning: the same overridden
// charm on the same command recurs across every project a target fans out to, so one
// warning per unique conflict per process is enough.
var charmConflictWarned sync.Map // signature string -> struct{}

// warnCharmConflicts emits a one-time soft warning when two active charms edit the
// same argv position and one silently overrides the other (the winner decided by
// alphabetical name, so the loser has no effect). It never blocks the run - the
// command still resolves deterministically - but an author almost never means to
// declare a charm whose edit is thrown away, so magus says so.
func warnCharmConflicts(ctx context.Context, base []string, charms map[string]spells.Charm) {
	var activeNames []string
	for name := range charms {
		if types.HasCharm(ctx, name) {
			activeNames = append(activeNames, name)
		}
	}
	conflicts, err := spellruntime.Conflicts(base, charms, activeNames)
	if err != nil {
		return
	}
	for _, c := range conflicts {
		winner := c.OverriddenBy
		if winner == "" {
			winner = "another active charm"
		}
		sig := strings.Join(base, "\x00") + "|" + c.Name + ">" + winner
		if _, seen := charmConflictWarned.LoadOrStore(sig, struct{}{}); seen {
			continue
		}
		slog.WarnContext(ctx, "magus: an active charm is overridden by another and has no effect",
			"charm", c.Name,
			"overridden_by", winner,
			"hint", fmt.Sprintf("charms %q and %q both edit the same argument; %q wins by name order, so %q does nothing here. Drop one, or make their edits disjoint.", c.Name, winner, winner, c.Name))
	}
}

// directMagusBinaryWarnOnce is process-global so the "use magus.cmd/..." hint fires
// at most once per process, not once per command - avoids log spam in a wide run.
var directMagusBinaryWarnOnce sync.Once

// execCommand runs cmd with args in dir, inheriting stdio and sandbox policy. When
// env is non-empty it overlays the base environment (the sandbox baseline when
// present, else the process env); later entries win per Go's exec duplicate-key rule.
func execCommand(ctx context.Context, dir, cmd string, args []string, env map[string]string, stdin string, capture bool) (run.ExecResult, error) {
	if filepath.Base(cmd) == "magus" {
		directMagusBinaryWarnOnce.Do(func() {
			slog.WarnContext(ctx, "magus: command spell target called with 'magus' binary",
				"hint", "use magus.cmd(...) or a typed magus.run/describe/insight/doctor(...) instead; contextual cwd, version-pinned, no arg-quoting issues")
		})
	}
	// Sort the per-call env overrides so the subprocess environment is deterministic
	// regardless of map iteration order; run.Exec applies them over the sandbox
	// BaseEnv (later entries win).
	var overrides []string
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		overrides = make([]string, 0, len(env))
		for _, k := range keys {
			overrides = append(overrides, k+"="+env[k])
		}
	}
	res, err := run.Exec(ctx, cmd, args, run.ExecOptions{Dir: dir, Env: overrides, Stdin: stdin, Capture: capture})
	if err != nil && errors.Is(err, types.ExecDenied) {
		return res, err // sandbox exec denial: surface the diagnostic verbatim
	}
	// A cancelled run kills this child, and a killed process has no verdict of its
	// own: ExitCode() reports -1, which the floor below would turn into "<cmd> exited
	// 1" - indistinguishable from the tool genuinely failing, printed with a
	// `reproduce:` line that does not reproduce it and (because a killed child writes
	// no stderr) no diagnostic to explain it. run.Exec already joins ctx.Err() for
	// exactly this; propagate it instead of blaming the tool. A child that reached a
	// real exit code before the kill landed still reports that code.
	if ctx.Err() != nil && (!res.Started || res.Code <= 0) {
		if err != nil {
			return res, err // already carries ctx.Err(), joined by run.Exec
		}
		return res, ctx.Err()
	}
	if !res.Started {
		if err != nil {
			// A process that never started has no exit code of its own - fabricating
			// "%s exited %d" here would discard run.Exec's classified error (e.g. the
			// MGS3003 tool-not-on-PATH diagnostic) and print a verdict on a tool that
			// never ran. Mirrors the cancellation branch above: propagate, don't invent.
			return res, err
		}
		return res, fmt.Errorf("%s exited %d", cmd, 1) // start failure with no error to propagate
	}
	if res.Code != 0 {
		// cmd is the executable that was exec'd (markdownlint, go, tsc), not a spell:
		// a spell is the adapter that chose to run it. Calling it one taught readers
		// a wrong mapping and sent them looking for a spell by the tool's name.
		return res, fmt.Errorf("%s exited %d", cmd, res.Code)
	}
	return res, nil
}

// sourcesBatchLimit caps how many files one batched Sources invocation carries in
// its argv. Mirrors gitTrackedBatch's approach (vcs/git.go): pick a round count
// comfortably under the OS's argv/environment limit rather than compute an exact
// byte budget that varies with path length and environment size. ARG_MAX is
// roughly 2MB on Linux and 256KB-1MB on macOS (historically floored at 4KB by
// POSIX); even generously long project-relative paths (a few hundred bytes) keep
// 256 of them a couple of orders of magnitude under the smallest of those.
const sourcesBatchLimit = 256

// runSourcedCommand runs bin once per file tgt.Sources matches (tgt.SourcesEach)
// or in as few batches as fit under sourcesBatchLimit (the default), appending
// the matched files to baseArgs exactly where `find | xargs` used to place them:
// after the declared/charm-patched/extra args. It is the execution half of
// spells.Command.Sources; see that field's doc for the full design.
//
// A glob set that matches nothing runs bin ZERO times and reports success
// (ExecResult{}) rather than invoking it with no files or failing outright -
// this mirrors `xargs -r`'s "do not run the command on an empty input" and the
// --step gate's own skip-is-not-a-failure convention (run.Exec's
// StepActionSkip branch returns the same zero value for the same reason).
//
// Every batch/file still runs even after an earlier one exits non-zero:
// shellcheck/buzz --check across many files should report every file's
// failure in one run, the same as the `xargs` this replaces (which only
// aborts early on a command it could not run at all, not on an ordinary
// non-zero exit) - but the FIRST error is what the caller sees, so the op
// still fails overall. A process that never started (bad exec, sandbox
// denial) or a cancelled run stops the loop immediately instead: every
// remaining invocation would fail identically, or should not proceed.
func runSourcedCommand(ctx context.Context, tgt spells.Op, bin, dir string, baseArgs []string, opts commandOpts) (run.ExecResult, error) {
	files, err := cache.ExpandSources(tgt.Sources, dir, nil, opts.ignoreDirs)
	if err != nil {
		return run.ExecResult{}, err
	}
	if len(files) == 0 {
		return run.ExecResult{}, nil
	}

	var combined run.ExecResult
	var firstErr error
	for _, batch := range sourceBatches(files, tgt.SourcesEach) {
		args := append(append([]string(nil), baseArgs...), batch...)
		res, err := execCommand(ctx, dir, bin, args, opts.env, opts.stdin, tgt.Capture)
		combined.Started = combined.Started || res.Started
		if res.MaxRSSBytes > combined.MaxRSSBytes {
			combined.MaxRSSBytes = res.MaxRSSBytes
		}
		if tgt.Capture {
			combined.Stdout = joinNonEmpty(combined.Stdout, res.Stdout)
			combined.Stderr = joinNonEmpty(combined.Stderr, res.Stderr)
		}
		if err == nil {
			continue
		}
		if firstErr == nil {
			firstErr = err
			combined.Code = res.Code
		}
		if !res.Started || ctx.Err() != nil || errors.Is(err, types.ExecDenied) {
			return combined, firstErr
		}
	}
	return combined, firstErr
}

// sourceBatches groups files into invocations: one file per batch when each is
// true (xargs -n1), otherwise as few batches as fit under sourcesBatchLimit.
func sourceBatches(files []string, each bool) [][]string {
	if each {
		batches := make([][]string, len(files))
		for i, f := range files {
			batches[i] = []string{f}
		}
		return batches
	}
	batches := make([][]string, 0, (len(files)+sourcesBatchLimit-1)/sourcesBatchLimit)
	for start := 0; start < len(files); start += sourcesBatchLimit {
		batches = append(batches, files[start:min(start+sourcesBatchLimit, len(files))])
	}
	return batches
}

// joinNonEmpty concatenates a and b with a newline separator, skipping either
// side that is empty rather than leaving a stray blank line.
func joinNonEmpty(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n" + b
	}
}
