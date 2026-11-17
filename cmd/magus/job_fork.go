package main

import (
	"context"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// forkHeldWorkspace is the workspace jobWorkspace loaded for ws, nil when it did not.
func forkHeldWorkspace(ctx context.Context, root string, ws job.Workspace) types.WorkspaceRepository { //nolint:ireturn // nil is the answer for no workspace
	if ws.Unavailable != "" {
		return nil
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return nil
	}
	return m
}

// admitForkValidation claims the machine budget for validating a fork's row, under the
// workspace's broker policy, as admitGraphBuild does for a graph build: a machine short of
// memory refuses it with MGS3053 and a full budget past budget_wait with MGS3009, both exit
// 75. Broker off claims nothing and reads no memory.
func admitForkValidation(ctx context.Context, admit cache.MachineAdmitter, root string) (func(), error) {
	return admitUnderBrokerPolicy(ctx, admit, root, "job-fork", guard.ForkValidationMemoryMB)
}
