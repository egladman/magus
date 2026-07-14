// Package cache implements magus's content-addressed build cache.
// Layout: cas/ (blobs) + manifests/ + logs/. The store is local; an optional
// pluggable [RemoteBackend] (see remote.go) lets a miss restore from, and a build
// publish to, a shared store.
package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/journal"
	runPkg "github.com/egladman/magus/internal/proc/run"
)

// Cache is an on-disk content-addressed build cache handle.
type Cache struct {
	dir            string
	mutable        bool // true = read+write (default); false = read-only
	sizeMB         int
	maxImportBytes int64 // per-entry cap for Import; 0 uses defaultMaxImportBytes
	log            *slog.Logger
	logLevel       slog.Level // effective minimum level; used by captureRun
	silent         bool       // silent output mode: bounded failure dumps + bubbled important lines
	collapse       bool       // collapse-on-success: withhold live subprocess output, replay it only on failure
	hits           atomic.Int64
	misses         atomic.Int64
	errs           atomic.Int64
	diskMu         sync.Mutex    // guards the memoized on-disk size below
	diskBytes      int64         // last computed cache size in bytes
	diskAt         time.Time     // when diskBytes was computed (zero = never)
	mtimes         *mtimeStore   // mtime fast-path for source hashing
	outputs        *OutputStore  // per-execution captured-output store (target output refs)
	exportMu       sync.RWMutex  // guards Export/Import against concurrent Run writes
	evictMu        sync.Mutex    // serialises evictLRU so concurrent Runs don't over-evict each other's fresh manifests
	remote         RemoteBackend // optional remote backend; nil = local-only

	// Remote-artifact signing: signer signs on push, verifier authenticates on
	// import. signingSeed/trustedKeys hold the raw option inputs until Open builds
	// the keys, so option application stays error-free and keys validate once.
	signer         *signer
	verifier       *verifier
	signingSeed    []byte
	trustedKeys    [][]byte
	insecureRemote bool // explicit opt-out: allow a remote backend with no trust set
}

const defaultMaxImportBytes int64 = 10 << 30

// Stats holds per-Cache counters reported in the end-of-run summary.
type Stats struct {
	Hit   int
	Miss  int
	Error int
}

// Step is the hashable description of a cached build step.
type Step struct {
	ProjectPath     string   // repo-relative project directory
	Sources         []string // doublestar globs (relative to WorkspaceRoot) for the cache key
	EnvAllow        []string // env var names whose values contribute to the key
	Outputs         []string // globs snapshotted into cache and replayed on hit
	Deps            []string // upstream project hashes folded into the key
	DependsOn       []string // upstream project paths for scheduling (not hashed)
	After           []string // explicit ordering edges (not hashed); build with DepKey
	WorkspaceRoot   string
	Target          string   // mixed into key to distinguish targets on the same sources
	Charms          []string // active charm names (sorted), mixed into key so charm-variant runs differ
	SpellDefVersion string   // binary fingerprint; forces miss on magus upgrade
	ToolVersions    []string // "spell:version" strings; forces miss on toolchain upgrade
	NoCache         bool     // when true, always run fn; never replay or snapshot (long-running targets)
	Exclusive       bool     // RunAll only: when true, runs alone; no other batch step runs concurrently (ignored by Run, which has no batch)
	Slots           int      // RunAll only: concurrency slots held while running (0 or 1 = one slot); clamped to the limiter's capacity. Never hashed.
	Label           string   // display-only project name for logs (root reads as e.g. "magus", not "."); never hashed
}

// Result is the outcome of a Cache.Run call.
type Result struct {
	ProjectPath string
	Hash        string
	Hit         bool
	Duration    time.Duration
	Outputs     []string // absolute paths written or replayed
	Ref         string   // per-execution output reference id (see recordOutput); "" when the output store is absent or persistence failed
}

type runCtx struct {
	step        *Step
	concurrency int
	limiter     *Limiter
	onHit       func(*Result)
	onMiss      func(*Result)
	onError     func(error)
	onStep      func(*Step)
	// onResults all fire after each Run (in registration order); multiple
	// observers (report, telemetry, diagnostic capture) coexist without clobbering.
	onResults []func(*Step, *Result, error)
	// deferMtimeFlush suppresses the per-Run mtime flush so a RunAll batch can
	// flush once after all steps complete instead of once per step.
	deferMtimeFlush bool
}

// fireResults notifies every registered result observer, in registration order.
func (rc *runCtx) fireResults(step *Step, result *Result, err error) {
	for _, fn := range rc.onResults {
		fn(step, result, err)
	}
}

// deferMtimeFlush is an internal RunOption set by RunAll so member Run calls skip
// the per-call mtime flush; RunAll flushes the shared store once after the batch.
func deferMtimeFlush() RunOption {
	return func(rc *runCtx) { rc.deferMtimeFlush = true }
}

