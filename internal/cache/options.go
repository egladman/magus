package cache

import "log/slog"

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

// WithSilent enables silent output mode: on top of quiet's suppression, a failing
// project's dump is bounded to its tail (with a pointer to the retained full log)
// and only target-marked important lines are bubbled up. See captureRun.
func WithSilent(silent bool) Option {
	return func(c *Cache) { c.silent = silent }
}

// WithCollapse enables collapse-on-success output: a project's subprocess output is
// captured rather than streamed live, so a passing project shows only its one-line
// status. On failure the captured output is replayed in full under the project so the
// error is not lost. Intended for interactive (TTY) pretty runs at default verbosity;
// callers should leave it off for non-TTY/CI so logs stay complete, and -v streams
// live instead. See captureRun.
func WithCollapse(collapse bool) Option {
	return func(c *Cache) { c.collapse = collapse }
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
func OnResult(fn func(*Step, *Result, error)) RunOption {
	return func(rc *runCtx) { rc.onResult = fn }
}

// OnStep fires before hashing, allowing the caller to mutate the Step (e.g. extend EnvAllow).
func OnStep(fn func(*Step)) RunOption {
	return func(rc *runCtx) { rc.onStep = fn }
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
