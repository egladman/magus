package queue

// This file runs the queue's hooks, each a command and its arguments with no shell
// between: the gate and the regeneration on each candidate, and the build tool's facts.
//
// Whatever a hook's process tree does is the change's: a normal non-zero exit, a death by
// signal (an OOM kill included) and a temporary failure that outlasts its retries are
// all a red gate or a refused regeneration. Only what the queue can prove is the
// machine's (the hook could not be started, or the queue itself was cancelled) is an
// error, which stops a partition; anything else would let a change stall its partition
// on every run by dying the right way.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/sandbox"
	magustypes "github.com/egladman/magus/types"
)

// ExitTempFail is EX_TEMPFAIL from sysexits.h: the hook could not run right now (a build
// tool's lock was held, say). It is run again, and a failure that outlasts the retries
// is the change's like any other.
const ExitTempFail = 75

// attempts bounds how often a hook exiting ExitTempFail is run.
const attempts = 3

// retryDelay is the wait before running a hook again after ExitTempFail.
var retryDelay = 5 * time.Second

// interruptGrace is how long a cancelled hook gets to stop before it is killed.
const interruptGrace = 30 * time.Second

// Command is a hook: a program and the arguments it always takes. The queue appends its
// inputs and runs it with no shell between, so nothing in it or them is expanded.
type Command []string

// ParseCommand reads flag's value as a command and its arguments, in sh's word syntax:
// quotes and backslashes group and escape, and nothing expands. Anything a shell would
// act on (a variable, a substitution, an operator, a redirection, a glob, a leading
// assignment, a comment) is refused with MGS3026, naming flag, since no shell runs it.
func ParseCommand(flag, line string) (Command, error) {
	refuse := func(what string) (Command, error) {
		return nil, magustypes.DiagnosticErrorf(magustypes.QueueHookNotACommand,
			"%s %q: %s, and a hook is a command and its arguments with no shell to act on it; put the line in a script and point %s at it",
			flag, line, what, flag)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX), syntax.KeepComments(true)).Parse(strings.NewReader(line), "")
	switch {
	case err != nil:
		return refuse("it does not parse as sh words (" + err.Error() + ")")
	case len(f.Stmts) == 0:
		return refuse("it names no command")
	case len(f.Stmts) != 1:
		return refuse("it holds more than one command")
	}
	st := f.Stmts[0]
	call, isCall := st.Cmd.(*syntax.CallExpr)
	switch {
	case len(st.Comments) > 0 || len(f.Last) > 0:
		return refuse("# starts a comment")
	case st.Background:
		return refuse("& runs it in the background")
	case st.Negated:
		return refuse("! negates it")
	case len(st.Redirs) > 0:
		return refuse(st.Redirs[0].Op.String() + " redirects it")
	case !isCall:
		if bc, ok := st.Cmd.(*syntax.BinaryCmd); ok {
			return refuse(bc.Op.String() + " joins two commands")
		}
		return refuse("it is a compound command")
	case len(call.Assigns) > 0:
		return refuse(call.Assigns[0].Name.Value + "= sets a variable")
	}
	for _, w := range call.Args {
		if what := shellSyntax(w.Parts, false); what != "" {
			return refuse(what)
		}
	}
	// Literal words only, so this is quote and escape removal: no ReadDir, no glob.
	cmd, err := expand.Fields(&expand.Config{}, call.Args...)
	if err != nil || len(cmd) != len(call.Args) {
		return refuse(fmt.Sprintf("its words do not read as literal text (%v)", err))
	}
	return cmd, nil
}

// shellSyntax names the first thing in parts a shell would expand, or "" when they are
// literal text.
func shellSyntax(parts []syntax.WordPart, quoted bool) string {
	for i, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if !quoted {
				if what := pattern(p.Value, i == 0); what != "" {
					return what
				}
			}
		case *syntax.SglQuoted:
			if p.Dollar {
				return "$'...' is a shell quote"
			}
		case *syntax.DblQuoted:
			if p.Dollar {
				return `$"..." is a shell quote`
			}
			if what := shellSyntax(p.Parts, true); what != "" {
				return what
			}
		case *syntax.ParamExp:
			return "$" + p.Param.Value + " is a variable"
		case *syntax.CmdSubst:
			return "$(...) is a command substitution"
		case *syntax.ArithmExp:
			return "$((...)) is arithmetic"
		default:
			return "it holds shell syntax"
		}
	}
	return ""
}