// Open returns a Cache rooted at dir (created on demand). MAGUS_CACHE_IMMUTABLE=true
// opens read-only (replays hits, never writes). Logger respects MAGUS_LOG_FORMAT/LEVEL.
func Open(dir string, opts ...Option) (*Cache, error) {
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("magus/cache: mkdir %q: %w", dir, err)
	}
	mutable := true
	if v := strings.ToLower(os.Getenv("MAGUS_CACHE_IMMUTABLE")); v == "true" || v == "1" {
		mutable = false
	}
	defaultLevel := slog.LevelInfo
	if v := os.Getenv("MAGUS_LOG_LEVEL"); v != "" {
		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(v)); err == nil {
			defaultLevel = lvl
		}
	}
	log := newLogger(os.Getenv("MAGUS_LOG_FORMAT"), defaultLevel)
	c := &Cache{
		dir:      dir,
		mutable:  mutable,
		log:      log,
		logLevel: defaultLevel,
		mtimes:   newMtimeStore(dir, log),
		outputs:  NewOutputStore(dir),
	}
	for _, o := range opts {
		o(c)
	}
	if err := c.initSigning(); err != nil {
		return nil, err
	}
	if c.sizeMB == 0 {
		c.sizeMB = parseSizeMB()
	}
	warnIfCoarseMtimeResolution(dir, c.log)
	return c, nil
}

// initSigning builds the signer and verifier from the raw option inputs, so a
// malformed key fails Open rather than at push/import time.
func (c *Cache) initSigning() error {
	if c.signingSeed != nil {
		s, err := newSigner(c.signingSeed)
		if err != nil {
			return err
		}
		c.signer = s
	}
	if c.trustedKeys != nil {
		v, err := newVerifier(c.trustedKeys)
		if err != nil {
			return err
		}
		c.verifier = v
	}
	// Defense in depth: the cache package is the trust boundary, so it enforces its
	// own invariant rather than relying on a caller to. A remote backend with no
	// verifier imports unsigned artifacts, so refuse it unless explicitly opted in.
	if c.remote != nil && c.verifier == nil && !c.insecureRemote {
		return errors.New("magus/cache: remote backend configured without a trust set; " +
			"pass WithTrustedKeys, or WithInsecureRemote to accept unsigned artifacts")
	}
	return nil
}

// sizeCap converts c.sizeMB to a byte count for eviction.
// Returns 0 (no cap) when sizeMB is zero or negative.
func (c *Cache) sizeCap() int64 {
	if c.sizeMB <= 0 {
		return 0
	}
	return int64(c.sizeMB) * (1 << 20)
}

// Stats returns a snapshot of the per-cache counters.
func (c *Cache) Stats() Stats {
	return Stats{Hit: int(c.hits.Load()), Miss: int(c.misses.Load()), Error: int(c.errs.Load())}
}

// Remote returns the configured remote backend, or nil when local-only. Exposed
// so subsystems with their own artifacts (the knowledge-graph shard store) can
// ride the same backend as build artifacts, under the same signing/verification.
func (c *Cache) Remote() RemoteBackend { return c.remote }

// Dir returns the cache's root directory. Subsystems that write sibling artifacts
// next to the cache (the symbol index a `scip` op produces) resolve their paths under
// it, so those artifacts stay out of the working tree.
func (c *Cache) Dir() string { return c.dir }

// Fresh reports whether step s would replay from cache rather than run: its inputs hash
// to a manifest already present locally. It is Run's hash-and-lookup without the
// execution or the remote fetch - a read-only "is this up to date?" probe (e.g. status
// reporting whether a project's symbol index reflects current sources). A missing
// manifest is "not fresh", not an error; only a hashing failure returns one.
func (c *Cache) Fresh(ctx context.Context, s Step) (bool, error) {
	hash, err := c.hashStep(ctx, &s)
	if err != nil {
		return false, err
	}
	_, mErr := c.readManifest(s.ProjectPath, hash)
	return mErr == nil, nil
}

