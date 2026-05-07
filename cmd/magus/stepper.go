package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/egladman/magus/internal/run"
)

// withStepGate installs a TTY step gate on ctx.
func withStepGate(ctx context.Context) context.Context {
	return run.WithStepGate(ctx, newStepGate())
}

// newStepGate returns a run.StepGate that prompts before each subprocess (s/c/k/r/a).
func newStepGate() run.StepGate {
	var mu sync.Mutex // serialize prompts: overlapping raw-mode TTY prompts corrupt the terminal
	return func(ctx context.Context, name string, args []string, dir string) run.StepAction {
		mu.Lock()
		defer mu.Unlock()
		for {
			argv := append([]string{name}, args...)
			fmt.Fprintf(os.Stderr, "\n→ %s  (cwd: %s)\n", strings.Join(argv, " "), dir)
			fmt.Fprintf(os.Stderr, "  [s]tep  [c]ontinue  s[k]ip  [r]epl  [a]bort: ")

			fd := int(os.Stderr.Fd())
			oldState, err := term.MakeRaw(fd)
			if err != nil {
				// Can't go raw — fall back to step-always so the user still sees commands.
				fmt.Fprintln(os.Stderr, "(terminal unavailable, stepping)")
				return run.StepActionStep
			}

			restore := func() {
				if err := term.Restore(fd, oldState); err != nil {
					fmt.Fprintf(os.Stderr, "magus: terminal restore failed: %v\n", err)
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
