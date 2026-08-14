package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	"github.com/egladman/magus/internal/proc/run"
)

// withStepGate installs the TTY step gate on ctx, together with the REPL it
// offers.
//
// BOTH, always, and that is the point of one function rather than two calls.
// The gate advertises [r]epl in its prompt, and the REPL is attached
// separately - so for as long as this repo has had stepping, every install site
// wired the gate, none wired the REPL, and pressing r printed "(no REPL
// available outside a magusfile run)" every time. Installing them together is
// what makes the advertised key impossible to leave dead.
func withStepGate(ctx context.Context) context.Context {
	ctx = run.WithStepRepl(ctx, stepRepl)
	return run.WithStepGate(ctx, newStepGate())
}

// stepRepl opens a Buzz REPL at a step boundary, seeded with the command that is
// about to run.
//
// A fresh session rather than the magusfile's own: the point of stopping here is
// to look at the WORKSPACE - read the file the command is about to consume,
// check what a tool reports, try the command's own arguments - and the full
// host surface (fs, os, vcs, http) is what answers that. The magusfile's locals
// are a different question, and `magus buzz` already answers it.
//
// Closing the REPL resumes the run, so this is a pause rather than an exit.
func stepRepl(ctx context.Context, name string, args []string, dir string) error {
	sess, err := interp.NewBuzzReplSession(ctx, dir)
	if err != nil {
		return fmt.Errorf("step repl: %w", err)
	}
	defer func() { _ = sess.Close() }()

	argv := strings.Join(append([]string{name}, args...), " ")
	return interp.Repl(ctx, sess, interp.ReplOptions{
		WorkDir: dir,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Banner: fmt.Sprintf("magus step - paused before `%s`\n  cmd, args and dir are bound; .help for commands, ctrl-d resumes the run",
			argv),
		Locals: map[string]engine.Value{
			"cmd":  engine.StringValue(name),
			"args": engine.StringValue(strings.Join(args, " ")),
			"dir":  engine.StringValue(dir),
		},
		Candidates: workspaceReplCandidates(ctx, dir),
	})
}

// newStepGate returns a run.StepGate that prompts before each subprocess (s/c/k/r/a).
//
// "continue" latches HERE rather than by clearing the gate off a context: the gate is
// installed once on the run's root ctx, and a caller that cleared it would only be
// clearing its own copy, so the next command re-prompted and [c] never meant continue.
func newStepGate() run.StepGate {
	var mu sync.Mutex // serialize prompts: overlapping raw-mode TTY prompts corrupt the terminal
	var resumed bool  // set by [c]; guarded by mu
	return func(ctx context.Context, name string, args []string, dir string) run.StepAction {
		mu.Lock()
		defer mu.Unlock()
		if resumed {
			return run.StepActionContinue
		}
		for {
			argv := append([]string{name}, args...)
			fmt.Fprintf(os.Stderr, "\n-> %s  (cwd: %s)\n", strings.Join(argv, " "), dir)
			fmt.Fprintf(os.Stderr, "  [s]tep  [c]ontinue  s[k]ip  [r]epl  [a]bort: ")

			// Raw mode goes on the descriptor this READS, which is stdin. It
			// used to go on stderr, and that only ever worked because the two
			// normally point at the same device: with stdin redirected the
			// terminal went raw, the first read hit EOF, and the run aborted
			// with no explanation of why.
			restoreTTY, err := tty.MakeRaw(os.Stdin.Fd())
			if err != nil {
				// Can't go raw: fall back to step-always so the user still sees commands.
				fmt.Fprintln(os.Stderr, "(terminal unavailable, stepping)")
				return run.StepActionStep
			}

			restore := func() {
				if err := restoreTTY(); err != nil {
					fmt.Fprintf(os.Stderr, "magus: %v\n", err)
				}
			}

			buf := make([]byte, 1)
			var wantRepl bool
			for {
				if _, err := os.Stdin.Read(buf); err != nil {
					restore()
					return run.StepActionAbort
				}
				switch buf[0] {
				case 's', '\r', '\n':
					fmt.Fprintln(os.Stderr, "step")
					restore()
					return run.StepActionStep
				case 'c':
					fmt.Fprintln(os.Stderr, "continue")
					restore()
					resumed = true
					return run.StepActionContinue
				case 'k':
					fmt.Fprintln(os.Stderr, "skip")
					restore()
					return run.StepActionSkip
				case 'r':
					fmt.Fprintln(os.Stderr, "repl")
					restore()
					wantRepl = true
				case 'a', 'q', 3: // 3 = Ctrl-C
					fmt.Fprintln(os.Stderr, "abort")
					restore()
					return run.StepActionAbort
				default:
					continue
				}
				break
			}

			if wantRepl {
				if replFn := run.StepReplFrom(ctx); replFn != nil {
					if err := replFn(ctx, name, args, dir); err != nil {
						fmt.Fprintf(os.Stderr, "repl: %v\n", err)
					}
				} else {
					fmt.Fprintln(os.Stderr, "(no REPL available outside a magusfile run)")
				}
				continue
			}
		}
	}
}