// Run executes fn under the cache. On a hash match it replays recorded outputs;
// otherwise fn runs and its outputs are snapshotted. Per-hash locking prevents
// manifest races when multiple RunAll goroutines share the same key.
func (c *Cache) Run(ctx context.Context, s Step, fn func(context.Context) error, opts ...RunOption) (Result, error) {
	rc := &runCtx{step: &s}
	for _, o := range opts {
		o(rc)
	}
	if rc.onStep != nil {
		rc.onStep(rc.step)
	}

	start := time.Now()
	result := Result{ProjectPath: s.ProjectPath}
	tracer := tracerFromContext(ctx)

	hashCtx, endHash := tracer.StartSpan(ctx, "magus.cache.hash")
	hash, err := c.hashStep(hashCtx, rc.step)
	endHash(err)
	if err != nil {
		return result, fmt.Errorf("magus/cache: hash %q: %w", s.ProjectPath, err)
	}
	result.Hash = hash

	// The inputs that produced the cache key, summarised: the starting point for
	// "why did this rebuild?". Counts (not per-file hashes) keep it cheap and off
	// the pinned hashStep hot path; -vv surfaces it for every step. Diagnostics go
	// to the default logger (stderr), not c.log (stdout cache-result events).
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.LogAttrs(ctx, slog.LevelDebug, "cache.key",
			slog.String("project", s.ProjectPath),
			slog.String("target", s.Target),
			slog.String("hash", shortHash(hash)),
			slog.Int("sources", len(s.Sources)),
			slog.Int("deps", len(s.Deps)),
			slog.Any("tool_versions", s.ToolVersions),
			slog.Any("charms", s.Charms),
			slog.Int("env_allow", len(s.EnvAllow)),
			slog.Bool("no_cache", s.NoCache),
		)
	}

	// hashStep records freshly computed file hashes in the mtime memo; persist
	// them. RunAll defers this to one flush after the whole batch (deferMtimeFlush)
	// so a cold N-step build doesn't rewrite shared shards N times.
	if !rc.deferMtimeFlush {
		c.mtimes.flush(ctx)
	}

	// exportMu.RLock ensures Export/Import cannot race with an active Run.
	c.exportMu.RLock()
	defer c.exportMu.RUnlock()

	unlock, err := hashLocks.acquire(ctx, hash)
	if err != nil {
		return result, err
	}
	defer unlock()

	// NoCache targets (e.g. a long-running fs.watch loop) never consult the cache:
	// skip the replay path so they always run, and the snapshot below is skipped
	// too, so a re-run re-executes instead of replaying a stale success.
	if !s.NoCache {
		manifest, mErr := c.readManifest(s.ProjectPath, hash)
		fromRemote := false
		if mErr != nil && c.remote != nil {
			// Local miss (manifest absent or unreadable): pull the artifact from the
			// remote backend into the local cache, then re-read so the shared hit path
			// below replays it.
			if c.fetchFromRemote(ctx, s.ProjectPath, hash) {
				if m, err := c.readManifest(s.ProjectPath, hash); err == nil {
					manifest, mErr, fromRemote = m, nil, true
				} else {
					c.log.Warn("cache.warn", slog.String("msg",
						fmt.Sprintf("remote manifest %s (%s): %v", s.ProjectPath, shortHash(hash), err)))
				}
			}
		}
		if mErr == nil {
			// Cache hit: local, or just populated from the remote backend.
			replayCtx, endReplay := tracer.StartSpan(ctx, "magus.cache.replay")
			paths, err := c.replay(replayCtx, manifest, s.WorkspaceRoot)
			endReplay(err)
			result.Duration = time.Since(start)
			if err == nil {
				result.Hit = true
				result.Outputs = paths
				c.hits.Add(1)
				logData, _ := os.ReadFile(c.logPath(s.ProjectPath, hash))
				// Quiet mode suppresses log replay; passing projects stay silent.
				if c.logLevel < slog.LevelError && len(logData) > 0 {
					_, _ = os.Stdout.Write(logData)
				}
				// A hit regenerated nothing, so reuse the existing ref for this cache
				// key rather than minting a duplicate; persist fresh only if the store
				// has no record yet (e.g. cache imported without its output store), in
				// which case store the raw cached log verbatim (which also emits a result
				// event via recordOutput).
				ref := ""
				if c.outputs != nil {
					ref = c.outputs.LatestRef(hash)
				}
				if ref == "" {
					ref = c.recordOutput(ctx, s, hash, logData, result.Duration, nil)
				} else {
					// The ref already exists, so recordOutput is skipped and nothing reaches the
					// journal. A cache hit is still a target OUTCOME, so emit a cached result event
					// here; otherwise a fully-cached run's log (and the live viewer) shows the run
					// with no per-target results.
					journal.Emit(ctx, journal.Event{
						Ts:      time.Now().UnixMilli(),
						Inv:     journal.InvocationIDFromContext(ctx),
						Project: s.ProjectPath,
						Target:  reproTarget(s),
						Kind:    journal.KindResult,
						Level:   "info",
						Status:  journal.StatusCached,
						Ref:     ref,
						DurMs:   result.Duration.Milliseconds(),
					})
				}
				event := "cache.hit"
				if fromRemote {
					event = "cache.remote.hit"
				}
				c.log.Info(
					event,
					slog.String("project", s.ProjectPath),
					slog.String("label", s.Label),
					slog.String("target", reproTarget(s)),
					slog.Int64("duration", int64(result.Duration)),
					slog.String("hash", shortHash(hash)),
					slog.String("ref", ref),
				)
				result.Ref = ref
				if rc.onHit != nil {
					rc.onHit(&result)
				}
				rc.fireResults(rc.step, &result, nil)
				return result, nil
			}
			c.log.Warn(
				"cache.warn",
				slog.String("msg", fmt.Sprintf("replay failed for %s (%s); rebuilding", s.ProjectPath, shortHash(hash))),
			)
		}
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	lp := c.logPath(s.ProjectPath, hash)
	// ContextWithCache lets spell bindings (magus.bust_cache) reach the active cache.
	rawOutput, runErr := c.captureRun(ContextWithCache(ctx, c), lp, s.ProjectPath, reproTarget(s), fn)
	if runErr != nil {
		result.Duration = time.Since(start)
		c.errs.Add(1)
		// The captured output is persisted verbatim under a ref so the exact failing
		// output stays retrievable via `magus query ref`.
		ref := c.recordOutput(ctx, s, hash, rawOutput, result.Duration, runErr)
		result.Ref = ref
		c.log.Error(
			"cache.error",
			slog.String("project", s.ProjectPath),
			slog.String("label", s.Label),
			slog.String("target", reproTarget(s)),
			slog.Int64("duration", int64(result.Duration)),
			slog.String("error", runErr.Error()),
			slog.String("ref", ref),
		)
		if rc.onError != nil {
			rc.onError(runErr)
		}
		rc.fireResults(rc.step, &result, runErr)
		return result, runErr
	}

	// fn may "win the race" and return success even after ctx was cancelled. Do
	// not snapshot/push such a run: its outputs may be incomplete, and publishing
	// them would poison the shared remote under a valid key. Surface the
	// cancellation instead so the entry is neither replayed nor exported.
	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	if c.mutable && !s.NoCache {
		_, endSnap := tracer.StartSpan(ctx, "magus.cache.snapshot")
		outs, err := c.snapshot(s, hash)
		endSnap(err)
		if err != nil {
			result.Duration = time.Since(start)
			return result, fmt.Errorf("magus/cache: snapshot %q: %w", s.ProjectPath, err)
		}
		result.Outputs = outs
		if c.remote != nil {
			c.pushToRemote(ctx, s, hash)
		}
		c.evictLRU(ctx, c.sizeCap())
	}

	result.Duration = time.Since(start)
	c.misses.Add(1)
	ref := c.recordOutput(ctx, s, hash, rawOutput, result.Duration, nil)
	result.Ref = ref
	c.log.Info(
		"cache.miss",
		slog.String("project", s.ProjectPath),
		slog.String("label", s.Label),
		slog.String("target", reproTarget(s)),
		slog.Int64("duration", int64(result.Duration)),
		slog.String("ref", ref),
	)
	if rc.onMiss != nil {
		rc.onMiss(&result)
	}
	rc.fireResults(rc.step, &result, nil)
	return result, nil
}

