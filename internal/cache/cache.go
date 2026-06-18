// Package cache implements magus's content-addressed build cache.
// Layout: cas/ (blobs) + manifests/ + logs/. The store is local; an optional
// pluggable [RemoteBackend] (see remote.go) lets a miss restore from, and a build
// publish to, a shared store.
package cache

import (
	"archive/tar"
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
	runPkg "github.com/egladman/magus/internal/run"
)

// Option configures a Cache at open time.
type Option func(*Cache)

// WithLogger replaces the default logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *Cache) { c.log = l }
}

// WithMutable controls whether the cache writes new entries on a miss (default true).
func WithMutable(mutable bool) Option {
	return func(c *Cache) { c.mutable = mutable }
}

// WithSigningKey sets the Ed25519 seed (32 bytes) used to sign artifacts on push.
// Set only in trusted CI; without it the cache cannot publish trusted artifacts.
func WithSigningKey(seed []byte) Option {
	return func(c *Cache) { c.signingSeed = seed }
}

// WithTrustedKeys sets the raw Ed25519 public keys (32 bytes each) that remote
// artifacts must be signed by. A non-empty set makes verification mandatory.
func WithTrustedKeys(pubkeys [][]byte) Option {
	return func(c *Cache) { c.trustedKeys = pubkeys }
}

// WithInsecureRemote allows a remote backend to run with no trust set, importing
// unsigned artifacts without authentication. Open otherwise refuses that
// combination. Only for a fully trusted store (e.g. a local cross-workspace cache);
// never for a shared cache that an untrusted party could write.
func WithInsecureRemote() Option {
	return func(c *Cache) { c.insecureRemote = true }
}

// WithSizeMB caps cache disk usage to n MiB. 0 means unlimited.
func WithSizeMB(n int) Option {
	return func(c *Cache) { c.sizeMB = n }
}

// WithMaxImportBytes sets the per-entry byte cap used by Import (default 10 GiB).
func WithMaxImportBytes(n int64) Option {
	return func(c *Cache) {
		if n > 0 {
			c.maxImportBytes = n
		}
	}
}

// WithLog sets the log format ("pretty", "text", "json") and minimum level.
func WithLog(format string, level slog.Level) Option {
	return func(c *Cache) {
		c.log = newLogger(format, level)
		c.logLevel = level
	}
}

