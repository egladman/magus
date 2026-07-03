package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/egladman/magus/internal/run"
	"github.com/egladman/magus/types"
)

// Default readiness polling bounds, used when a service declares a readiness probe.
const (
	defaultReadyTimeout  = 30 * time.Second
	defaultReadyInterval = 200 * time.Millisecond
	defaultStopGrace     = 5 * time.Second
)

// ExecRunner is the production [Runner]: it forks the service process in its own
// process group, waits for an optional readiness probe to pass, and stops it via
// its graceful Stop command or a signal, escalating to a group kill. It supervises
// the process in the background (the Registry, not this Runner, decides when to stop
// it), which is why a service must run in the foreground and not detach - the
// MGS5002 ward enforces that.
//
// Process control (group setup, graceful terminate, hard group-kill) is delegated to
// internal/run's platform primitives so grandchildren of a wrapper like `docker run`
// are reaped, not orphaned, on every OS - the same handling magus uses for ordinary
// forked commands.
//
// The zero value is ready to use with sane defaults; the timing fields are for
// tuning (and for keeping tests fast). Any field left 0 uses its default.
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
	stop  types.Command
	grace time.Duration
	done  chan struct{} // closed once the process has been reaped
}

// Start forks the service and returns once its readiness probe passes (or
// immediately if it declares none). A readiness failure stops the just-started
// process and returns an error, so a failed Start leaves nothing running.
func (r ExecRunner) Start(ctx context.Context, s types.Service) (Handle, error) {
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
			stopProc(h)
			return nil, fmt.Errorf("service: %q not ready: %w", s.Command.Bin, err)
		}
	}
	return h, nil
}

// Stop stops a running service.
func (ExecRunner) Stop(h Handle) {
	eh, ok := h.(*execHandle)
	if !ok || eh == nil {
		return
	}
	stopProc(eh)
}

// stopProc shuts a service down: prefer its graceful Stop command, else SIGTERM the
// process group; either way escalate to a hard group kill if it does not exit within
// the grace window, and wait for it to be reaped. Signalling and killing target the
// whole group (via internal/run) so a wrapper's grandchildren are not orphaned.
func stopProc(h *execHandle) {
	select {
	case <-h.done:
		return // already exited
	default:
	}

	if h.stop.Bin != "" {
		runStopCommand(h.stop, h.grace)
	} else {
		_ = run.TerminateGroup(h.cmd)
	}

	select {
	case <-h.done:
	case <-time.After(h.grace):
		run.KillGroup(h.cmd)
		<-h.done
	}
}

// runStopCommand runs a service's graceful stop command, bounded by grace so a hung
// stop binary cannot block teardown indefinitely (the caller still escalates to a
// group kill afterward).
func runStopCommand(stop types.Command, grace time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	_ = exec.CommandContext(ctx, stop.Bin, stop.Args...).Run()
}

// waitReady polls the readiness probe until it exits 0 or the timeout elapses. The
// probe is a command whose exit code is the signal (the Kubernetes exec-probe
// model), run repeatedly at a fixed interval.
func (r ExecRunner) waitReady(ctx context.Context, probe types.Command) error {
	deadline := time.Now().Add(r.readyTimeout())
	for {
		c := exec.Command(probe.Bin, probe.Args...)
		if c.Run() == nil {
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
