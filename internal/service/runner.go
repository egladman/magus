package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/spells"
)

// Default readiness polling bounds, used when a service declares a readiness probe.
const (
	defaultReadyTimeout  = 30 * time.Second
	defaultReadyInterval = 200 * time.Millisecond
	defaultStopGrace     = 5 * time.Second
)

// DefaultShutdownTimeout bounds a [Registry.Shutdown] call for a caller whose own ctx
// may already be cancelled (e.g. Ctrl-C) by the time it tears down services - such a
// caller must derive a fresh ctx (context.WithoutCancel plus this timeout) rather than
// pass the cancelled one through, or Shutdown would return immediately without
// stopping anything. It covers one victim's worst case (defaultReadyTimeout waiting
// out an in-flight Start, plus defaultStopGrace) with headroom; Shutdown tears down
// victims concurrently, so this bounds the whole call, not each service.
const DefaultShutdownTimeout = 40 * time.Second

// ExecRunner is the production [Runner]: it forks the service process in its own
// process group, waits for an optional readiness probe to pass, and stops it via
// its graceful Stop command or a signal, escalating to a group kill. It supervises
// the process in the background (the Registry, not this Runner, decides when to stop
// it), which is why a service must run in the foreground and not detach - the
// MGS5002 ward enforces that.
//
// Process control (group setup, graceful terminate, hard group-kill) is delegated to
// internal/proc/run's platform primitives so grandchildren of a wrapper like `docker run`
// are reaped, not orphaned, on every OS - the same handling magus uses for ordinary
// forked commands.
//
// The zero value is ready to use; the timing fields are for tuning (and for keeping
// tests fast). Any field left 0 uses its default.
type ExecRunner struct {
	StopGrace     time.Duration // wait for a graceful stop before a hard kill
	ReadyTimeout  time.Duration // total time a readiness probe may take to pass
	ReadyInterval time.Duration // delay between readiness probe attempts
}

func (r ExecRunner) stopGrace() time.Duration {
	if r.StopGrace > 0 {
		return r.StopGrace
	}
	return defaultStopGrace
}

func (r ExecRunner) readyTimeout() time.Duration {
	if r.ReadyTimeout > 0 {
		return r.ReadyTimeout
	}
	return defaultReadyTimeout
}

func (r ExecRunner) readyInterval() time.Duration {
	if r.ReadyInterval > 0 {
		return r.ReadyInterval
	}
	return defaultReadyInterval
}

type execHandle struct {
	cmd   *exec.Cmd
	stop  spells.Command
	grace time.Duration
	done  chan struct{} // closed once the process has been reaped
}

// Start forks the service and returns once its readiness probe passes (or
// immediately if it declares none). A readiness failure stops the just-started
// process and returns an error, so a failed Start leaves nothing running.
func (r ExecRunner) Start(ctx context.Context, s spells.Service) (Handle, error) {
	if s.Command.Bin == "" {
		return nil, fmt.Errorf("service: no command to run")
	}
	c := exec.Command(s.Command.Bin, s.Command.Args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	run.SetupProcessGroup(c) // own process group so Stop can reap the whole subtree
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("service: start %q: %w", s.Command.Bin, err)
	}
	h := &execHandle{cmd: c, stop: s.Stop, grace: r.stopGrace(), done: make(chan struct{})}
	go func() {
		_ = c.Wait() // reap so the process never lingers as a zombie
		close(h.done)
	}()

	if s.Readiness.Bin != "" {
		if err := r.waitReady(ctx, s.Readiness); err != nil {
			// Start's own ctx, not context.Background(): if ctx is already why waitReady
			// gave up (deadline/cancel), stopProc's grace wait should not outlive it
			// either - a caller that cancelled Start is not going to wait around for a
			// graceful cleanup grace period.
			stopProc(ctx, h)
			return nil, fmt.Errorf("service: %q not ready: %w", s.Command.Bin, err)
		}
	}
	return h, nil
}

// Stop stops a running service. ctx bounds the wait beyond the usual stop
// grace: if ctx is done first, Stop escalates to a hard kill immediately instead of
// waiting out the rest of the grace window, and does not block confirming the
// process was reaped (the Start goroutine still reaps it in the background, so
// nothing is left a zombie - Stop just stops waiting to hear about it).
func (ExecRunner) Stop(ctx context.Context, h Handle) {
	eh, ok := h.(*execHandle)
	if !ok || eh == nil {
		return
	}
	stopProc(ctx, eh)
}

// stopProc shuts a service down: prefer its graceful Stop command, else SIGTERM the
// process group; either way escalate to a hard group kill if it does not exit within
// the grace window (or ctx ends first), and wait for it to be reaped unless ctx ends
// before that too. Signaling and killing target the whole group (via
// internal/proc/run) so a wrapper's grandchildren are not orphaned.
func stopProc(ctx context.Context, h *execHandle) {
	select {
	case <-h.done:
		return // already exited
	default:
	}

	if h.stop.Bin != "" {
		runStopCommand(ctx, h.stop, h.grace)
	} else {
		_ = run.TerminateGroup(h.cmd)
	}

	select {
	case <-h.done:
		return
	case <-time.After(h.grace):
	case <-ctx.Done():
		// Caller's deadline expired before the grace window did; escalate now rather
		// than waiting out the rest of grace, so a bounded Shutdown cannot be stalled
		// past its own deadline by one slow-to-exit process.
	}
	run.KillGroup(h.cmd)
	select {
	case <-h.done:
	case <-ctx.Done():
	}
}

// runStopCommand runs a service's graceful stop command, bounded by the shorter of
// grace and ctx's remaining time, so a hung stop binary cannot block teardown
// indefinitely (the caller still escalates to a group kill afterward) and cannot
// outlive a caller deadline either.
func runStopCommand(ctx context.Context, stop spells.Command, grace time.Duration) {
	cctx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	_ = exec.CommandContext(cctx, stop.Bin, stop.Args...).Run()
}

// waitReady polls the readiness probe until it exits 0 or the timeout elapses. The
// probe is a command whose exit code is the signal (the Kubernetes exec-probe
// model), run repeatedly at a fixed interval. Each attempt is bounded by the
// remaining time to deadline (mirroring runStopCommand's use of CommandContext) so a
// probe binary that never exits cannot block past ReadyTimeout or an outer ctx
// cancellation - a plain exec.Command here would run inside c.Run() uninterruptibly.
func (r ExecRunner) waitReady(ctx context.Context, probe spells.Command) error {
	deadline := time.Now().Add(r.readyTimeout())
	for {
		attemptCtx, cancel := context.WithDeadline(ctx, deadline)
		err := exec.CommandContext(attemptCtx, probe.Bin, probe.Args...).Run()
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("readiness probe %q did not pass within %s", probe.Bin, r.readyTimeout())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.readyInterval()):
		}
	}
}
