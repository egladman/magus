package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// lifecycle is how a long-lived magus process, the broker or a foreground server, answers
// the signals it owns. Neither exits on SIGHUP: both run with no controlling terminal, so
// a hangup is only ever sent on purpose. The server reloads its configuration and the
// broker reopens its log.
//
// A first SIGTERM (and a first SIGINT, unless interruptIsNow) starts a graceful stop; a
// second stopping signal, or a SIGINT when interruptIsNow, asks for one now. Past that
// the process has stopped listening, and a third signal meets the default disposition,
// so a teardown that wedges can still be killed without reaching for SIGKILL.
type lifecycle struct {
	hangup         func()
	stop           func()
	stopNow        func()
	interruptIsNow bool
}

// ownsItsSignals reports whether this invocation is a process that answers signals
// through a lifecycle rather than through watchInterrupts: `magus broker` serving, or a
// server running in this process.
func ownsItsSignals(args []string) bool {
	sub, subArgs := peekSub(args)
	switch sub {
	case "broker":
		return brokerServes(subArgs)
	case "server":
		return isServerRun(subArgs) && wantsForeground(subArgs)
	}
	return false
}

// watch answers SIGHUP, SIGINT and SIGTERM for l until ctx ends or stopNow ran, and
// returns the function that stops answering. Windows delivers no SIGHUP, and its console
// close and shutdown events arrive as SIGTERM.
func (l lifecycle) watch(ctx context.Context) (release func()) {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
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

// handle is the policy watch applies, apart from signal registration so a test can drive
// it with a plain channel.
func (l lifecycle) handle(ctx context.Context, sigs <-chan os.Signal) {
	stopping := false
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigs:
			switch {
			case sig == syscall.SIGHUP:
				if l.hangup != nil {
					l.hangup()
				}
			case stopping || (sig == os.Interrupt && l.interruptIsNow):
				l.stopNow()
				return
			default:
				stopping = true
				l.stop()
			}
		}
	}
}
