// Package run is the shared subprocess helper for magus spells.
package run

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/egladman/magus/internal/sandbox"
)

// ErrAborted is returned by Run when the step gate returns StepActionAbort.
var ErrAborted = errors.New("run: aborted by user")

type (
	contextKey  struct{}
	stepGateKey struct{}
	stepReplKey struct{}
)

type writers struct {
	stdout io.Writer
	stderr io.Writer
}

// StepAction is returned by a step gate to control how Run proceeds.
type StepAction int

const (
	StepActionStep     StepAction = iota // execute this command then pause again
	StepActionContinue                   // execute this command and stop pausing
	StepActionSkip                       // skip this command (return nil without executing)
	StepActionAbort                      // abort with an error
)

// StepGate is called by Run before each subprocess; StepActionContinue removes the gate.
type StepGate func(ctx context.Context, name string, args []string, dir string) StepAction

// WithOutputWriters returns a context carrying stdout/stderr writers for Run.
func WithOutputWriters(ctx context.Context, stdout, stderr io.Writer) context.Context {
	return context.WithValue(ctx, contextKey{}, writers{stdout: stdout, stderr: stderr})
}

// OutputWriters returns the writers from WithOutputWriters, or os.Stdout/os.Stderr.
func OutputWriters(ctx context.Context) (stdout, stderr io.Writer) {
	if w, ok := ctx.Value(contextKey{}).(writers); ok {
		return w.stdout, w.stderr
	}
	return os.Stdout, os.Stderr
}

// WithStepGate attaches a gate to ctx; Run invokes it before each subprocess.
func WithStepGate(ctx context.Context, gate StepGate) context.Context {
	return context.WithValue(ctx, stepGateKey{}, gate)
}

// StepReplFn opens an interactive REPL at a step boundary with subprocess context as globals.
type StepReplFn func(ctx context.Context, name string, args []string, dir string) error

// WithStepRepl attaches fn to ctx for the step gate to open a REPL on 'r'.
func WithStepRepl(ctx context.Context, fn StepReplFn) context.Context {
	return context.WithValue(ctx, stepReplKey{}, fn)
}

// StepReplFrom retrieves the StepReplFn stored by WithStepRepl, or nil.
func StepReplFrom(ctx context.Context) StepReplFn {
	fn, _ := ctx.Value(stepReplKey{}).(StepReplFn)
	return fn
}

// Run executes name with args in dir, using writers from ctx (fallback: os.Stdout/Stderr).
// Cancellation sends a graceful signal with a 5s WaitDelay before force-kill.
func Run(ctx context.Context, dir, name string, args ...string) error {
	if gate, ok := ctx.Value(stepGateKey{}).(StepGate); ok && gate != nil {
		switch gate(ctx, name, args, dir) {
		case StepActionSkip:
			return nil
		case StepActionAbort:
			return ErrAborted
		case StepActionContinue:
			ctx = context.WithValue(ctx, stepGateKey{}, StepGate(nil))
		case StepActionStep:
		}
	}
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	setCancel(c) // platform-specific graceful cancel; see run_unix.go / run_windows.go
	c.WaitDelay = 5 * time.Second
	if w, ok := ctx.Value(contextKey{}).(writers); ok {
		c.Stdout = w.stdout
		c.Stderr = w.stderr
	} else {
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	}
	if p := sandbox.FromContext(ctx); p != nil {
		c.Env = p.BaseEnv
	}
	err := c.Run()
	if ctx.Err() != nil {
		killGroup(c) // reap grandchildren that ignored the graceful signal
	}
	// Surface ctx.Err() whenever cancelled — even if err is nil because the
	// process won the race and exited 0 — so callers can distinguish cancel from a
	// clean finish. errors.Join drops a nil err.
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	return err
}
