package main

import (
	"context"
	"time"

	"github.com/egladman/magus/broker"
	internalci "github.com/egladman/magus/internal/ci"
)

// gatePoolProbeTimeout bounds the admission-server probe. The server is an
// accelerant, never a capability gate: a probe that cannot answer in time
// reads as idle and the gate runs.
var gatePoolProbeTimeout = 2 * time.Second

// gatePoolSaturated asks the broker whether a new run would be refused, and always says
// what it saw, so the finding can print the capacity behind either answer. Every
// failure (no socket, no answer) reads as idle: the broker is an arbiter, never a
// capability gate for this advice.
func gatePoolSaturated(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, gatePoolProbeTimeout)
	defer cancel()
	st, err := broker.QueryStatus(ctx, broker.DefaultAddr())
	if err != nil {
		return false, "no broker answered; treated as idle"
	}
	return internalci.DescribePool(&st.Capacity)
}
