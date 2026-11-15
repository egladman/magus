package main

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/types"
)

const streamAllSentinel = watch.AllSentinel

// streamAffected reads file-path batches from r and runs target on the affected
// projects, each batch through m.Run with runOpts, so the guard judges it as it judges
// any run. watch.RunBatches schedules the batches. streamAllSentinel triggers a
// full-workspace build. null reads NUL-separated paths with double-NUL batch
// boundaries. Per-batch errors go to errFn.
func streamAffected(ctx context.Context, m *magus.Magus, r io.Reader, target string, null bool, errFn func(error), runOpts ...magus.RunOption) {
	watch.RunBatches(ctx, watch.ReadBatches(ctx, r, null), func(paths []string) error {
		targets, err := streamTargets(ctx, m, paths, target)
		if err != nil || len(targets) == 0 {
			return err
		}
		if err := m.Run(ctx, targets, runOpts...); err != nil {
			return fmt.Errorf("stream: %s: %w", target, err)
		}
		return nil
	}, errFn)
}

// streamTargets is target on every project the batch affects, or on the whole
// workspace when the batch carries streamAllSentinel.
func streamTargets(ctx context.Context, m *magus.Magus, paths []string, target string) ([]types.Target, error) {
	if slices.Contains(paths, streamAllSentinel) {
		ts, err := m.ExpandPath(types.Target{Name: target})
		if err != nil {
			return nil, fmt.Errorf("stream: expand all: %w", err)
		}
		return ts, nil
	}
	res, err := m.AffectedFromPaths(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("stream: compute affected: %w", err)
	}
	targets := make([]types.Target, len(res.Affected))
	for i, p := range res.Affected {
		targets[i] = types.Target{Path: p, Name: target}
	}
	return targets, nil
}
