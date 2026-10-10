package knowledge

import (
	"context"
	"log/slog"
	"runtime"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/readlog"
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
	// from it unless StampsFunc can compute them.
	Stamps Stamps
	// StampsFunc computes Stamps on demand, for a caller whose stamps cost something it
	// would rather not pay on every read: the CLI's fold in the evaluated workspace model,
	// which means parsing every magusfile. Ensure calls it at most once, and only when a
	// wanted class is not settled by FastStamps. nil means Stamps is all there is.
	StampsFunc func(context.Context) (Stamps, error)
	// FastStamps are the cheap stamps (see manifest.FastStamps): what Stamps fold minus the
	// evaluated workspace model, so they cost a tree walk and a few stats. A class whose
	// fast stamp matches the one recorded at its last sync is answered from the store
	// without Stamps being computed at all, which is the steady state of a read. A class
	// with no fast stamp is settled by Stamps as before; nil settles nothing.
	FastStamps Stamps
	// FastStampsFunc computes the fast stamps over what the evaluation they stand for
	// read beyond the tree (see manifest.Reads): the recorded reads at the start of a
	// read, and the reads an evaluation just made when its shards are synced or its fast
	// stamps recorded, so the stamp on the manifest is the one the next read computes.
	// known is false for a manifest that never recorded its reads, which the function
	// folds so that such a stamp matches nothing recorded with them. nil leaves
	// FastStamps as given.
	FastStampsFunc func(ctx context.Context, reads readlog.Reads, known bool) Stamps
	// IndexesFunc resolves the symbol indexes the workspace declares (see Inputs.Indexes),
	// for a manifest that has none recorded. Ensure calls it only when StampsFunc has
	// already paid for the evaluated workspace, so a read the fast stamps settle never
	// evaluates one to record them; a rebuild records what gather returns instead.
	IndexesFunc func(context.Context) ([]SymbolIndexDeclaration, error)
	// ReadsFunc returns what the evaluated workspace read beyond the tree (see
	// Inputs.Reads), under the same rule as IndexesFunc: only once the evaluation is paid
	// for, and only for a manifest that has none recorded.
	ReadsFunc func(context.Context) (readlog.Reads, error)
	// Root is the workspace root stamped onto the returned graph (see Graph.SetRoot).
	Root string
}