// pattern names an unescaped glob or brace character in an unquoted literal, or a
// tilde leading its word.
func pattern(lit string, leads bool) string {
	if leads && strings.HasPrefix(lit, "~") {
		return "~ is a home directory"
	}
	for i := 0; i < len(lit); i++ {
		switch c := lit[i]; {
		case c == '\\':
			i++
		case strings.IndexByte("*?[{", c) >= 0:
			return string(c) + " is a pattern"
		}
	}
	return ""
}

// hookCommand is one hook invocation: Command run with Args appended.
type hookCommand struct {
	Command Command
	Args    []string
	Dir     string
	// Sandbox is the base's config the hook runs under; see [HookEnv].
	Sandbox config.SandboxConfig
	// Scratch is the candidate's scratch directory, writable and holding TMPDIR; empty
	// for a facts hook, which runs in the base's own checkout.
	Scratch string
	Env     []string // set over what the sandbox passes from the queue's environment
	Stdin   string
	Stdout  io.Writer // nil discards
	Stderr  io.Writer // nil discards
	// Capture also returns stdout in the result, redacted.
	Capture bool
}

// Run runs c through magus's own process runner under c's sandbox policy, in a process
// group of its own. A cancelled context sends the group SIGTERM, so a build tool it
// started can stop cleanly, and kills it interruptGrace later. Whatever of the group
// outlives the hook is killed before Run returns, so no process a hook started runs on
// into the next hook or past the verdict it led to; a process that left the group
// itself (setsid) escapes this, and only the machine's own boundary, a CI job's, ends
// it. An argument [types.CheckUnit] refuses is an error, since Args follow Command's
// own.
func (c hookCommand) Run(ctx context.Context) (procrun.ExecResult, error) {
	if len(c.Command) == 0 {
		return procrun.ExecResult{}, errors.New("empty command hook")
	}
	for _, a := range c.Args {
		if err := types.CheckUnit(a); err != nil {
			return procrun.ExecResult{}, fmt.Errorf("%s hook: %w", c.Command[0], err)
		}
	}
	policy, err := c.policy()
	if err != nil {
		return procrun.ExecResult{}, err
	}
	stdout, stderr := c.Stdout, c.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	ctx = procrun.WithOutputWriters(sandbox.WithPolicy(ctx, policy), stdout, stderr)
	return procrun.Exec(ctx, c.Command[0], slices.Concat(c.Command[1:], c.Args), procrun.ExecOptions{
		Dir:         c.Dir,
		Env:         c.Env,
		Stdin:       c.Stdin,
		Capture:     c.Capture,
		CancelGrace: interruptGrace,
	})
}

