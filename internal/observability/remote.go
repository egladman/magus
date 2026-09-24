package observability

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
)

// InstrumentRemoteBackend wraps b so every get/put records a span and the
// magus.cache.remote.* metrics through p. It returns b unchanged when telemetry
// is off, so a disabled build pays nothing — no wrapping, no byte counting. The
// optional [cache.RemotePruner] capability is preserved: a backend that supports
// prune still does after wrapping (the prune sweep is traced too).
//
// These metrics meter the TRANSPORT: what the backend answered. The run's remote tally
// meters OUTCOMES, so the two differ by design: a fetched artifact that fails
// verification or replay is a hit here and a failure in the tally, and a store the
// backend reports as already present is a put here and neither an upload nor a failure
// in the tally.
func InstrumentRemoteBackend(b cache.RemoteBackend, p Provider) cache.RemoteBackend {
	if b == nil || p == nil || !p.Enabled() {
		return b
	}
	base := &instrumentedBackend{RemoteBackend: b, p: p}
	if pr, ok := b.(cache.RemotePruner); ok {
		return &instrumentedPruner{instrumentedBackend: base, pruner: pr}
	}
	return base
}

// instrumentedBackend forwards Active and HasArtifact unchanged (cheap probes not worth
// metering) and instruments GetArtifact/PutArtifact.
type instrumentedBackend struct {
	cache.RemoteBackend
	p Provider
}

// GetArtifact traces the fetch and records the outcome. On a hit the span and
// metrics close when the returned reader is closed, so the recorded byte count
// is the artifact size the cache actually imported.
func (b *instrumentedBackend) GetArtifact(ctx context.Context, namespace, key string) (io.ReadCloser, error) {
	ctx, end := b.p.StartSpan(ctx, "magus.cache.remote.get", Attr{Key: "magus.project", Value: namespace})
	start := time.Now()
	rc, err := b.RemoteBackend.GetArtifact(ctx, namespace, key)
	if errors.Is(err, cache.ErrRemoteMiss) {
		b.p.RecordRemoteOp(ctx, RemoteOp{Method: "get", Outcome: "miss", Duration: time.Since(start).Seconds()})
		end(nil)
		return nil, err
	}
	if err != nil {
		b.p.RecordRemoteOp(ctx, RemoteOp{Method: "get", Outcome: "error", Duration: time.Since(start).Seconds()})
		end(err)
		return nil, err
	}
	counted := &countingReadCloser{CountingReader: cache.CountingReader{Reader: rc}, closer: rc}
	counted.onClose = func(n int64) {
		b.p.RecordRemoteOp(ctx, RemoteOp{Method: "get", Outcome: "hit", Duration: time.Since(start).Seconds(), Bytes: n})
		end(nil)
	}
	return counted, nil
}

// PutArtifact traces the upload and records the bytes streamed to the backend.
func (b *instrumentedBackend) PutArtifact(ctx context.Context, namespace, key string, r io.Reader) error {
	ctx, end := b.p.StartSpan(ctx, "magus.cache.remote.put", Attr{Key: "magus.project", Value: namespace})
	cr := &cache.CountingReader{Reader: r}
	start := time.Now()
	err := b.RemoteBackend.PutArtifact(ctx, namespace, key, cr)
	outcome := "stored"
	switch {
	case errors.Is(err, cache.ErrRemoteExists):
		outcome = "exists"
	case err != nil:
		outcome = "error"
	}
	b.p.RecordRemoteOp(ctx, RemoteOp{Method: "put", Outcome: outcome, Duration: time.Since(start).Seconds(), Bytes: cr.N})
	if outcome == "exists" {
		end(nil)
	} else {
		end(err)
	}
	return err
}

// instrumentedPruner adds the prune capability back onto an instrumented backend
// whose underlying store supports it, tracing the sweep. Prune is an out-of-band
// maintenance op, so it gets a span but no hit/miss counters.
type instrumentedPruner struct {
	*instrumentedBackend
	pruner cache.RemotePruner
}

func (b *instrumentedPruner) PruneArtifacts(ctx context.Context, policy cache.RetentionPolicy) error {
	ctx, end := b.p.StartSpan(ctx, "magus.cache.remote.prune")
	err := b.pruner.PruneArtifacts(ctx, policy)
	end(err)
	return err
}

// countingReadCloser counts a get stream and fires onClose once, when the cache has
// finished importing the artifact.
type countingReadCloser struct {
	cache.CountingReader
	closer  io.Closer
	once    sync.Once
	onClose func(n int64)
}

func (c *countingReadCloser) Close() error {
	err := c.closer.Close()
	c.once.Do(func() { c.onClose(c.N) })
	return err
}

// CacheTracer adapts a Provider to [cache.Tracer] so the cache package can open
// phase spans without importing this package. It returns nil when telemetry is
// off; [cache.ContextWithTracer] stores a nil Tracer as a no-op.
func CacheTracer(p Provider) cache.Tracer {
	if p == nil || !p.Enabled() {
		return nil
	}
	return cacheTracer{p: p}
}

type cacheTracer struct{ p Provider }

func (t cacheTracer) StartSpan(ctx context.Context, name string) (context.Context, func(error)) {
	return t.p.StartSpan(ctx, name)
}
