package knowledge

import (
	"context"
	"log/slog"
	"runtime"
	"slices"

	"golang.org/x/sync/errgroup"
)

// BuildOptions carries the build toggles so callers pass named fields rather than
// a row of transposable booleans.
type BuildOptions struct {
	Immutable bool         // set when cache.write.enabled is false: load-only, never write
	Refresh   bool         // force a full rebuild regardless of fingerprints and stamps
	MaxBytes  int64        // soft cap on the shards dir; 0 = unlimited
	Remote    RemoteShards // optional remote shard backing; nil = local-only
	// Stamps are the current input stamps (see Stamps). A class whose stamp matches the
	// one its shards were stored under is answered from the store; nil answers nothing
	// from it.
	Stamps Stamps
	// Root is the workspace root stamped onto the returned graph (see Graph.SetRoot).
	Root string
}

// Build is the cache-first entry point: it assembles every shard from the
// gathered inputs, fingerprints each by content, reconciles them against the
// persisted store, and returns the merged in-memory graph. First run pays a full
// build; steady state writes only the shards whose content changed.
func Build(ctx context.Context, cacheDir string, opts BuildOptions, in Inputs, log *slog.Logger) (*Graph, error) {
	opts.Stamps = nil
	if opts.Root == "" {
		opts.Root = in.Root
	}
	return Ensure(ctx, cacheDir, opts, AllClasses, func([]ShardClass) (Inputs, error) { return in, nil }, log)
}

// Ensure brings every class in want up to date and returns the default graph: the
// non-lazy shards merged in shard-name order. A class whose stored stamp matches
// opts.Stamps is answered from the store without assembly; the rest are reassembled from
// the Inputs gather returns. gather is called at most once, with the stale classes, so a
// caller gathers only what those classes read; it is not called at all when every class
// is current, which is the steady state of a read.
//
// The graph is the same whichever path produced each class: stored shards are checked
// against the manifest's fingerprints, and assembled ones are merged in the order Load
// reads the store in.
func Ensure(ctx context.Context, cacheDir string, opts BuildOptions, want []ShardClass, gather func(stale []ShardClass) (Inputs, error), log *slog.Logger) (*Graph, error) {
	store := NewStore(cacheDir, opts.Immutable, opts.MaxBytes, opts.Remote, log)
	man := store.readManifestOrNil()
	stale := staleClasses(man, opts.Stamps, want, opts.Refresh)

	var fresh []ShardClass
	for _, c := range DefaultClasses {
		if slices.Contains(want, c) && !slices.Contains(stale, c) {
			fresh = append(fresh, c)
		}
	}
	stored, bad, err := store.readClassShards(ctx, man, fresh)
	if err != nil {
		return nil, err
	}
	stale = append(stale, bad...)

	var built []Shard
	if len(stale) > 0 {
		in, err := gather(stale)
		if err != nil {
			return nil, err
		}
		if slices.Contains(stale, ClassSession) {
			// The overlay resolves against every class; the ones not being rebuilt are
			// read from the store.
			if in.storedPathIDs, err = store.storedPathIDs(ctx, man, stale); err != nil {
				return nil, err
			}
		}
		built, err = assembleAndSync(ctx, store, in, stale, opts)
		if err != nil {
			return nil, err
		}
	}
	g := mergeShards(append(built, stored...), false)
	g.SetRoot(opts.Root)
	return g, nil
}

// assembleAndSync assembles the classes in stale, fingerprints every shard, and persists
// them with their stamps.
func assembleAndSync(ctx context.Context, store *Store, in Inputs, stale []ShardClass, opts BuildOptions) ([]Shard, error) {
	if slices.Contains(stale, ClassDomain) {
		byProject := map[string]map[string]string{}
		for _, p := range in.Graph.Projects {
			if len(p.Layers) > 0 {
				byProject[p.Path] = p.Layers
			}
		}
		layers, err := unionLayers(byProject)
		if err != nil {
			return nil, err
		}
		in.Layers = layers
	}
	shards := AssembleClasses(in, stale)
	for _, sh := range shards {
		if sh.Err != nil {
			return nil, sh.Err
		}
	}

	// optimization: fingerprint shards in parallel. Each fingerprint builds a
	// temp graph, sorts, marshals, and hashes: independent CPU work done for
	// every shard on every build, so it scales with cores. fingerprintShardContent
	// shares no state, so this is race-free.
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
			fpByIndex[i] = fingerprintShardContent(shards[i])
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

	plan := syncPlan{classes: stale, stamps: opts.Stamps, extra: in.Extra, refresh: opts.Refresh}
	if err := store.syncClasses(ctx, shards, fps, plan); err != nil {
		return nil, err
	}
	return shards, nil
}
