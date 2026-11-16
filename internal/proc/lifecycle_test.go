//go:build !windows

package proc

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lifecycleBound is how long a test waits for a signal to be answered. It bounds a
// failure; nothing waits it out on success.
const lifecycleBound = 20 * time.Second

// runLifecycle feeds sigs to lifecycle.handle and returns what it did, in order, once
// it has done want things. It also reports whether handle returned on its own, which it
// does only after a stop-now.
func runLifecycle(interruptIsNow bool, want int, sigs ...os.Signal) (got []string, returned bool) {
	ch := make(chan os.Signal, len(sigs))
	for _, s := range sigs {
		ch <- s
	}
	events := make(chan string, len(sigs))
	l := Lifecycle{
		Hangup:         func() { events <- "hangup" },
		Stop:           func() { events <- "stop" },
		StopNow:        func() { events <- "now" },
		InterruptIsNow: interruptIsNow,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.handle(ctx, ch)
	}()
	for range want {
		got = append(got, <-events)
	}
	if got[len(got)-1] == "now" {
		<-done
		returned = true
	}
	cancel()
	<-done
	return got, returned
}

func TestLifecycleSignalPolicy(t *testing.T) {
	for _, tc := range []struct {
		name           string
		interruptIsNow bool
		sigs           []os.Signal
		want           []string
	}{
		{"a hangup never stops", false, []os.Signal{syscall.SIGHUP, syscall.SIGHUP}, []string{"hangup", "hangup"}},
		{"a first TERM stops gracefully", false, []os.Signal{syscall.SIGTERM}, []string{"stop"}},
		{"a second TERM stops now", false, []os.Signal{syscall.SIGTERM, syscall.SIGTERM}, []string{"stop", "now"}},
		{"a hangup mid-stop is still a hangup", false, []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGTERM}, []string{"stop", "hangup", "now"}},
		{"the server takes INT like TERM", false, []os.Signal{os.Interrupt, os.Interrupt}, []string{"stop", "now"}},
		{"the broker takes INT as now", true, []os.Signal{os.Interrupt, syscall.SIGHUP}, []string{"now"}},
		{"INT after a broker's TERM is now", true, []os.Signal{syscall.SIGTERM, os.Interrupt}, []string{"stop", "now"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, returned := runLifecycle(tc.interruptIsNow, len(tc.want), tc.sigs...)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, got[len(got)-1] == "now", returned,
				"handle stops listening after a stop-now, and only then")
		})
	}
}

// A SIGHUP that lands after startup bound the socket and before the lifecycle watches is
// queued and answered once the watch starts, rather than meeting the default disposition
// and ending the process.
func TestASignalBeforeTheLifecycleWatchesIsAnsweredLater(t *testing.T) {
	CaptureSignals()
	t.Cleanup(func() {
		if early != nil {
			signal.Stop(early)
			early = nil
		}
	})
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))

	hangups := make(chan struct{}, 1)
	release := Lifecycle{Hangup: func() { hangups <- struct{}{} }, Stop: func() {}, StopNow: func() {}}.Watch(t.Context())
	defer release()
	select {
	case <-hangups:
	case <-time.After(lifecycleBound):
		t.Fatal("the queued SIGHUP was never answered")
	}
}