// Build is the cache-first entry point: it assembles every shard from the
// gathered inputs, fingerprints each by content, reconciles them against the
// persisted store, and returns the merged in-memory graph. First run pays a full
// build; steady state writes only the shards whose content changed.
func Build(ctx context.Context, cacheDir string, opts BuildOptions, in Inputs, log *slog.Logger) (*Graph, error) {
	opts.Stamps, opts.StampsFunc = nil, nil
	opts.FastStamps, opts.FastStampsFunc, opts.IndexesFunc, opts.ReadsFunc = nil, nil, nil, nil
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
	e := &ensureRun{
		store: NewStore(cacheDir, opts.Immutable, opts.MaxBytes, opts.Remote, log),
		opts:  opts,
	}
	e.man = e.store.readManifestOrNil()
	if opts.FastStampsFunc != nil {
		// Over the reads the last evaluation recorded: the environment and files its top
		// levels consulted are inputs of the declarations the shards hold, so a stamp
		// that stands for "that evaluation still holds" folds their current values.
		e.opts.FastStamps = opts.FastStampsFunc(ctx, e.man.reads(), e.man.readsKnown())
	}

	// The fast stamps settle what they can first, so a read whose tree, binary,
	// configuration and recorded reads are as they were at the last sync never pays for
	// the full stamps. Whatever they leave unsettled is judged by the full stamps,
	// computed on demand.
	//   measured 2026-10-02 on this repository (10k nodes): the full stamps cost a warm
	//   `magus query` 425ms of its 590ms, every bit of it parsing magusfiles the read
	//   never used. The fast stamps cost the tree walk the full ones already paid.
	unsettled := fastStaleClasses(e.man, e.opts.FastStamps, want, opts.Refresh)
	var stale []ShardClass
	if len(unsettled) > 0 {
		if err := e.resolveStamps(ctx); err != nil {
			return nil, err
		}
		stale = staleClasses(e.man, e.opts.Stamps, unsettled, opts.Refresh)
	}

	var fresh []ShardClass
	for _, c := range DefaultClasses {
		if slices.Contains(want, c) && !slices.Contains(stale, c) {
			fresh = append(fresh, c)
		}
	}
	stored, bad, err := e.store.readClassShards(ctx, e.man, fresh)
	if err != nil {
		return nil, err
	}
	stale = append(stale, bad...)

	var built []Shard
	if len(stale) > 0 {
		// A class rebuilt here is stamped with both stamps, so the full ones are needed
		// even when only a shard file went bad under fast-fresh stamps.
		if err := e.resolveStamps(ctx); err != nil {
			return nil, err
		}
		e.store.log.DebugContext(ctx, "reassembling shard classes", slog.Any("classes", stale))
		in, err := gather(stale)
		if err != nil {
			return nil, err
		}
		if slices.Contains(stale, ClassSession) {
			// The overlay resolves against every class; the ones not being rebuilt are
			// read from the store.
			if in.storedPathIDs, err = e.store.storedPathIDs(ctx, e.man, stale); err != nil {
				return nil, err
			}
		}
		built, err = e.assembleAndSync(ctx, in, stale)
		if err != nil {
			return nil, err
		}
	}
	g := mergeShards(slices.Concat(built, stored))
	g.SetRoot(opts.Root)
	if len(built) == 0 {
		// Every stored shard was checked against man, so g is the graph man describes.
		g.base = shardsIdentity(stored, e.man)
		if err := e.recordFastStamps(ctx, fresh); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// ensureRun is one Ensure call: the store it reads, the manifest it read, and the options
// whose stamps resolve as the call goes. The helpers below are its steps; holding the
// three here keeps each step's signature to what the step itself decides.
type ensureRun struct {
	store *Store
	man   *manifest
	opts  BuildOptions
	// stampsResolved records that StampsFunc ran, which is also the fact that the
	// evaluated workspace is already paid for.
	stampsResolved bool
}

// resolveStamps fills opts.Stamps from StampsFunc when the caller deferred them; a second
// call is a no-op, so Ensure can ask wherever it first needs them.
func (e *ensureRun) resolveStamps(ctx context.Context) error {
	if e.opts.Stamps != nil || e.opts.StampsFunc == nil {
		return nil
	}
	stamps, err := e.opts.StampsFunc(ctx)
	if err != nil {
		return err
	}
	if stamps == nil {
		stamps = Stamps{}
	}
	e.opts.Stamps, e.stampsResolved = stamps, true
	return nil
}

// recordFastStamps records the fast stamps of the fresh classes the full stamps had to settle (a
// store written before fast stamps existed), so the next read settles them the cheap way
// instead of paying the full stamps forever. The declared indexes ride along when the
// evaluated workspace was paid for anyway, and only then.
func (e *ensureRun) recordFastStamps(ctx context.Context, fresh []ShardClass) error {
	rec := fastStampRecord{stamps: e.opts.FastStamps, classes: fresh}
	if e.opts.FastStampsFunc != nil && !e.man.readsKnown() {
		// The stamps computed at the start of this read folded "reads unknown", which
		// nothing may be recorded over (see assembleAndSync). Unless the reads resolve
		// below, this read records no stamp and the next one pays the full stamps again.
		rec.stamps = nil
		if e.stampsResolved && e.opts.ReadsFunc != nil {
			reads, err := e.opts.ReadsFunc(ctx)
			if err != nil {
				return err
			}
			rec.reads = &reads
			rec.stamps = e.opts.FastStampsFunc(ctx, reads, true)
		}
	}
	if e.stampsResolved && e.opts.IndexesFunc != nil && !e.man.indexesKnown() {
		indexes, err := e.opts.IndexesFunc(ctx)
		if err != nil {
			return err
		}
		if indexes == nil {
			indexes = []SymbolIndexDeclaration{}
		}
		rec.indexes = indexes
	}
	return e.store.recordFastStamps(ctx, e.man, rec)
}

// fastStaleClasses returns the members of want the fast stamps do not settle: a class with
// no fast stamp, none recorded, or a recorded one that differs. refresh, or a store with
// no readable manifest, leaves every one unsettled. What it returns is judged by the full
// stamps, never reassembled on this verdict alone.
func fastStaleClasses(man *manifest, fast Stamps, want []ShardClass, refresh bool) []ShardClass {
	var out []ShardClass
	for _, c := range want {
		cur := fast[c]
		if refresh || man == nil || cur == "" || man.FastStamps[c] != cur {
			out = append(out, c)
		}
	}
	return out
}

// assembleAndSync assembles the classes in stale, fingerprints every shard, and persists
// them with their stamps.
func (e *ensureRun) assembleAndSync(ctx context.Context, in Inputs, stale []ShardClass) ([]Shard, error) {
	store, opts := e.store, e.opts
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

	fast := opts.FastStamps
	if opts.FastStampsFunc != nil {
		switch {
		case in.Reads != nil:
			// Recorded over the reads THIS evaluation made, which is what the next read
			// computes its stamp over; the stamps computed at the start of this read
			// folded the previous evaluation's reads and would be one evaluation behind.
			fast = opts.FastStampsFunc(ctx, *in.Reads, true)
		case !e.man.readsKnown():
			// Computed over unknown reads, which no stamp may be recorded over: a read
			// that matched it would trust an evaluation whose inputs nobody wrote down.
			fast = nil
		}
	}
	plan := syncPlan{classes: stale, stamps: opts.Stamps, fastStamps: fast, indexes: in.Indexes, reads: in.Reads, extra: in.Extra, refresh: opts.Refresh}
	if err := store.syncClasses(ctx, shards, fps, plan); err != nil {
		return nil, err
	}
	return shards, nil
}
