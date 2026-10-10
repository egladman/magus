package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/tty"
)

// confirmWindow is how long a first Ctrl+C stays armed. Long enough to
// press again deliberately, short enough that a stray interrupt earlier
// in a long run cannot make a later single press fatal -- which would
// reintroduce exactly the accident the confirmation exists to prevent.
const confirmWindow = 3 * time.Second

// watchInterrupts returns a context cancelled on shutdown signals, a stop
// function the caller must invoke to release the signal handler, and a query
// reporting which signal stopped the run. The third return is what
// [withInterrupt] turns into an exit code; cancellation alone is invisible to
// the caller.
//
// A first SIGINT at an interactive terminal, while this process has targets
// executing, only warns: it names what a stop would discard, the run keeps
// going, and a second SIGINT within [confirmWindow] stops it. Builds are long
// and Ctrl+C is next to Ctrl+V, so a single fingertip should not discard
// minutes of work. This is safe precisely because magus puts every child in
// its own process group (see internal/proc/run): a terminal Ctrl+C reaches
// magus alone, so ignoring one does not leave the run half-killed behind its
// back.
//
// The first signal stops the command, with no confirmation, when:
//
//   - nothing is executing, so a stop discards nothing: a read, a follow
//     stream (events -f, status -W, watch) or a run between targets. A pause
//     there is a toll with nothing behind it;
//   - the signal is SIGTERM, which comes from a supervisor rather than a
//     fingertip and must be honored at once; or
//   - stderr is not a terminal, so nobody is there to read the prompt and
//     a CI system sending SIGINT would otherwise have to send two.
func watchInterrupts(parent context.Context) (context.Context, func(), func() (syscall.Signal, bool)) {
	ctx, cancel := context.WithCancel(parent)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Signal number, 0 when none stopped the run. Atomic because the handler
	// goroutine writes it while the main goroutine reads it on the way out.
	var stopped atomic.Int32

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The warning answers a keypress, so it is written to the terminal itself: a
		// quiet display would drop it and the run would look hung.
		confirmInterrupts(ctx, sigs, cancel, os.Stderr,
			tty.IsTerminalWriter(os.Stderr, tty.SystemProbe), confirmWindow, cache.InFlight,
			func(sig syscall.Signal) { stopped.Store(int32(sig)) },
			func() { signal.Stop(sigs) })
	}()

	interrupted := func() (syscall.Signal, bool) {
		if n := stopped.Load(); n != 0 {
			return syscall.Signal(n), true
		}
		return 0, false
	}

	return ctx, func() {
		signal.Stop(sigs)
		cancel()
		<-done
	}, interrupted
}

// confirmInterrupts implements the policy described on watchInterrupts.
// It is separated from signal registration so a test can drive it with a
// plain channel, a buffer and a fixed running set instead of real signals,
// a real terminal and real targets. A nil inFlight reports nothing running.
//
// It returns when ctx is done, which the caller's stop function
// guarantees, so the goroutine cannot outlive the command.
func confirmInterrupts(
	ctx context.Context,
	sigs <-chan os.Signal,
	cancel context.CancelFunc,
	out io.Writer,
	interactive bool,
	window time.Duration,
	inFlight func() []cache.RunningTarget,
	record func(syscall.Signal),
	release func(),
) {
	if release == nil {
		release = func() {}
	}
	var timer *time.Timer
	var armed <-chan time.Time
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer, armed = nil, nil
		}
	}
	defer stopTimer()

	for {
		select {
		case <-ctx.Done():
			return
		case <-armed:
			// The window closed with no second press. Forget the first one,
			// so the next Ctrl+C warns rather than stopping the run.
			timer, armed = nil, nil
		case sig := <-sigs:
			var running []cache.RunningTarget
			if sig != syscall.SIGTERM && interactive && armed == nil && inFlight != nil {
				running = inFlight()
			}
			if len(running) == 0 {
				stopTimer()
				// Record before cancelling: recording after races dispatch
				// returning, which brings the exit-0 bug back intermittently.
				if s, ok := sig.(syscall.Signal); ok && record != nil {
					record(s)
				}
				cancel()
				// Hand the NEXT signal back to the runtime's default
				// disposition before returning. Nothing drains sigs once this
				// goroutine is gone, so every later SIGINT/SIGTERM was
				// swallowed: a supervisor's second SIGTERM did nothing, and a
				// user hammering Ctrl+C through a slow teardown (or through
				// the end-of-run failure prompt, which blocks on a read that
				// cannot be interrupted) had no escape at all.
				release()
				return
			}
			_, _ = fmt.Fprintf(out, "\n%s\n", discardWarning(running, time.Now()))
			timer = time.NewTimer(window)
			armed = timer.C
		}
	}
}

// discardWarning is what an armed first Ctrl+C prints: the targets a second
// press would stop and how long the oldest has run, so the choice is made
// against what it costs. It names the key rather than the signal because that
// is what the user pressed.
func discardWarning(running []cache.RunningTarget, now time.Time) string {
	const show = 3
	names := make([]string, 0, min(len(running), show))
	oldest := now
	for i, t := range running {
		if i < show {
			names = append(names, t.Project+" "+t.Target)
		}
		if t.Started.Before(oldest) {
			oldest = t.Started
		}
	}
	list := strings.Join(names, ", ")
	if extra := len(running) - show; extra > 0 {
		list += fmt.Sprintf(" and %d more", extra)
	}
	noun, them := "targets", "them"
	if len(running) == 1 {
		noun, them = "target", "it"
	}
	return fmt.Sprintf("interrupt: %d %s running (%s), %s in; Ctrl+C again stops %s",
		len(running), noun, list, now.Sub(oldest).Round(time.Second), them)
}
