package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/interactive/tty"
)

// confirmWindow is how long a first Ctrl+C stays armed. Long enough to
// press again deliberately, short enough that a stray interrupt earlier
// in a long run cannot make a later single press fatal -- which would
// reintroduce exactly the accident the confirmation exists to prevent.
const confirmWindow = 3 * time.Second

// interruptMessage is what the first Ctrl+C prints. It names the key
// rather than the signal because that is what the user pressed.
const interruptMessage = "interrupt: press Ctrl+C again to stop the run"

// watchInterrupts returns a context cancelled on shutdown signals, and a
// stop function the caller must invoke to release the signal handler.
//
// A first SIGINT at an interactive terminal only warns; the run keeps
// going and a second SIGINT within [confirmWindow] stops it. Builds are
// long and Ctrl+C is next to Ctrl+V, so a single fingertip should not
// discard minutes of work. This is safe precisely because magus puts every
// child in its own process group (see internal/proc/run): a terminal
// Ctrl+C reaches magus alone, so ignoring one does not leave the run
// half-killed behind its back.
//
// The confirmation is skipped, and the first signal stops the run, when:
//
//   - the signal is SIGTERM, which comes from a supervisor rather than a
//     fingertip and must be honoured at once; or
//   - stderr is not a terminal, so nobody is there to read the prompt and
//     a CI system sending SIGINT would otherwise have to send two.
func watchInterrupts(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		defer close(done)
		confirmInterrupts(ctx, sigs, cancel, os.Stderr,
			tty.IsTerminalWriter(os.Stderr, tty.SystemProbe), confirmWindow)
	}()

	return ctx, func() {
		signal.Stop(sigs)
		cancel()
		<-done
	}
}

// confirmInterrupts implements the policy described on watchInterrupts.
// It is separated from signal registration so a test can drive it with a
// plain channel and a buffer instead of real signals and a real terminal.
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
) {
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
			if sig == syscall.SIGTERM || !interactive || armed != nil {
				stopTimer()
				cancel()
				return
			}
			_, _ = fmt.Fprintf(out, "\n%s\n", interruptMessage)
			timer = time.NewTimer(window)
			armed = timer.C
		}
	}
}
