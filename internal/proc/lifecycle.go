package proc

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Lifecycle is how a long-lived magus process, the broker or a foreground server, answers
// the signals it owns. Neither exits on SIGHUP: both run with no controlling terminal, so
// a hangup is only ever sent on purpose. The server reloads its configuration and the
// broker reopens its log.
//
// A first SIGTERM (and a first SIGINT, unless InterruptIsNow) starts a graceful stop; a
// second stopping signal, or a SIGINT when InterruptIsNow, asks for one now. Past that
// the process has stopped listening, and a third signal meets the default disposition,
// so a teardown that wedges can still be killed without reaching for SIGKILL.
type Lifecycle struct {
	Hangup         func()
	Stop           func()
	StopNow        func()
	InterruptIsNow bool
}

// early queues the signals a process that answers its own signals receives before its
// Lifecycle watches. The broker's and the server's sockets bind during startup, which is what a
// supervisor reads as ready, and the lifecycle is installed only after; a SIGHUP in that
// window met the default disposition and ended the process.
var early chan os.Signal

// CaptureSignals starts queueing SIGHUP, SIGINT and SIGTERM for the next lifecycle to
// watch. Call it before anything binds a socket.
func CaptureSignals() {
	early = make(chan os.Signal, 4)
	signal.Notify(early, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
}

// Watch answers SIGHUP, SIGINT and SIGTERM for l until ctx ends or stopNow ran, and
// returns the function that stops answering. It first answers what CaptureSignals
// queued. Windows delivers no SIGHUP, and its console close and shutdown events arrive
// as SIGTERM.
func (l Lifecycle) Watch(ctx context.Context) (release func()) {
	sigs := early
	early = nil
	if sigs == nil {
		sigs = make(chan os.Signal, 4)
		signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.handle(ctx, sigs)
		signal.Stop(sigs)
	}()
	return func() {
		cancel()
		<-done
	}
}

// handle is the policy Watch applies, apart from signal registration so a test can drive
// it with a plain channel.
func (l Lifecycle) handle(ctx context.Context, sigs <-chan os.Signal) {
	stopping := false
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigs:
			switch {
			case sig == syscall.SIGHUP:
				if l.Hangup != nil {
					l.Hangup()
				}
			case stopping || (sig == os.Interrupt && l.InterruptIsNow):
				l.StopNow()
				return
			default:
				stopping = true
				l.Stop()
			}
		}
	}
}