// Cache is an on-disk content-addressed build cache handle.
type Cache struct {
	dir            string
	mutable        bool // true = read+write (default); false = read-only
	sizeMB         int
	maxImportBytes int64 // per-entry cap for Import; 0 uses defaultMaxImportBytes
	log            *slog.Logger
	logLevel       slog.Level // effective minimum level; used by captureRun
	hits           atomic.Int64
	misses         atomic.Int64
	errs           atomic.Int64
	mtimes         *mtimeStore   // mtime fast-path for source hashing
	exportMu       sync.RWMutex  // guards Export/Import against concurrent Run writes
	evictMu        sync.Mutex    // serialises evictLRU so concurrent Runs don't over-evict each other's fresh manifests
	remote         RemoteBackend // optional remote backend; nil = local-only

	// Remote-artifact signing: signer signs on push, verifier authenticates on import.
	// signingSeed/trustedKeys hold the raw option inputs until Open builds the keys,
	// so option application stays error-free and keys are validated once.
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

// Spec is the hashable description of a cached build step.
type Spec struct {
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
	NoCache         bool     // when true, always run fn — never replay or snapshot (long-running targets)
	Isolated        bool     // RunAll only: when true, runs alone — no other spec in the batch runs concurrently with it (ignored by Run, which has no batch)
}

// Result is the outcome of a Cache.Run call.
type Result struct {
	ProjectPath string
	Hash        string
	Hit         bool
	Duration    time.Duration
	Outputs     []string // absolute paths written or replayed
}

type runCtx struct {
	spec        *Spec
	concurrency int
	limiter     *Limiter
	onHit       func(*Result)
	onMiss      func(*Result)
	onError     func(error)
	onSpec      func(*Spec)
	onResult    func(*Spec, *Result, error)
	// deferMtimeFlush suppresses the per-Run mtime flush so a RunAll batch can
	// flush once after all specs complete instead of once per spec.
	deferMtimeFlush bool
}

// deferMtimeFlush is an internal RunOption set by RunAll so member Run calls skip
// the per-call mtime flush; RunAll flushes the shared store once after the batch.
func deferMtimeFlush() RunOption {
	return func(rc *runCtx) { rc.deferMtimeFlush = true }
}

// RunOption configures a single Cache.Run (or RunAll) invocation.
type RunOption func(*runCtx)

// OnHit fires after a cache hit replay.
func OnHit(fn func(*Result)) RunOption {
	return func(rc *runCtx) { rc.onHit = fn }
}

// OnMiss fires after a successful cache miss (fn returned no error).
func OnMiss(fn func(*Result)) RunOption {
	return func(rc *runCtx) { rc.onMiss = fn }
}

// OnError fires when fn returns an error.
func OnError(fn func(error)) RunOption {
	return func(rc *runCtx) { rc.onError = fn }
}

// OnResult fires after every Cache.Run regardless of outcome (after OnHit/OnMiss/OnError).
func OnResult(fn func(*Spec, *Result, error)) RunOption {
	return func(rc *runCtx) { rc.onResult = fn }
}

// OnSpec fires before hashing, allowing the caller to mutate the Spec (e.g. extend EnvAllow).
func OnSpec(fn func(*Spec)) RunOption {
	return func(rc *runCtx) { rc.onSpec = fn }
}

// WithConcurrency caps in-flight RunAll builds. 1 = serial, 0 = unlimited.
// WithLimiter takes precedence when both are supplied.
func WithConcurrency(n int) RunOption {
	return func(rc *runCtx) { rc.concurrency = n }
}

// WithLimiter shares an external Limiter with RunAll instead of creating a private one,
// so in-process tasks and nested calls compete for the same concurrency budget.
func WithLimiter(l *Limiter) RunOption {
	return func(rc *runCtx) { rc.limiter = l }
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
	// verifier imports unsigned artifacts — refuse it unless explicitly opted in.
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

// Run executes fn under the cache. On a hash match it replays recorded outputs;
// otherwise fn runs and its outputs are snapshotted. Per-hash locking prevents
// manifest races when multiple RunAll goroutines share the same key.
func (c *Cache) Run(ctx context.Context, s Spec, fn func(context.Context) error, opts ...RunOption) (Result, error) {
	rc := &runCtx{spec: &s}
	for _, o := range opts {
		o(rc)
	}
	if rc.onSpec != nil {
		rc.onSpec(rc.spec)
	}

	start := time.Now()
	result := Result{ProjectPath: s.ProjectPath}
	tracer := tracerFromContext(ctx)

	hashCtx, endHash := tracer.StartSpan(ctx, "magus.cache.hash")
	hash, err := c.hashSpec(hashCtx, rc.spec)
	endHash(err)
	if err != nil {
		return result, fmt.Errorf("magus/cache: hash %q: %w", s.ProjectPath, err)
	}
	result.Hash = hash

	// hashSpec records freshly computed file hashes in the mtime memo; persist
	// them. RunAll defers this to one flush after the whole batch (deferMtimeFlush)
	// so a cold N-spec build doesn't rewrite shared shards N times.
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
			// Cache hit — local, or just populated from the remote backend.
			replayCtx, endReplay := tracer.StartSpan(ctx, "magus.cache.replay")
			paths, err := c.replay(replayCtx, manifest, s.WorkspaceRoot)
			endReplay(err)
			result.Duration = time.Since(start)
			if err == nil {
				result.Hit = true
				result.Outputs = paths
				c.hits.Add(1)
				// Quiet mode suppresses log replay; passing projects stay silent.
				if c.logLevel < slog.LevelError {
					if data, _ := os.ReadFile(c.logPath(s.ProjectPath, hash)); len(data) > 0 {
						_, _ = os.Stdout.Write(data)
					}
				}
				event := "cache.hit"
				if fromRemote {
					event = "cache.remote.hit"
				}
				c.log.Info(
					event,
					slog.String("project", s.ProjectPath),
					slog.String("target", reproTarget(s)),
					slog.Int64("duration", int64(result.Duration)),
					slog.String("hash", shortHash(hash)),
				)
				if rc.onHit != nil {
					rc.onHit(&result)
				}
				if rc.onResult != nil {
					rc.onResult(rc.spec, &result, nil)
				}
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
	runErr := c.captureRun(ContextWithCache(ctx, c), lp, s.ProjectPath, fn)
	if runErr != nil {
		result.Duration = time.Since(start)
		c.errs.Add(1)
		c.log.Error(
			"cache.error",
			slog.String("project", s.ProjectPath),
			slog.String("target", reproTarget(s)),
			slog.Int64("duration", int64(result.Duration)),
			slog.String("error", runErr.Error()),
		)
		if rc.onError != nil {
			rc.onError(runErr)
		}
		if rc.onResult != nil {
			rc.onResult(rc.spec, &result, runErr)
		}
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
	c.log.Info(
		"cache.miss",
		slog.String("project", s.ProjectPath),
		slog.String("target", reproTarget(s)),
		slog.Int64("duration", int64(result.Duration)),
	)
	if rc.onMiss != nil {
		rc.onMiss(&result)
	}
	if rc.onResult != nil {
		rc.onResult(rc.spec, &result, nil)
	}
	return result, nil
}

// reproTarget renders the spec's target with its active charms the way the CLI
// spells it — "name" or "name:charm1,charm2" — so the pretty reporter can print a
// copy-pasteable `magus run <target> <project>` for the just-run project.
func reproTarget(s Spec) string {
	if len(s.Charms) == 0 {
		return s.Target
	}
	return s.Target + ":" + strings.Join(s.Charms, ",")
}

// RunAll schedules specs concurrently (bounded by WithConcurrency/WithLimiter).
// Spec.DependsOn imposes scheduling order for in-scope specs only; out-of-scope
// deps are ignored. A cyclic DependsOn graph is rejected before any goroutine
// launches. Upstream cache keys are folded into dependent Spec.Deps transitively
// (happens-before: markDone writes the key before waitForDeps returns in dependents).
// Every goroutine is launched immediately and blocks on deps without holding a
// slot, so the pool never deadlocks and g.Wait() always drains cleanly.
func (c *Cache) RunAll(ctx context.Context, specs []Spec, fn func(context.Context, Spec) error, opts ...RunOption) ([]Result, error) {
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

	if err := checkAcyclic(specs); err != nil {
		return nil, err
	}

	barrier := newDepBarrier(specs)

	var keysMu sync.Mutex
	resolvedKeys := make(map[string]string, len(specs))

	// isolationMu serializes Spec.Isolated specs against the whole batch: an
	// isolated spec takes the write lock (runs alone); every other spec takes the
	// read lock (runs in parallel with peers but never alongside an isolated spec).
	//
	// Ordering is load-bearing: the lock is taken *after* waitForDeps (so an isolated
	// spec's own deps aren't blocked by its own writer intent) and *before* the
	// limiter slot (so a pending writer never holds a slot — which would let it
	// starve a dependent of the slot it needs). That ordering is also why the lock
	// necessarily spans the *whole* c.Run below, not just fn: moving it inside
	// c.Run would put it after the slot and reintroduce the starvation it avoids.
	// The cost is that an isolated *cache hit* also serializes its replay, which is
	// fine — isolated targets are rare and typically NoCache. A batch with no
	// isolated specs only ever takes uncontended read locks.
	var isolationMu sync.RWMutex
	acquireIsolation := func(isolated bool) func() {
		if isolated {
			isolationMu.Lock()
			return isolationMu.Unlock
		}
		isolationMu.RLock()
		return isolationMu.RUnlock
	}

	// ultra-opt: coalesce the mtime-store flush to once per batch instead of once
	// per spec. A per-spec flush rewrites every shard a completing spec shares with
	// earlier specs, so a cold N-spec build's shard writes grow super-linearly.
	//   measured: BenchmarkRunAllColdMtime (24 projects × 64 files) -44.9% sec/op,
	//   -81.0% B/op, -28.7% allocs/op (benchstat, n=10, p=0.000).
	//   trade-off: a build killed mid-flight persists the memo once at the end
	//   (WithoutCancel below) rather than incrementally per spell; equivalent net
	//   coverage, slightly more re-hashing only if the process is hard-killed.
	// Member Run calls skip the per-call mtime flush; the batch flushes once below.
	opts = append(opts, deferMtimeFlush())

	results := make([]Result, len(specs))
	g, gctx := errgroup.WithContext(ctx)
	for i, s := range specs {
		g.Go(func() error {
			// markDone on every exit so a failing upstream cascades cancellation.
			defer barrier.markDone(specKey(s))

			// Wait for upstreams before acquiring a slot: holding a slot while
			// blocked on a dep would deadlock a saturated limiter.
			if err := barrier.waitForDeps(gctx, s); err != nil {
				return err
			}
			defer acquireIsolation(s.Isolated)()
			// acquireIsolation's Lock/RLock is not ctx-aware, so a goroutine can
			// park there uninterruptibly while a sibling fails. Re-check after it
			// returns and bail before running fn — lim.Acquire below would catch a
			// cancelled gctx too, except on an unlimited limiter, where it returns
			// nil without consulting ctx.
			if err := gctx.Err(); err != nil {
				return err
			}
			if err := lim.Acquire(gctx); err != nil {
				return err
			}
			defer lim.Release()

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
				ctx = WithSlotHeld(ctx)
				return fn(ctx, s)
			}, opts...)
			// Write key before markDone; the markDone→waitForDeps happens-before edge
			// ensures dependents see the key when they unblock.
			if r.Hash != "" {
				keysMu.Lock()
				resolvedKeys[specKey(s)] = r.Hash
				keysMu.Unlock()
			}
			results[i] = r
			return err
		})
	}
	err := g.Wait()
	// Flush the shared mtime memo once for the whole batch. WithoutCancel so a
	// cancelled run still persists the hashing already done (the per-spec flush it
	// replaced also persisted completed specs before cancellation).
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

	if len(projectPaths) == 0 {
		// Clean everything.
		for _, sub := range []string{manifestsDir, logsDir} {
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
		data, err := os.ReadFile(p) //nolint:gosec // p is always under c.dir; symlink escapes are not a concern for a local cache
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
			_ = os.Remove(p) //nolint:gosec // p is always under c.dir; symlink escapes are not a concern for a local cache
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
// dumps the captured log to stderr. On failure the log file is removed (not replayable).
func (c *Cache) captureRun(ctx context.Context, logPath, projectPath string, fn func(context.Context) error) error {
	quiet := c.logLevel >= slog.LevelError

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fn(ctx)
	}
	logF, err := os.Create(logPath)
	if err != nil {
		return fn(ctx)
	}

	var captureCtx context.Context
	if quiet {
		captureCtx = runPkg.WithOutputWriters(ctx, logF, logF)
	} else {
		stdout := io.MultiWriter(os.Stdout, logF)
		stderr := io.MultiWriter(os.Stderr, logF)
		captureCtx = runPkg.WithOutputWriters(ctx, stdout, stderr)
	}

	runErr := fn(captureCtx)
	if cerr := logF.Close(); cerr != nil && runErr == nil {
		runErr = cerr
	}
	if runErr != nil {
		if quiet {
			if data, readErr := os.ReadFile(logPath); readErr == nil && len(data) > 0 {
				_, _ = fmt.Fprintf(os.Stderr, "\n── %s (failed) ──\n", projectPath)
				_, _ = os.Stderr.Write(data)
				_, _ = fmt.Fprintln(os.Stderr)
			}
		}
		if rmErr := os.Remove(logPath); rmErr != nil && !os.IsNotExist(rmErr) {
			c.log.Warn("cache.warn", slog.String("msg", fmt.Sprintf("remove partial log %s: %v", logPath, rmErr)))
		}
	}
	return runErr
}
