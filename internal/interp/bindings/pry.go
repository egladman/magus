package bindings

import (
	"context"
	"fmt"
	"os"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	buzzengine "github.com/egladman/magus/internal/interp/engine/buzz"
)

// buildBuzzPry returns the magus.pry() direct callable for a Buzz session. It suspends
// execution at the call site and opens the shared Pry REPL on the running
// session, with stack introspection and (via the VM step hook) .step/.next/
// .finish. In parse mode (target enumeration) it is a no-op so loading a
// magusfile.buzz never blocks on a breakpoint.
func buildBuzzPry(sess *buzz.Session, parseMode bool) vm.Callable {
	if parseMode {
		return func(_ context.Context, _ []vm.Value) (vm.Value, error) {
			return vm.Null, nil
		}
	}
	return func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		esess := buzzengine.Wrap(sess)
		opts := interp.ReplOptions{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}

		resume, err := interp.Pry(ctx, esess, buzzPryContext(esess), opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magus.pry: %v\n", err)
			return vm.Null, nil
		}
		if resume == interp.ResumeContinue {
			return vm.Null, nil
		}
		buzzInstallStepHook(ctx, esess, resume, opts)
		return vm.Null, nil
	}
}

// buzzPryContext builds the REPL's PryContext from the session's current call
// stack (innermost frame first).
func buzzPryContext(esess engine.Session) interp.PryContext {
	pctx := interp.PryContext{}
	dbg, ok := esess.(engine.DebugReader)
	if !ok {
		return pctx
	}
	frames := dbg.Frames()
	pctx.Frames = frames
	if len(frames) > 0 {
		pctx.File = frames[0].Source
		pctx.Line = frames[0].CurrentLine
		pctx.Func = frames[0].Name
	}
	return pctx
}

// buzzInstallStepHook arms a one-shot line hook that re-enters the Pry REPL when
// execution reaches the next line selected by mode (step into / over / finish),
// then resumes. The hook keys purely off
// line events and call depth: step stops at any depth, next at the start depth
// or shallower, finish strictly shallower (the current frame has returned).
func buzzInstallStepHook(ctx context.Context, esess engine.Session, resume interp.PryResume, opts interp.ReplOptions) {
	stepper, ok := esess.(engine.Stepper)
	if !ok {
		fmt.Fprintln(os.Stdout, "(stepping not supported on this engine — resuming)")
		return
	}
	dbg, _ := esess.(engine.DebugReader)
	depthNow := func() int {
		if dbg != nil {
			return dbg.CallDepth()
		}
		return 0
	}

	startDepth := depthNow()
	mode := resume
	var hook func(engine.StepEvent, engine.Frame)
	hook = func(ev engine.StepEvent, _ engine.Frame) {
		if ev != engine.StepLine {
			return
		}
		cur := depthNow()
		switch mode {
		case interp.ResumeStep: // step into: stop at the next line, any depth
		case interp.ResumeNext: // step over: skip lines in deeper (called) frames
			if cur > startDepth {
				return
			}
		case interp.ResumeFinish: // run until the current frame returns
			if cur >= startDepth {
				return
			}
		}
		// Suspend stepping while the REPL is open so nested evals don't re-fire.
		stepper.ClearStepHook()
		next, err := interp.Pry(ctx, esess, buzzPryContext(esess), opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "magus.pry: %v\n", err)
			return
		}
		if next == interp.ResumeContinue {
			return // leave hook cleared: run to completion
		}
		mode = next
		startDepth = depthNow()
		stepper.SetStepHook(engine.MaskLine, hook)
	}
	stepper.SetStepHook(engine.MaskLine, hook)
}
