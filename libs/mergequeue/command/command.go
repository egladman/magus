// Package command runs the merge queue's hooks as shell command lines: the gate and the
// regeneration on each staging commit, and the affected hook during planning.
//
// A hook's failure is sorted by what it says about the change. A normal non-zero exit
// is the change's: a red gate, or a regeneration refused. [ExitTempFail] is retried and
// then reported as the machine's, and so is a death by signal (an OOM kill, a runner
// shutting down), so no author is kicked back for a machine failure.
package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/libs/mergequeue"
)

// Environment the queue sets for its hooks.
const (
	EnvChange     = "MERGEQUEUE_CHANGE"      // the change's id
	EnvHead       = "MERGEQUEUE_HEAD"        // the change's head commit
	EnvBase       = "MERGEQUEUE_BASE"        // the branch the queue merges into
	EnvBaseCommit = "MERGEQUEUE_BASE_COMMIT" // the commit every stage is built on
	EnvOnto       = "MERGEQUEUE_ONTO"        // the commit this stage was built onto
	EnvStage      = "MERGEQUEUE_STAGE"       // the staging commit being gated
)

// scrubbed are credentials no hook sees: a gate runs the changes' code, and a token in
// its environment is a token handed to every author in the queue.
var scrubbed = []string{
	"MERGEQUEUE_TOKEN", "GITHUB_TOKEN", "GH_TOKEN",
	"ACTIONS_RUNTIME_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL",
}

// ExitTempFail is EX_TEMPFAIL from sysexits.h: the hook could not run right now (a build
// tool's lock was held, say), which says nothing about the change.
const ExitTempFail = 75

// attempts bounds how often a hook exiting ExitTempFail is run.
const attempts = 3

// retryDelay is the wait before running a hook again after ExitTempFail.
var retryDelay = 5 * time.Second

// interruptGrace is how long an interrupted hook gets to stop before it is killed.
const interruptGrace = 30 * time.Second

// Command is one hook invocation: a shell command line run with `sh -c`.
type Command struct {
	Line   string
	Dir    string
	Env    []string // added to the queue's environment, less the scrubbed credentials
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run runs c in a process group of its own. A cancelled context interrupts the whole
// group rather than killing it, so a build tool it started can stop cleanly; the group
// is killed interruptGrace later.
func (c Command) Run(ctx context.Context) error {
	if strings.TrimSpace(c.Line) == "" {
		return errors.New("empty command hook")
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Line)
	cmd.Dir = c.Dir
	cmd.Env = append(environ(), c.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	isolate(cmd)
	var killer *time.Timer
	cmd.Cancel = func() error {
		killer = time.AfterFunc(interruptGrace, func() { kill(cmd) })
		return interrupt(cmd)
	}
	cmd.WaitDelay = interruptGrace + time.Second
	err := cmd.Run()
	if killer != nil {
		// A pid, and so a group id, is reused once its process is reaped.
		killer.Stop()
	}
	return err
}

func environ() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(scrubbed, name)
	})
}

// changeFailure is a hook's normal non-zero exit: what it says is about the change.
type changeFailure struct{ code int }

func (f changeFailure) Error() string { return fmt.Sprintf("exited %d", f.code) }

