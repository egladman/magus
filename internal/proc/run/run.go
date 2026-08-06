// Package run is the shared subprocess helper for magus spells.
package run

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

// ErrAborted is returned by Exec when the step gate returns StepActionAbort.
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

// WithStepGate attaches a gate to ctx; Exec invokes it before each subprocess.
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

// commandLine renders name + args as a single display string for an exec event, quoting
// any argument that contains whitespace so the command reads back unambiguously. It is for
// display in the log viewer, not for re-execution (no shell-escaping guarantees).
func commandLine(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, a := range args {
		if strings.ContainsAny(a, " \t\n") {
			parts = append(parts, `"`+a+`"`)
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}
