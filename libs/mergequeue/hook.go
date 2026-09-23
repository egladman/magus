package mergequeue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Environment the queue sets for its command hooks.
const (
	EnvChange  = "MERGEQUEUE_CHANGE"   // the change's id
	EnvHead    = "MERGEQUEUE_HEAD"     // the change's head commit
	EnvBase    = "MERGEQUEUE_BASE"     // the branch the queue merges into
	EnvBaseSHA = "MERGEQUEUE_BASE_SHA" // the commit every stage is built on
	EnvBelow   = "MERGEQUEUE_BELOW"    // the commit the stage was built on
	EnvStage   = "MERGEQUEUE_STAGE"    // the staging commit being gated
)

// Command is one command hook: a shell command line run with `sh -c`.
type Command struct {
	Line   string
	Dir    string
	Env    []string // added to the queue's own environment
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run runs c. A cancelled context interrupts the command rather than killing it, so a
// build tool it started can stop cleanly; it is killed 30 seconds later.
func (c Command) Run(ctx context.Context) error {
	if strings.TrimSpace(c.Line) == "" {
		return errors.New("mergequeue: empty command hook")
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Line)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 30 * time.Second
	return cmd.Run()
}

// ExitTempFail is EX_TEMPFAIL from sysexits.h: the gate could not run right now (a
// build tool's lock was held, say), which says nothing about the change.
const ExitTempFail = 75

// gateAttempts bounds how often a gate exiting ExitTempFail is run again.
const gateAttempts = 3

// CommandGate is a [Gate] running a command in each staging commit's checkout. Exit
// status 0 is green. [ExitTempFail] is retried, and is an error once attempts run out,
// so no author is kicked back for the machine being busy; any other exit is red.
type CommandGate struct {
	Line    string
	Base    string // branch, for the hook's environment
	BaseSHA string
	// Log receives the command's output, each line prefixed with its stage, so several
	// concurrent gates stay readable. Nil discards it.
	Log *PrefixWriter
	// RetryDelay is the wait before running a gate again after ExitTempFail; zero means
	// five seconds.
	RetryDelay time.Duration
}

func (g *CommandGate) retryDelay() time.Duration {
	if g.RetryDelay > 0 {
		return g.RetryDelay
	}
	return 5 * time.Second
}

func (g *CommandGate) Validate(ctx context.Context, s Stage, below string, c Change) (GateResult, error) {
	label := "[" + short(s.Commit) + " #" + c.ID + "] "
	var out io.Writer = io.Discard
	if g.Log != nil {
		out = g.Log.With(label)
	}
	var err error
	var exit *exec.ExitError
	for attempt := 1; ; attempt++ {
		err = Command{
			Line: g.Line,
			Dir:  s.Dir,
			Env: []string{EnvChange + "=" + c.ID, EnvHead + "=" + c.Head, EnvBase + "=" + g.Base,
				EnvBaseSHA + "=" + g.BaseSHA, EnvBelow + "=" + below, EnvStage + "=" + s.Commit},
			Stdout: out,
			Stderr: out,
		}.Run(ctx)
		if ctx.Err() != nil {
			return GateResult{}, ctx.Err()
		}
		if !errors.As(err, &exit) || exit.ExitCode() != ExitTempFail {
			break
		}
		if attempt == gateAttempts {
			return GateResult{}, fmt.Errorf("`%s` exited %d (temporary failure) %d times on the staging commit `%s`",
				g.Line, ExitTempFail, gateAttempts, short(s.Commit))
		}
		select {
		case <-ctx.Done():
			return GateResult{}, ctx.Err()
		case <-time.After(g.retryDelay()):
		}
	}
	if errors.As(err, &exit) {
		return GateResult{Summary: fmt.Sprintf("`%s` exited %d on the staging commit `%s`; the queue log's lines prefixed %q name what failed.",
			g.Line, exit.ExitCode(), short(s.Commit), strings.TrimSpace(label))}, nil
	}
	if err != nil {
		return GateResult{}, err
	}
	return GateResult{Green: true}, nil
}

// affectedAnswer is what the affected hook prints. Any other keys are ignored, so a
// build tool's richer structured output can be the answer as it stands.
type affectedAnswer struct {
	Affected  []string `json:"affected"`
	Unbounded string   `json:"unbounded"`
}

// AffectedCommand is an [AffectedFunc] running line in dir with the change's paths on
// stdin, one per line. It must print one JSON object with "affected" (a list of units)
// and optionally "unbounded" (why the list is not a proof). A missing "affected" is
// unbounded; a failing command is an error.
func AffectedCommand(line, dir string, stderr io.Writer) AffectedFunc {
	return func(ctx context.Context, c Change, paths []string) ([]string, string, error) {
		if len(paths) == 0 {
			return []string{}, "", nil
		}
		var stdout bytes.Buffer
		err := Command{
			Line:   line,
			Dir:    dir,
			Env:    []string{EnvChange + "=" + c.ID, EnvHead + "=" + c.Head},
			Stdin:  strings.NewReader(strings.Join(paths, "\n") + "\n"),
			Stdout: &stdout,
			Stderr: stderr,
		}.Run(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("affected hook: %w", err)
		}
		var ans affectedAnswer
		if err := json.Unmarshal(stdout.Bytes(), &ans); err != nil {
			return nil, "", fmt.Errorf("affected hook printed no JSON object: %w", err)
		}
		if ans.Affected == nil && ans.Unbounded == "" {
			return nil, "the affected hook printed no affected set", nil
		}
		return ans.Affected, ans.Unbounded, nil
	}
}

// PrefixWriter interleaves concurrent writers a whole line at a time, each line tagged
// with its writer's prefix.
type PrefixWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewPrefixWriter writes to w.
func NewPrefixWriter(w io.Writer) *PrefixWriter { return &PrefixWriter{w: w} }

// With returns a writer tagging each of its lines with prefix.
func (p *PrefixWriter) With(prefix string) io.Writer { return &linePrefixer{p: p, prefix: prefix} }

type linePrefixer struct {
	p      *PrefixWriter
	prefix string
	buf    []byte
}

func (l *linePrefixer) Write(b []byte) (int, error) {
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		l.p.mu.Lock()
		_, err := fmt.Fprintf(l.p.w, "%s%s\n", l.prefix, l.buf[:i])
		l.p.mu.Unlock()
		l.buf = l.buf[i+1:]
		if err != nil {
			return len(b), err
		}
	}
}
