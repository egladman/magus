//go:build !windows

package proc

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// shutdownGrace is how long watchSignals waits after cancellation before SIGKILL.
const shutdownGrace = 30 * time.Second

// watchSignals installs a one-shot SIGINT/SIGTERM/SIGHUP handler: cancels in-flight
// handler contexts, closes the server, and re-raises the original signal.
// Sends SIGKILL after shutdownGrace if handlers do not exit.
func watchSignals(srv *Server, cancel context.CancelFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		var sig os.Signal
		select {
		case sig = <-ch:
		case <-srv.done:
			signal.Stop(ch)
			return
		}
		signal.Stop(ch)
		cancel()

		done := make(chan struct{})
		go func() {
			srv.Close()
			close(done)
		}()
		select {
		case <-done:
			p, _ := os.FindProcess(os.Getpid())
			if p != nil {
				_ = p.Signal(sig)
			}
		case <-time.After(shutdownGrace):
			p, _ := os.FindProcess(os.Getpid())
			if p != nil {
				_ = p.Signal(syscall.SIGKILL)
			}
		}
	}()
}
