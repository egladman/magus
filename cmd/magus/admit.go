package main

import (
	"context"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// admitUnderBrokerPolicy claims memoryMB of the machine budget for target,
// under the workspace's broker policy. Broker off claims nothing.
func admitUnderBrokerPolicy(ctx context.Context, admit cache.MachineAdmitter, root, target string, memoryMB int) (func(), error) {
	policy := globalCfg.Broker.Resolved()
	if policy == types.BrokerOff {
		return func() {}, nil
	}
	claim := types.MachineClaim{Project: ".", Target: target, MemoryMB: memoryMB, Dir: resolveRootOrEmpty(root)}
	return cache.AdmitMachine(ctx, admit, claim, policy == types.BrokerRequired, globalCfg.BudgetWait)
}