// policy is the base's sandbox for a hook in c.Dir: its mode raised to at least
// best-effort, since a hook runs a change's code whatever the base asks, with c.Scratch
// read, write and exec and TMPDIR inside it. Exec because the scratch variables put tool
// caches there, and go run executes the binaries it caches in GOCACHE; the nested magus
// a hook runs stacks its own sandbox on this one, so a right withheld here is withheld
// from every child whatever that inner policy grants.
func (c hookCommand) policy() (*sandbox.Policy, error) {
	cfg := c.Sandbox
	if cfg.Mode.WeakerThan(magustypes.SandboxModeBestEffort) {
		cfg.Mode = magustypes.SandboxModeBestEffort
	}
	if c.Scratch == "" {
		return sandbox.FromConfig(c.Dir, "", cfg)
	}
	tmp := filepath.Join(c.Scratch, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	cfg.Allow = append(slices.Clone(cfg.Allow), config.SandboxAllowPath{Path: c.Scratch, Mode: "rwx"})
	return sandbox.FromConfigWithTempDir(c.Dir, "", tmp, cfg)
}

// pathLines is paths one per line, as a hook reads them on stdin. Git allows a line
// break in a path, which that protocol would read as two paths, so such paths are
// returned as refused and left out rather than carried.
func pathLines(paths []string) (lines string, refused []string) {
	kept := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.ContainsAny(p, "\n\r") {
			refused = append(refused, p)
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		return "", refused
	}
	return strings.Join(kept, "\n") + "\n", refused
}

// changeFailure is a hook failing on the change: what it says is about the change.
type changeFailure struct{ why string }

func (f changeFailure) Error() string { return f.why }

// runHook runs c, running it again after ExitTempFail. It returns a changeFailure for
// anything the hook's process tree did, and any other error only when the hook could
// not run.
func runHook(ctx context.Context, c hookCommand) (procrun.ExecResult, error) {
	for attempt := 1; ; attempt++ {
		res, err := c.Run(ctx)
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
		// The hook exited 0 and a process that left its group held its output past
		// the grace: the stray is the change's, and the exit is the verdict.
		case err == nil, errors.Is(err, exec.ErrWaitDelay) && res.Started:
			return res, nil
		default:
			return res, err
		}
		code := exit.ExitCode()
		switch {
		case code < 0:
			return res, changeFailure{"was killed (" + exit.String() + ")"}
		case code != ExitTempFail:
			return res, changeFailure{fmt.Sprintf("exited %d", code)}
		case attempt == attempts:
			return res, changeFailure{fmt.Sprintf("exited %d (temporary failure) %d times", ExitTempFail, attempts)}
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}

// ScratchVar is an environment variable the queue points into each hook's scratch
// directory, a directory private to the checkout the hook runs in: Name is set to Dir
// inside it. It keeps the caches of the tools a hook runs private to one candidate
// without the hook's command line saying so, which leaves that line one a person can
// run outside the queue as it stands.
type ScratchVar struct {
	Name string
	Dir  string // relative, inside the scratch directory
}

// ParseScratchVar reads "NAME=DIR". NAME is a shell variable name, and DIR a relative
// path that stays inside the scratch directory.
func ParseScratchVar(spec string) (ScratchVar, error) {
	name, dir, ok := strings.Cut(spec, "=")
	switch {
	case !ok || !isEnvName(name) || dir == "":
		return ScratchVar{}, fmt.Errorf("%q is not NAME=DIR", spec)
	case !filepath.IsLocal(dir):
		return ScratchVar{}, fmt.Errorf("%q: %s leaves the scratch directory", spec, dir)
	}
	return ScratchVar{Name: name, Dir: filepath.Clean(dir)}, nil
}

func isEnvName(s string) bool {
	for i, r := range s {
		switch {
		case r == '_', 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z':
		case i > 0 && '0' <= r && r <= '9':
		default:
			return false
		}
	}
	return s != ""
}

// HookEnv is the sandbox a hook runs in and what its environment adds.
type HookEnv struct {
	// Sandbox is the base's sandbox config, never a candidate's. Every hook runs under
	// the policy it builds, rooted at the hook's checkout, in its mode raised to at
	// least best-effort. So of the queue's own environment a hook gets only what the
	// sandbox gives a sandboxed child and the passthrough names, and where the kernel
	// has landlock its whole process tree is held to the policy's files. A credential
	// the passthrough names reaches every hook, which is the workspace's choice.
	Sandbox config.SandboxConfig
	// Scratch are pointed into each candidate's scratch directory.
	Scratch []ScratchVar
	// Fixed are NAME=VALUE assignments every hook takes as given, such as a
	// [CacheReadProxy]'s stand-ins.
	Fixed []string
}

// of creates each scratch variable under scratch and returns every assignment.
func (e HookEnv) of(scratch string) ([]string, error) {
	env := make([]string, 0, len(e.Scratch)+len(e.Fixed))
	for _, v := range e.Scratch {
		dir := filepath.Join(scratch, v.Dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		env = append(env, v.Name+"="+dir)
	}
	return append(env, e.Fixed...), nil
}

// CommandGate is a [types.Gate] running cmd in a checkout with the units appended as
// arguments and env added to its environment. Exit status 0 is green; anything else the
// hook's processes do is red. Its output goes to log, each line tagged with the commit
// gated and its change, or "base" for a commit gated as it stands; a nil log discards
// it.
func CommandGate(cmd Command, env HookEnv, log *HookLog) types.Gate {
	return commandGate{cmd: cmd, env: env, log: log}
}

type commandGate struct {
	cmd Command
	env HookEnv
	log *HookLog
}

func (g commandGate) Validate(ctx context.Context, cand types.Candidate, units []string) (types.GateResult, error) {
	env, err := g.env.of(cand.Scratch)
	if err != nil {
		return types.GateResult{}, fmt.Errorf("gate on `%s`: %w", short(cand.Commit), err)
	}
	of := "base"
	if cand.Change != "" {
		of = "#" + cand.Change
	}
	out := g.log.Prefixed("[" + short(cand.Commit) + " " + of + "] ")
	defer out.Close()
	_, err = runHook(ctx, hookCommand{Command: g.cmd, Args: units, Dir: cand.Dir, Sandbox: g.env.Sandbox, Scratch: cand.Scratch, Env: env, Stdout: out, Stderr: out})
	var failed changeFailure
	switch {
	case errors.As(err, &failed):
		return types.GateResult{Summary: "the gate " + failed.why}, nil
	case err != nil:
		return types.GateResult{}, fmt.Errorf("gate on `%s`: %w", short(cand.Commit), err)
	}
	return types.GateResult{Green: true}, nil
}

// CommandRegenerate is a [types.RegenerateFunc] running cmd in a checkout with the
// units appended as arguments, the generated paths on stdin, one per line, and hookEnv
// added to its environment. A failure of the hook's processes is a
// *[types.RefusedError] naming the paths: the change did not regenerate. So is a path
// holding a line break, which the hook is never run for.
func CommandRegenerate(cmd Command, hookEnv HookEnv, log *HookLog) types.RegenerateFunc {
	return func(ctx context.Context, r types.Regeneration) error {
		env, err := hookEnv.of(r.Scratch)
		if err != nil {
			return fmt.Errorf("regenerate %s: %w", r.Change.Label(), err)
		}
		stdin, refused := pathLines(r.Paths)
		if len(refused) > 0 {
			return &types.RefusedError{Reason: "the regeneration reads one path per line, and " + joinPaths(refused) + " holds a line break", Paths: refused}
		}
		out := log.Prefixed("[regenerate #" + r.Change.ID + "] ")
		defer out.Close()
		_, err = runHook(ctx, hookCommand{
			Command: cmd,
			Args:    r.Units,
			Dir:     r.Dir,
			Sandbox: hookEnv.Sandbox,
			Scratch: r.Scratch,
			Env:     env,
			Stdin:   stdin,
			Stdout:  out,
			Stderr:  out,
		})
		var failed changeFailure
		if errors.As(err, &failed) {
			return &types.RefusedError{Reason: "the regeneration " + failed.why, Paths: r.Paths}
		}
		return err
	}
}

// CommandFacts is [types.BuildFacts] from cmd, run in dir, for a build tool that has no
// Go implementation. The fact asked for is appended to cmd as one argument, and cmd
// prints one JSON object answering it; other keys are ignored, so a build tool's richer
// structured output can be the answer as it stands.
//
//	affected    stdin: the change's paths, one per line
//	            prints {"affected": [unit], "unbounded_by": why}; a missing "affected" is unbounded
//	outputs     stdin: paths, one per line
//	            prints {"outputs": [path], "updated": [path], "maintained": [path]}: the
//	            ones some target writes whole, the ones a target rewrites in place, and
//	            the ones the build tool rewrites itself on every run; a missing key
//	            names none
//	generation  stdin: {"outputs": [path], "changed": [path]}
//	            prints {"units": [unit], "code": [path], "unbounded": why}
//	all         stdin: empty
//	            prints {"units": [unit]}: how the build tool names every unit
//	auto_resolve  stdin: {"path": path, "base": text, "merged": text}, a conflicted
//	            source file's merge base content and the merge the queue settled
//	            prints {"auto_resolve": bool, "verdict": line}: whether the merge may
//	            go without a person, and the build tool's line on the path either way;
//	            a command that fails this query settles nothing
//
// A path holding a line break is never written: affected answers unbounded for it, and
// outputs leaves it unclassified, which is source.
//
// A failing command is an error, auto_resolve aside, since the hook reads only the base,
// never the change's code. Its environment is a gate's under env, less env.Scratch: facts
// run in dir, which has no scratch directory.
func CommandFacts(cmd Command, dir string, env HookEnv, log *HookLog) types.BuildFacts {
	return commandFacts{cmd: cmd, dir: dir, env: env, log: log}
}

type commandFacts struct {
	cmd Command
	dir string
	env HookEnv
	log *HookLog
}

func (f commandFacts) ask(ctx context.Context, query, label, stdin string, answer any) error {
	stderr := f.log.Prefixed("[" + label + "] ")
	defer stderr.Close()
	res, err := runHook(ctx, hookCommand{
		Command: f.cmd,
		Args:    []string{query},
		Dir:     f.dir,
		Sandbox: f.env.Sandbox,
		Env:     f.env.Fixed,
		Stdin:   stdin,
		Stderr:  stderr,
		Capture: true,
	})
	if err != nil {
		return fmt.Errorf("%s hook: %w", query, err)
	}
	if err := json.Unmarshal([]byte(res.Stdout), answer); err != nil {
		return fmt.Errorf("%s hook printed no JSON object: %w", query, err)
	}
	return nil
}

func (f commandFacts) Affected(ctx context.Context, c types.Change, paths []string) ([]string, string, error) {
	if len(paths) == 0 {
		return []string{}, "", nil
	}
	stdin, refused := pathLines(paths)
	if len(refused) > 0 {
		return nil, "the affected hook reads one path per line, and " + joinPaths(refused) + " holds a line break", nil
	}
	var ans struct {
		Affected    []string `json:"affected"`
		UnboundedBy string   `json:"unbounded_by"`
	}
	if err := f.ask(ctx, "affected", "affected #"+c.ID, stdin, &ans); err != nil {
		return nil, "", err
	}
	if ans.Affected == nil && ans.UnboundedBy == "" {
		return nil, "the affected hook printed no affected set", nil
	}
	return ans.Affected, ans.UnboundedBy, nil
}

func (f commandFacts) Classify(ctx context.Context, paths []string) (map[string]types.Writes, error) {
	out := map[string]types.Writes{}
	// A path the hook cannot be asked about stays unclassified, which is source: its
	// conflicts are the author's, and a review sees it.
	stdin, _ := pathLines(paths)
	if stdin == "" {
		return out, nil
	}
	var ans struct {
		Outputs    []string `json:"outputs"`
		Updated    []string `json:"updated"`
		Maintained []string `json:"maintained"`
	}
	if err := f.ask(ctx, "outputs", "outputs", stdin, &ans); err != nil {
		return nil, err
	}
	mark := func(answered []string, set func(*types.Writes)) {
		for _, p := range answered {
			if slices.Contains(paths, p) {
				writes := out[p]
				set(&writes)
				out[p] = writes
			}
		}
	}
	mark(ans.Outputs, func(w *types.Writes) { w.Output = true })
	mark(ans.Updated, func(w *types.Writes) { w.Updated = true })
	mark(ans.Maintained, func(w *types.Writes) { w.Maintained = true })
	return out, nil
}

func (f commandFacts) AutoResolvable(ctx context.Context, path string, base, merged []byte) (string, bool, error) {
	in, err := json.Marshal(map[string]string{"path": path, "base": string(base), "merged": string(merged)})
	if err != nil {
		return "", false, err
	}
	var ans struct {
		AutoResolve bool   `json:"auto_resolve"`
		Verdict     string `json:"verdict"`
	}
	// A build tool that answers no auto_resolve settles nothing: the file stays the
	// author's conflict, as it was before the query existed.
	if err := f.ask(ctx, "auto_resolve", "auto_resolve", string(in), &ans); err != nil {
		return path + ": the facts command answered no auto_resolve (" + err.Error() + ")", false, nil //nolint:nilerr // settles nothing, the safe side
	}
	if ans.Verdict == "" {
		ans.Verdict = path + ": the auto_resolve hook gave no verdict"
	}
	return ans.Verdict, ans.AutoResolve, nil
}

func (f commandFacts) Generation(ctx context.Context, outputs, changed []string) (types.Generation, error) {
	in, err := json.Marshal(map[string][]string{"outputs": outputs, "changed": changed})
	if err != nil {
		return types.Generation{}, err
	}
	var ans struct {
		Units     []string `json:"units"`
		Code      []string `json:"code"`
		Unbounded string   `json:"unbounded"`
	}
	if err := f.ask(ctx, "generation", "generation", string(in), &ans); err != nil {
		return types.Generation{}, err
	}
	return types.Generation{Units: ans.Units, Code: ans.Code, Unbounded: ans.Unbounded}, nil
}

func (f commandFacts) AllUnits(ctx context.Context) ([]string, error) {
	var ans struct {
		Units []string `json:"units"`
	}
	if err := f.ask(ctx, "all", "all", "", &ans); err != nil {
		return nil, err
	}
	if len(ans.Units) == 0 {
		return nil, errors.New("all hook named no unit")
	}
	return ans.Units, nil
}

// HookLog interleaves the output of concurrent hooks a whole line at a time, each line
// tagged with its hook's prefix. A nil *HookLog discards.
type HookLog struct {
	mu sync.Mutex
	w  io.Writer
}

// NewHookLog writes to w.
func NewHookLog(w io.Writer) *HookLog { return &HookLog{w: w} }

// Prefixed returns a writer tagging each of its lines with prefix. Close writes a final
// line that ended without a newline.
func (l *HookLog) Prefixed(prefix string) io.WriteCloser {
	if l == nil {
		return nopCloser{io.Discard}
	}
	return &prefixed{log: l, prefix: prefix}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type prefixed struct {
	log    *HookLog
	prefix string
	buf    []byte
}

func (p *prefixed) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		err := p.line(p.buf[:i])
		p.buf = p.buf[i+1:]
		if err != nil {
			return len(b), err
		}
	}
}

func (p *prefixed) Close() error {
	if len(p.buf) == 0 {
		return nil
	}
	err := p.line(p.buf)
	p.buf = nil
	return err
}

func (p *prefixed) line(b []byte) error {
	p.log.mu.Lock()
	defer p.log.mu.Unlock()
	_, err := fmt.Fprintf(p.log.w, "%s%s\n", p.prefix, b)
	return err
}