// run runs c, running it again after ExitTempFail. It returns a changeFailure for a
// normal failing exit and any other error when the machine failed.
func run(ctx context.Context, c Command) error {
	for attempt := 1; ; attempt++ {
		err := c.Run(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if err == nil || !errors.As(err, &exit) {
			return err
		}
		switch code := exit.ExitCode(); {
		case code == ExitTempFail:
			if attempt == attempts {
				return fmt.Errorf("`%s` exited %d (temporary failure) %d times", c.Line, ExitTempFail, attempts)
			}
		case code < 0 || signalled(code):
			return fmt.Errorf("`%s` was killed (%v): the machine failed, not the change", c.Line, err)
		default:
			return changeFailure{code}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}

// signalled reports whether a shell's exit status says its command died of SIGINT,
// SIGKILL or SIGTERM: a shell reports a signal death as 128 plus the signal.
func signalled(code int) bool { return code == 130 || code == 137 || code == 143 }

// Gate is a [mergequeue.Gate] running line in each staging commit's checkout. Exit
// status 0 is green and a normal failing exit is red. line's output goes to log, each
// line tagged with its stage; a nil log discards it.
func Gate(line string, plan mergequeue.Plan, log *Log) mergequeue.Gate {
	return gate{line: line, base: plan.Base, baseCommit: plan.BaseCommit, log: log}
}

type gate struct {
	line, base, baseCommit string
	log                    *Log
}

func (g gate) Validate(ctx context.Context, s mergequeue.Stage, onto string, c mergequeue.Change) (mergequeue.GateResult, error) {
	label := "[" + short(s.Commit) + " #" + c.ID + "] "
	out := g.log.Prefixed(label)
	defer out.Close()
	err := run(ctx, Command{
		Line: g.line,
		Dir:  s.Dir,
		Env: []string{EnvChange + "=" + c.ID, EnvHead + "=" + c.Head, EnvBase + "=" + g.base,
			EnvBaseCommit + "=" + g.baseCommit, EnvOnto + "=" + onto, EnvStage + "=" + s.Commit},
		Stdout: out,
		Stderr: out,
	})
	var failed changeFailure
	switch {
	case errors.As(err, &failed):
		return mergequeue.GateResult{Summary: fmt.Sprintf("`%s` exited %d on the staging commit `%s`; the queue log's lines prefixed %q name what failed.",
			g.line, failed.code, short(s.Commit), strings.TrimSpace(label))}, nil
	case err != nil:
		return mergequeue.GateResult{}, fmt.Errorf("gate on the staging commit `%s`: %w", short(s.Commit), err)
	}
	return mergequeue.GateResult{Green: true}, nil
}

// Regenerate is a [mergequeue.RegenerateFunc] running line in a stage's checkout with
// the derived paths on stdin, one per line. A normal failing exit is a
// *[mergequeue.RefusedError]: the change's code did not regenerate.
func Regenerate(line string, plan mergequeue.Plan, log *Log) mergequeue.RegenerateFunc {
	return func(ctx context.Context, dir, onto string, c mergequeue.Change, paths []string) error {
		label := "[regenerate #" + c.ID + "] "
		out := log.Prefixed(label)
		defer out.Close()
		err := run(ctx, Command{
			Line: line,
			Dir:  dir,
			Env: []string{EnvChange + "=" + c.ID, EnvHead + "=" + c.Head, EnvBase + "=" + plan.Base,
				EnvBaseCommit + "=" + plan.BaseCommit, EnvOnto + "=" + onto},
			Stdin:  strings.NewReader(strings.Join(paths, "\n") + "\n"),
			Stdout: out,
			Stderr: out,
		})
		var failed changeFailure
		if errors.As(err, &failed) {
			return &mergequeue.RefusedError{Reason: fmt.Sprintf("`%s` exited %d regenerating %s; the queue log's lines prefixed %q name what failed.",
				line, failed.code, strings.Join(paths, ", "), strings.TrimSpace(label))}
		}
		return err
	}
}

// affectedAnswer is what the affected hook prints. Any other keys are ignored, so a
// build tool's richer structured output can be the answer as it stands.
type affectedAnswer struct {
	Affected    []string `json:"affected"`
	UnboundedBy string   `json:"unbounded_by"`
}

// Affected is a [mergequeue.AffectedFunc] running line in dir with the change's paths
// on stdin, one per line. It must print one JSON object with "affected" (a list of
// units) and optionally "unbounded_by" (why the list is not a proof). A missing
// "affected" is unbounded; a failing command is an error, since the hook reads only the
// base, never the change's code.
func Affected(line, dir string, log *Log) mergequeue.AffectedFunc {
	return func(ctx context.Context, c mergequeue.Change, paths []string) ([]string, string, error) {
		if len(paths) == 0 {
			return []string{}, "", nil
		}
		stderr := log.Prefixed("[affected #" + c.ID + "] ")
		defer stderr.Close()
		var stdout bytes.Buffer
		err := run(ctx, Command{
			Line:   line,
			Dir:    dir,
			Env:    []string{EnvChange + "=" + c.ID, EnvHead + "=" + c.Head},
			Stdin:  strings.NewReader(strings.Join(paths, "\n") + "\n"),
			Stdout: &stdout,
			Stderr: stderr,
		})
		if err != nil {
			return nil, "", fmt.Errorf("affected hook: %w", err)
		}
		var ans affectedAnswer
		if err := json.Unmarshal(stdout.Bytes(), &ans); err != nil {
			return nil, "", fmt.Errorf("affected hook printed no JSON object: %w", err)
		}
		if ans.Affected == nil && ans.UnboundedBy == "" {
			return nil, "the affected hook printed no affected set", nil
		}
		return ans.Affected, ans.UnboundedBy, nil
	}
}

// Log interleaves the output of concurrent hooks a whole line at a time, each line
// tagged with its hook's prefix. A nil *Log discards.
type Log struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLog writes to w.
func NewLog(w io.Writer) *Log { return &Log{w: w} }

// Prefixed returns a writer tagging each of its lines with prefix. Close writes a final
// line that ended without a newline.
func (l *Log) Prefixed(prefix string) io.WriteCloser {
	if l == nil {
		return nopCloser{io.Discard}
	}
	return &prefixed{log: l, prefix: prefix}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

type prefixed struct {
	log    *Log
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

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
