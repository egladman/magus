package main

import (
	"context"
	"fmt"
	"github.com/egladman/magus/internal/interactive/tty"
	"os"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/proc/run"
)

// withStepGate installs a TTY step gate on ctx.
func withStepGate(ctx context.Context) context.Context {
	return run.WithStepGate(ctx, newStepGate())
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

			restoreTTY, err := tty.MakeRaw(os.Stderr.Fd())
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