// recordOutput persists a step's captured output events under a per-execution
// reference id and returns that ref for the result log event. It builds the target's
// `result` record (status/duration/error), persists the output events plus that
// result to the ref's JSONL file, and emits the result to the capture logger (for the
// invocation log / live stream). Persistence is best-effort: a store error logs a
// warning and yields an empty ref, so the run's own outcome never hinges on it. runErr
// nil means the step passed (or was a cache hit); non-nil means it failed.
func (c *Cache) recordOutput(ctx context.Context, s Step, hash string, output []byte, dur time.Duration, runErr error) string {
	nowMs := time.Now().UnixMilli()
	inv := journal.InvocationIDFromContext(ctx)
	target := reproTarget(s)

	// The descriptor: identity + outcome of this execution, stored beside the verbatim output
	// blob. The invocation id traces the output back to the run that produced it (`magus query
	// output <ref> -o json` and the viewer surface this).
	d := OutputDescriptor{
		Project:     s.ProjectPath,
		Target:      target,
		Inv:         inv,
		Failed:      runErr != nil,
		TimestampMs: nowMs,
		DurationMs:  dur.Milliseconds(),
	}
	if runErr != nil {
		d.ErrMsg = runErr.Error()
	}

	var ref string
	if c.outputs != nil {
		r, err := c.outputs.Persist(hash, output, d)
		if err != nil {
			c.log.Warn("cache.warn", slog.String("msg",
				fmt.Sprintf("persist output for %s (%s): %v", s.ProjectPath, shortHash(hash), err)))
		} else {
			ref = r
		}
	}

	// Stream the result event to the capture logger (invocation log / live viewer), carrying
	// the freshly minted ref. Per-line output events already reached the journal during capture.
	result := journal.Event{
		Ts: nowMs, Project: s.ProjectPath, Target: target, Kind: journal.KindResult,
		Level: "info", Status: journal.StatusPass, DurMs: dur.Milliseconds(), Inv: inv, Ref: ref,
	}
	if runErr != nil {
		result.Status = journal.StatusFail
		result.Level = "error"
		result.Text = runErr.Error()
	}
	journal.Emit(ctx, result)
	return ref
}

