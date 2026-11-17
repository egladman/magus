package main

import (
	"github.com/egladman/magus/internal/server"
)

// ownsItsSignals reports whether this process answers signals through a
// proc.Lifecycle rather than cache.WatchInterrupts. A serving broker does, and so
// does a server run here.
func ownsItsSignals(args []string) bool {
	sub, subArgs := peekSub(args)
	if sub != "server" {
		return false
	}
	if len(subArgs) > 0 && subArgs[0] == "broker" {
		return server.BrokerServes(subArgs[1:])
	}
	return server.IsServerRun(subArgs) && server.WantsForeground(subArgs)
}
