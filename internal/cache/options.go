package cache

import (
	"context"
	"log/slog"
)

// Option configures a Cache at open time.
type Option func(*Cache)

// WithLogger replaces the default logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *Cache) { c.log = l }
}

// WithLocalWrite controls whether a run writes the local tier (default true). Off, the
// cache replays hits and writes nothing, the remote tier included, and a remote-tier
// hit replays from a staging directory rather than being promoted.
func WithLocalWrite(enabled bool) Option {
	return func(c *Cache) { c.localWrite = enabled }
}

// WithRemoteWrite declares whether a run writes the remote tier. Without it the remote
// tier is written whenever the local tier is and a signed entry can be. Declared true,
// it is a requirement: Open errors when it cannot be honored, and a failed remote write
// fails the step. Declared false, the remote tier is only read.
func WithRemoteWrite(enabled bool) Option {
	return func(c *Cache) { c.remoteWrite = &enabled }
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

// WithLog sets the log format ("pretty", "plain", "text", "json") and minimum level.
func WithLog(format string, level slog.Level) Option {
	return func(c *Cache) {
		c.log = newLogger(format, level)
		c.logLevel = level
	}
}

// WithRecordOnlyOutput makes every line the cache writes to the terminal a record, for a
// -o jsonl run: h receives the cache's log records, and the free text it would otherwise
// print is withheld. That is a target's own output on success or failure, the failure
// dump, and the unchanged-failure hint; the output stays in the run log behind the ref
// the run's result record names. h's own level filters the log records.
func WithRecordOnlyOutput(h slog.Handler) Option {
	return func(c *Cache) {
		c.log = slog.New(jsonlSafetyNetHandler{h})
		c.recordsOnly = true
	}
}

// WithSilent enables silent output mode: on top of quiet's suppression, a failing
// project's dump is bounded to its tail (with a pointer to the retained full log)
// and only target-marked important lines are bubbled up. See captureRun.
func WithSilent(silent bool) Option {
	return func(c *Cache) { c.silent = silent }
}

// WithCollapse enables collapse-on-success output: a project's subprocess output is
// captured. Failures show an excerpt; the output ref keeps the full log.
func WithCollapse(collapse bool) Option {
	return func(c *Cache) { c.collapse = collapse }
}

// WithMachineAdmission routes every step through machine-wide admission: it takes its
// concurrency slots and declared memory_mb from a budget shared by every magus on the
// host. A step that does not fit fails fast (MGS3009, exit 75); magus never queues
// behind a peer already holding the budget.
//
// admitter must reach the ONE arbiter for this machine, which is the user's broker.
// Omitting the option leaves admission per-process, which is what a library caller
// with no host to arbitrate wants.
//
// A step the admitter cannot answer for runs unarbitrated, said once through the
// caller's logger, unless WithMachineAdmissionRequired is also given.
func WithMachineAdmission(admitter MachineAdmitter) Option {
	return func(c *Cache) { c.machineAdmitter = admitter }
}

// WithMachineAdmissionRequired refuses a step the admitter cannot answer for (MGS3022,
// exit 69) instead of running it unarbitrated. It has no effect without
// WithMachineAdmission.
func WithMachineAdmissionRequired() Option {
	return func(c *Cache) { c.machineRequired = true }
}

// RunOption configures a single Cache.Run (or RunAll) invocation.
type RunOption func(*runCtx)

// OnHit fires after a cache hit replay.
func OnHit(fn func(*Result)) RunOption {
	return func(rc *runCtx) { rc.onHit = fn }
}

// AuditReplay judges the absolute paths a cache hit just restored. A hit never invokes
// the step's function, so a check that wraps the function sees nothing a replay writes;
// this is where that check gets the replay. A non-nil error fails the step after the
// replay is reported.
func AuditReplay(fn func(ctx context.Context, s Step, written []string) error) RunOption {
	return func(rc *runCtx) { rc.auditReplay = fn }
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
// Multiple OnResult options accumulate; all fire in registration order.
func OnResult(fn func(*Step, *Result, error)) RunOption {
	return func(rc *runCtx) { rc.onResults = append(rc.onResults, fn) }
}

// WithMaxFailures bounds how many steps may fail before RunAll stops admitting more,
// as a budget rather than a boolean: 1 is fail-fast, 3 tolerates three, and 0 (the
// default) is unlimited. A step that fails only because a dependency failed does not
// count (it is a consequence, not an independent finding), so a budget of 1 stops at
// the first REAL failure rather than at whichever cascade victim reports first.
func WithMaxFailures(n int) RunOption {
	return func(rc *runCtx) { rc.maxFailures = n }
}

// WithLimiter shares an external Limiter with RunAll instead of creating a private one,
// so in-process tasks and nested calls compete for the same concurrency budget.
func WithLimiter(l *Limiter) RunOption {
	return func(rc *runCtx) { rc.limiter = l }
}
