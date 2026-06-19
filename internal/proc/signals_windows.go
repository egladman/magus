//go:build windows

package proc

import (
	"context"
	"os"
	"os/signal"
)

// watchSignals installs a one-shot signal handler for os.Interrupt
// (Ctrl+C). Windows does not support SIGTERM/SIGHUP via signal.Notify.
func watchSignals(srv *Server, cancel context.CancelFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		select {
		case <-ch:
			signal.Stop(ch)
			cancel()
			srv.Close()
			// Windows does not support re-raising signals the way Unix does;
			// Close() has already shut down the listener and drained connections,
			// so we can return and let the main goroutine observe the cancelled
			// context rather than killing the process from a library goroutine.
		case <-srv.done:
			signal.Stop(ch)
		}
	}()
}
