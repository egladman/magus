package main

import (
	"context"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/types"
)

// gateMergeScanLimit bounds the history walk job_graph shares with the gate.
const gateMergeScanLimit = internalci.GateMergeScanLimit

// gatePoolProbeTimeout bounds the admission-server probe. The server is an
// accelerant, never a capability gate: a probe that cannot answer in time
// reads as idle and the gate runs.
var gatePoolProbeTimeout = 2 * time.Second

// gateRedundancy is internalci.GateRedundancy under the lowercase method names the
// affected path still calls.
type gateRedundancy internalci.GateRedundancy

func (g *gateRedundancy) noteUndeclaredSeeds(projects []string) {
	(*internalci.GateRedundancy)(g).NoteUndeclaredSeeds(projects)
}

func (g *gateRedundancy) evaluate(ctx context.Context, disabled bool) error {
	return (*internalci.GateRedundancy)(g).Evaluate(ctx, disabled)
}

func (g *gateRedundancy) size(ctx context.Context, base string, forced bool) *types.TierReport {
	return (*internalci.GateRedundancy)(g).Size(ctx, base, forced)
}

func (g *gateRedundancy) record(ctx context.Context, runErr error, cutShort bool, failed []journal.Event) {
	(*internalci.GateRedundancy)(g).Record(ctx, runErr, cutShort, failed)
}

func gateEnv() internalci.GateEnv {
	return internalci.GateEnv{DryRun: globalCfg.DryRun, Nested: spawn.CurrentLevel() > 0, Version: version, PoolProbe: gatePoolSaturated, Repairs: magus.UncommittedRepairs}
}

func isGateInvocation(target string, partial bool) bool {
	return internalci.IsGateInvocation(target, partial, globalCfg.DryRun)
}

func prepareGateRedundancy(ctx context.Context, m *magus.Magus, target string, targets []types.Target, charms []string, partial bool) *gateRedundancy {
	return (*gateRedundancy)(internalci.PrepareGateRedundancy(ctx, m, gateEnv(), target, targets, charms, partial))
}


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