// reproTarget renders the step's target with its active charms the way the CLI
// spells it ("name" or "name:charm1,charm2"), so the pretty reporter can print a
// copy-pasteable `magus run <target> <project>` for the just-run project.
func reproTarget(s Step) string {
	if len(s.Charms) == 0 {
		return s.Target
	}
	return s.Target + ":" + strings.Join(s.Charms, ",")
}

// RunAll schedules steps concurrently (bounded by WithConcurrency/WithLimiter).
// Step.DependsOn imposes scheduling order for in-scope steps only; out-of-scope
// deps are ignored. A cyclic DependsOn graph is rejected before any goroutine
// launches. Upstream cache keys fold into dependent Step.Deps transitively
// (happens-before: markDone writes the key before waitForDeps returns in
// dependents). Every goroutine launches immediately and blocks on deps without
// holding a slot, so the pool never deadlocks and g.Wait() always drains cleanly.
func (c *Cache) RunAll(ctx context.Context, steps []Step, fn func(context.Context, Step) error, opts ...RunOption) ([]Result, error) {
	rc := &runCtx{}
	for _, o := range opts {
		o(rc)
	}

	lim := rc.limiter
	if lim == nil {
		n := DefaultConcurrency()
		if rc.concurrency > 0 {
			n = rc.concurrency
		}
		lim = NewLimiter(n)
	}

	if err := checkAcyclic(steps); err != nil {
		return nil, err
	}

	barrier := newDepBarrier(steps)

	var keysMu sync.Mutex
	resolvedKeys := make(map[string]string, len(steps))

	// isolationMu serializes Step.Exclusive steps against the whole batch: an
	// exclusive step takes the write lock (runs alone); every other step takes the
	// read lock (runs in parallel with peers but never alongside an exclusive step).
	//
	// Ordering is load-bearing: take the lock *after* waitForDeps (so an exclusive
	// step's own deps aren't blocked by its own writer intent) and *before* the
	// limiter slot (so a pending writer never holds a slot and starves a dependent
	// of the slot it needs). That ordering is also why the lock spans the *whole*
	// c.Run below, not just fn: moving it inside c.Run would put it after the slot
	// and reintroduce the starvation it avoids. The cost is that an exclusive
	// *cache hit* also serializes its replay, which is fine: exclusive targets are
	// rare and typically NoCache. A batch with no exclusive steps only ever takes
	// uncontended read locks.
	var isolationMu sync.RWMutex
	acquireIsolation := func(exclusive bool) func() {
		if exclusive {
			isolationMu.Lock()
			return isolationMu.Unlock
		}
		isolationMu.RLock()
		return isolationMu.RUnlock
	}

	// optimization: coalesce the mtime-store flush to once per batch instead of once
	// per step. A per-step flush rewrites every shard a completing step shares with
	// earlier steps, so a cold N-step build's shard writes grow super-linearly.
	//   measured: BenchmarkRunAllColdMtime (24 projects x 64 files) -44.9% sec/op,
	//   -81.0% B/op, -28.7% allocs/op (benchstat, n=10, p=0.000).
	//   trade-off: a build killed mid-flight persists the memo once at the end
	//   (WithoutCancel below) rather than incrementally per spell; equivalent net
	//   coverage, slightly more re-hashing only if the process is hard-killed.
	// Member Run calls skip the per-call mtime flush; the batch flushes once below.
	opts = append(opts, deferMtimeFlush())

	results := make([]Result, len(steps))
	g, gctx := errgroup.WithContext(ctx)
	for i, s := range steps {
		g.Go(func() error {
			// markDone on every exit so a failing upstream cascades cancellation.
			defer barrier.markDone(stepKey(s))

			// Trace the DAG progression (blocked-on-deps, then admitted) so a
			// "why is this serialized / what is it waiting on?" question can be read
			// off the log at -vvv without a profiler.
			if len(s.DependsOn) > 0 && slog.Default().Enabled(gctx, levelTrace) {
				slog.LogAttrs(gctx, levelTrace, "schedule.wait",
					slog.String("project", s.ProjectPath), slog.String("target", s.Target),
					slog.Any("depends_on", s.DependsOn))
			}
			// Wait for upstreams before acquiring a slot: holding a slot while
			// blocked on a dep would deadlock a saturated limiter.
			if err := barrier.waitForDeps(gctx, s); err != nil {
				return err
			}
			defer acquireIsolation(s.Exclusive)()
			// acquireIsolation's Lock/RLock is not ctx-aware, so a goroutine can
			// park there uninterruptibly while a sibling fails. Re-check after it
			// returns and bail before running fn. lim.Acquire below would catch a
			// cancelled gctx too, except on an unlimited limiter, where it returns
			// nil without consulting ctx.
			if err := gctx.Err(); err != nil {
				return err
			}
			// A heavy step can request extra slots (Step.Slots) to throttle parallel
			// work around itself. Clamp to [1, budget]: 0 means one slot, and a
			// request above the budget would exceed capacity and error, so cap it. A
			// request >= the budget holds every slot, so no peer can enter fn while it
			// runs (it does not take the isolation lock, so unlike an exclusive step it
			// does not also serialize replays). On an unlimited limiter (budget <= 0)
			// there is nothing to throttle against, so the request is a no-op:
			// AcquireN returns immediately.
			slots := s.Slots
			if slots < 1 {
				slots = 1
			}
			if budget := lim.Capacity(); budget > 0 && slots > budget {
				slots = budget
			}
			if err := lim.AcquireN(gctx, slots); err != nil {
				return err
			}
			defer lim.ReleaseN(slots)
			if slog.Default().Enabled(gctx, levelTrace) {
				slog.LogAttrs(gctx, levelTrace, "schedule.run",
					slog.String("project", s.ProjectPath), slog.String("target", s.Target),
					slog.Bool("exclusive", s.Exclusive), slog.Int("slots", slots))
			}

			// Fold upstream keys into Deps for transitive cache-key propagation.
			// After edges are ordering-only and excluded from the key.
			if len(s.DependsOn) > 0 {
				keysMu.Lock()
				depKeys := make([]string, 0, len(s.DependsOn))
				for _, dep := range s.DependsOn {
					if k, ok := resolvedKeys[DepKey(dep, s.Target)]; ok {
						depKeys = append(depKeys, k)
					}
				}
				keysMu.Unlock()
				if len(depKeys) > 0 {
					s.Deps = slices.Concat(s.Deps, depKeys)
				}
			}

			r, err := c.Run(gctx, s, func(ctx context.Context) error {
				ctx = ContextWithLimiter(ctx, lim)
				ctx = WithSlotsHeld(ctx, slots)
				return fn(ctx, s)
			}, opts...)
			// Write key before markDone; the markDone→waitForDeps happens-before edge
			// ensures dependents see the key when they unblock.
			if r.Hash != "" {
				keysMu.Lock()
				resolvedKeys[stepKey(s)] = r.Hash
				keysMu.Unlock()
			}
			results[i] = r
			return err
		})
	}
	err := g.Wait()
	// Flush the shared mtime memo once for the whole batch. WithoutCancel so a
	// cancelled run still persists the hashing already done (the per-step flush this
	// replaced also persisted completed steps before cancellation).
	c.mtimes.flush(context.WithoutCancel(ctx))
	return results, err
}

