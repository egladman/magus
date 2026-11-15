package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/types"
)

const streamAllSentinel = watch.AllSentinel

// streamAffected reads file-path batches from r and runs target on the affected
// projects, each batch through m.Run with runOpts, so the guard judges it as it judges
// any run. Builds run synchronously; batches arriving during a build are merged and run
// after. streamAllSentinel triggers a full-workspace build. null reads NUL-separated
// paths with double-NUL batch boundaries. Per-batch errors go to errFn.
func streamAffected(ctx context.Context, m *magus.Magus, r io.Reader, target string, null bool, errFn func(error), runOpts ...magus.RunOption) {
	if errFn == nil {
		errFn = func(error) {}
	}
	batches := watch.ReadBatches(ctx, r, null)

	var (
		mu      sync.Mutex
		pending []string
		running bool
		wg      sync.WaitGroup
	)

	runBatch := func(paths []string) {
		var batchTargets []types.Target
		if slices.Contains(paths, streamAllSentinel) {
			ts, err := m.ExpandPath(types.Target{Name: target})
			if err != nil {
				errFn(fmt.Errorf("stream: expand all: %w", err))
				return
			}
			batchTargets = ts
		} else {
			res, err := m.AffectedFromPaths(ctx, paths)
			if err != nil {
				errFn(fmt.Errorf("stream: compute affected: %w", err))
				return
			}
			if len(res.Affected) == 0 {
				return
			}
			batchTargets = make([]types.Target, len(res.Affected))
			for i, p := range res.Affected {
				batchTargets[i] = types.Target{Path: p, Name: target}
			}
		}
		if err := m.Run(ctx, batchTargets, runOpts...); err != nil {
			errFn(fmt.Errorf("stream: %s: %w", target, err))
		}
	}

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case paths, ok := <-batches:
			if !ok {
				wg.Wait()
				return
			}
			if len(paths) == 0 {
				continue
			}
			mu.Lock()
			if running {
				pending = watch.MergePaths(pending, paths)
				mu.Unlock()
				continue
			}
			running = true
			wg.Add(1)
			mu.Unlock()
			go func(p []string) {
				defer wg.Done()
				// A panic in one batch is reported but must not kill the worker:
				// doing so would orphan any batches already merged into pending. The
				// drain loop below is the single owner of running=false, so recovering
				// per-batch (not at the goroutine level) keeps the "the active worker
				// always drains pending" invariant intact across a panicking build.
				safeRun := func(p []string) {
					defer func() {
						if r := recover(); r != nil {
							errFn(fmt.Errorf("stream: panic: %v", r))
						}
					}()
					runBatch(p)
				}
				for {
					safeRun(p)
					mu.Lock()
					next := pending
					pending = nil
					if len(next) == 0 {
						running = false
						mu.Unlock()
						return
					}
					mu.Unlock()
					select {
					case <-ctx.Done():
						mu.Lock()
						running = false
						mu.Unlock()
						return
					default:
					}
					p = next
				}
			}(paths)
		}
	}
}
