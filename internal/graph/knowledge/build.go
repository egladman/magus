package knowledge

import (
	"context"
	"log/slog"
	"runtime"

	"golang.org/x/sync/errgroup"
)

// BuildOptions carries the build toggles so callers pass named fields rather than
// a row of transposable booleans.
type BuildOptions struct {
	Immutable bool         // mirror MAGUS_CACHE_IMMUTABLE: load-only, never write
	Refresh   bool         // force a full rebuild regardless of fingerprints
	MaxBytes  int64        // soft cap on the shards dir; 0 = unlimited
	Remote    RemoteShards // optional remote shard backing; nil = local-only
	// InputFingerprints maps a shard name to the fingerprint of its EXPENSIVE input (a git
	// scan), for shards whose producer the caller may skip when the input is unchanged.
	// Sync records it and reuses a skipped-but-fresh shard from disk. Empty for the common
	// case (all shards content-fingerprinted and rebuilt each time).
	InputFingerprints map[string]string
}

// Build is the cache-first entry point: it assembles every shard from the
// gathered inputs, fingerprints each by content, reconciles them against the
// persisted store, and returns the merged in-memory graph. First run pays a full
// build; steady state writes only the shards whose content changed.
func Build(ctx context.Context, cacheDir string, opts BuildOptions, in Inputs, log *slog.Logger) (*Graph, error) {
	shards := AssembleShards(in)

	// optimization: fingerprint shards in parallel. Each fingerprint builds a
	// temp graph, sorts, marshals, and hashes - independent CPU work done for
	// every shard on every build (the steady-state query cost), so it scales with
	// cores. fingerprintShardContent shares no state, so this is race-free.
	//   measured: BenchmarkBuildNoop -44.3% sec/op (benchstat, n=8+6, 2000-project
	//             fixture, 10-core: ~56.7ms -> ~31.6ms; includes the no-op manifest
	//             skip in Sync). BuildCold also benefits.
	//   trade-off: results collected into a per-index slice, then into the map, to
	//             avoid a shared-map write; negligible extra allocation.
	fpByIndex := make([]string, len(shards))
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(runtime.GOMAXPROCS(0))
	for i := range shards {
		eg.Go(func() error {
			if err := egctx.Err(); err != nil {
				return err
			}
			fp, err := fingerprintShardContent(shards[i])
			if err != nil {
				return err
			}
			fpByIndex[i] = fp
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	fps := make(map[string]string, len(shards))
	for i, sh := range shards {
		fps[sh.Name] = fpByIndex[i]
	}

	store := NewStore(cacheDir, opts.Immutable, opts.MaxBytes, opts.Remote, log)
	return store.Sync(ctx, shards, fps, opts.InputFingerprints, opts.Refresh)
}