// Clean removes cached manifests for the given project paths (all if none given).
// Orphaned blobs are GC'd after manifests are deleted.
func (c *Cache) Clean(ctx context.Context, projectPaths ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	manifestsDir := filepath.Join(c.dir, "manifests")
	logsDir := filepath.Join(c.dir, "logs")
	outputsDir := filepath.Join(c.dir, "outputs")

	if len(projectPaths) == 0 {
		// Clean everything.
		for _, sub := range []string{manifestsDir, logsDir, outputsDir} {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, err := os.ReadDir(sub)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return fmt.Errorf("magus/cache: clean list %s: %w", sub, err)
			}
			for _, e := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := os.RemoveAll(filepath.Join(sub, e.Name())); err != nil {
					return fmt.Errorf("magus/cache: clean remove %s: %w", e.Name(), err)
				}
			}
		}
	} else {
		for _, path := range projectPaths {
			if err := ctx.Err(); err != nil {
				return err
			}
			flat := flattenPath(path)
			for _, sub := range []string{manifestsDir, logsDir} {
				if err := os.RemoveAll(filepath.Join(sub, flat)); err != nil {
					return fmt.Errorf("magus/cache: clean remove %s: %w", path, err)
				}
			}
			// The output store is keyed by cache key, not project path, so it can't
			// be wiped by a flattened-path RemoveAll; match its metadata on project.
			if c.outputs != nil {
				c.outputs.removeForProject(path)
			}
		}
	}
	return c.gcBlobs(ctx)
}

// safeCachePath joins name (a tar entry's slash-separated path) onto the cache
// root and rejects any result that escapes it. It is the single definition of the
// path-traversal guard shared by [Cache.Import] and importArtifact.
func (c *Cache) safeCachePath(name string) (string, error) {
	clean := filepath.Clean(filepath.Join(c.dir, filepath.FromSlash(name)))
	if !strings.HasPrefix(clean, c.dir+string(filepath.Separator)) && clean != c.dir {
		return "", fmt.Errorf("magus/cache: unsafe path %q", name)
	}
	return clean, nil
}

