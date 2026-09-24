package main

import (
	"context"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// runLifecycle feeds sigs to lifecycle.handle and returns what it did, in order, once
// it has done want things. It also reports whether handle returned on its own, which it
// does only after a stop-now.
func runLifecycle(interruptIsNow bool, want int, sigs ...os.Signal) (got []string, returned bool) {
	ch := make(chan os.Signal, len(sigs))
	for _, s := range sigs {
		ch <- s
	}
	events := make(chan string, len(sigs))
	l := lifecycle{
		hangup:         func() { events <- "hangup" },
		stop:           func() { events <- "stop" },
		stopNow:        func() { events <- "now" },
		interruptIsNow: interruptIsNow,
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

func TestOwnsItsSignals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"broker"}, true},
		{[]string{"broker", "--log", "/x"}, true},
		{[]string{"-v", "broker"}, true},
		{[]string{"broker", "status"}, false},
		{[]string{"broker", "stop"}, false},
		{[]string{"broker", "-h"}, false},
		{[]string{"server", "--foreground"}, true},
		{[]string{"server", "start", "--foreground"}, true},
		// The launcher returns once its child is up; the child is the server.
		{[]string{"server", "start"}, false},
		{[]string{"server", "reload"}, false},
		{[]string{"run", "test", "."}, false},
	} {
		assert.Equal(t, tc.want, ownsItsSignals(tc.args), "%v", tc.args)
	}
}