// importLimit is the per-entry byte cap applied when extracting a tar, guarding
// against tar-bomb input. Shared by [Cache.Import] and importArtifact.
func (c *Cache) importLimit() int64 {
	if c.maxImportBytes == 0 {
		return defaultMaxImportBytes
	}
	return c.maxImportBytes
}

// Export writes the cache as a gzip-compressed tar archive (paths relative to
// the cache root, so Import can extract into any target directory).
func (c *Cache) Export(ctx context.Context, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.exportMu.Lock()
	defer c.exportMu.Unlock()

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	err := filepath.WalkDir(c.dir, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(c.dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     rel + "/",
				Mode:     0o755,
			})
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     rel,
			Size:     info.Size(),
			Mode:     0o644,
			ModTime:  info.ModTime(),
		}); err != nil {
			return err
		}
		return func() error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}()
	})
	if err != nil {
		return fmt.Errorf("magus/cache: export walk: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("magus/cache: export tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("magus/cache: export gzip close: %w", err)
	}
	return nil
}

// Import extracts a gzip-compressed tar archive produced by Export into the cache directory.
// Existing files are overwritten; entries older than what is on disk are skipped.
func (c *Cache) Import(ctx context.Context, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.exportMu.Lock()
	defer c.exportMu.Unlock()

	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("magus/cache: import gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("magus/cache: import tar: %w", err)
		}

		clean, err := c.safeCachePath(hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(clean, 0o755); err != nil {
				return fmt.Errorf("magus/cache: import mkdir %q: %w", clean, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return fmt.Errorf("magus/cache: import mkdir parent: %w", err)
			}
			if !hdr.ModTime.IsZero() {
				if existing, err := os.Stat(clean); err == nil && existing.ModTime().After(hdr.ModTime) {
					continue
				}
			}
			f, err := os.Create(clean)
			if err != nil {
				return fmt.Errorf("magus/cache: import create %q: %w", clean, err)
			}
			// LimitReader caps per-entry bytes to guard against tar-bomb input.
			if _, err := io.Copy(f, io.LimitReader(tr, c.importLimit())); err != nil {
				_ = f.Close()
				return fmt.Errorf("magus/cache: import write %q: %w", clean, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("magus/cache: import close %q: %w", clean, err)
			}
		}
	}
	return nil
}

// GC evicts LRU entries to the size cap and removes unreferenced CAS blobs.
func (c *Cache) GC(ctx context.Context) error {
	c.evictLRU(ctx, c.sizeCap())
	return c.gcBlobs(ctx)
}

// gcBlobs removes CAS blobs not referenced by any surviving manifest.
func (c *Cache) gcBlobs(ctx context.Context) error {
	casDir := filepath.Join(c.dir, "cas")
	manifestsDir := filepath.Join(c.dir, "manifests")

	referenced := map[string]struct{}{}
	if err := filepath.WalkDir(manifestsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		data, err := os.ReadFile(p) // p is always under c.dir; symlink escapes are not a concern for a local cache
		if err != nil {
			c.log.Warn("cache.warn", slog.String("msg", fmt.Sprintf("gc: read %s: %v", p, err)))
			return nil
		}
		var m Manifest
		if jErr := codec.Unmarshal(data, &m); jErr != nil {
			c.log.Warn("cache.warn", slog.String("msg", fmt.Sprintf("gc: corrupt manifest %s: %v", p, jErr)))
			return nil
		}
		for _, out := range m.Outputs {
			if out.Blob != "" {
				referenced[out.Blob] = struct{}{}
			}
		}
		return nil
	}); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("magus/cache: gc walk manifests: %w", err)
	}

	if err := filepath.WalkDir(casDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		blob := filepath.Base(p)
		if _, ok := referenced[blob]; !ok {
			_ = os.Remove(p) // p is always under c.dir; symlink escapes are not a concern for a local cache
		}
		return nil
	}); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("magus/cache: gc walk cas: %w", err)
	}
	return nil
}

func (c *Cache) logPath(projectPath, hash string) string {
	return filepath.Join(c.dir, "logs", flattenPath(projectPath), hash+".log")
}

// captureRun runs fn while teeing stdout/stderr to logPath via context writers.
// Quiet mode (logLevel >= Error) suppresses live terminal output; on failure it
// dumps the captured log to stderr.
//
// Silent mode (WithSilent) bubbles up only target-marked notice lines, and on
// failure dumps just the log's tail.
//
// The log file is retained in every mode (pass or fail): Run persists it to the
// output store under a target-output ref, so a failing target's exact output stays
// retrievable via `magus query ref...`. It is overwritten on the next run of the key.
//
// Alongside the raw logF (which drives the terminal replay/dumps below, unchanged),
// output is line-tapped into structured journal events tagged with project/target/
// stream. The records are returned for the output store and streamed to the run's
// sink; the raw paths are untouched so the live view and failure dumps stay verbatim.
func (c *Cache) captureRun(ctx context.Context, logPath, projectPath, target string, fn func(context.Context) error) ([]byte, error) {
	quiet := c.logLevel >= slog.LevelError
	// Collapse withholds live output the same way quiet does, but at default
	// verbosity: a passing project shows only its status line; a failing one has its
	// captured output replayed below. Silent has its own stricter rules, so it takes
	// precedence and collapse stays off under it.
	collapse := c.collapse && !c.silent && !quiet
	withhold := quiet || collapse

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fn(ctx)
	}
	logF, err := os.Create(logPath)
	if err != nil {
		return nil, fn(ctx)
	}

	col := newLineEmitter(ctx, projectPath, target)
	// os/exec drives stdout and stderr from separate goroutines, so both taps write to the log
	// file concurrently; guard it so lines never interleave mid-write in the durable log. The
	// same guarded stream is tee'd into rawBuf so captureRun returns the VERBATIM bytes (what
	// the process wrote, in write order) - the output store keeps these as-is, no reconstruction.
	var rawBuf bytes.Buffer
	safeLogF := &syncWriter{w: io.MultiWriter(logF, &rawBuf)}
	var stdoutTap, stderrTap *lineTap
	if withhold {
		stdoutTap = col.newLineTap(safeLogF, journal.StreamStdout)
		stderrTap = col.newLineTap(safeLogF, journal.StreamStderr)
	} else {
		stdoutTap = col.newLineTap(io.MultiWriter(os.Stdout, safeLogF), journal.StreamStdout)
		stderrTap = col.newLineTap(io.MultiWriter(os.Stderr, safeLogF), journal.StreamStderr)
	}
	captureCtx := runPkg.WithOutputWriters(ctx, stdoutTap, stderrTap)
	// Tag the step so subprocesses run under fn emit exec events labeled with this
	// project/target (the run primitive reads this from ctx; see internal/proc/run).
	captureCtx = journal.WithStep(captureCtx, projectPath, target)

	runErr := fn(captureCtx)
	// Emit any trailing partial lines before the records are read.
	stdoutTap.flush()
	stderrTap.flush()
	if cerr := logF.Close(); cerr != nil && runErr == nil {
		runErr = cerr
	}

	// Silent mode bubbles up only the lines the target marked as notices, the
	// sole output for an otherwise-silent passing run.
	if c.silent {
		for _, msg := range extractNotices(logPath) {
			_, _ = fmt.Fprintf(os.Stderr, "notice: %s: %s\n", projectPath, msg)
		}
	}

	if runErr != nil {
		switch {
		case c.silent:
			// Bound the dump to the log's tail and keep the full log so its path resolves.
			if data, readErr := os.ReadFile(logPath); readErr == nil && len(data) > 0 {
				tail, omitted := tailLines(data, maxFailTailLines)
				_, _ = fmt.Fprintf(os.Stderr, "\n-- %s (failed) --\n", projectPath)
				if omitted > 0 {
					_, _ = fmt.Fprintf(os.Stderr, "... %d earlier line(s) omitted; full log: %s\n", omitted, logPath)
				}
				_, _ = os.Stderr.Write(tail)
				_, _ = fmt.Fprintln(os.Stderr)
			}
		case collapse:
			// The live view (status + indented stage lines) went to stderr while the
			// project ran. On failure replay the raw, unindented subprocess output to
			// stdout so it stays copy/paste and pipe friendly (2>/dev/null yields just
			// the failures). The header is part of the live view, hence stderr.
			if data, readErr := os.ReadFile(logPath); readErr == nil && len(data) > 0 {
				_, _ = fmt.Fprintf(os.Stderr, "\n-- %s (failed) --\n", projectPath)
				_, _ = os.Stdout.Write(data)
				if data[len(data)-1] != '\n' {
					_, _ = fmt.Fprintln(os.Stdout)
				}
			}
		case quiet:
			if data, readErr := os.ReadFile(logPath); readErr == nil && len(data) > 0 {
				_, _ = fmt.Fprintf(os.Stderr, "\n-- %s (failed) --\n", projectPath)
				_, _ = os.Stderr.Write(data)
				_, _ = fmt.Fprintln(os.Stderr)
			}
		}
		// The failed log is retained (not removed): Run persists it to the output
		// store under a ref so the exact failing output stays retrievable via
		// `magus query ref...`. It is overwritten on the next run of the same key.
	}
	return rawBuf.Bytes(), runErr
}
