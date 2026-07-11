package observability

import (
	"context"
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

// instrumentedBackend forwards Active unchanged (a cheap, cached probe not worth
// metering) and instruments GetArtifact/PutArtifact.
type instrumentedBackend struct {
	cache.RemoteBackend
	p Provider
}

// GetArtifact traces the fetch and records the outcome. On a hit the span and
// metrics close when the returned reader is closed, so the recorded byte count
// is the artifact size the cache actually imported.
func (b *instrumentedBackend) GetArtifact(ctx context.Context, projectPath, hash string) (io.ReadCloser, error) {
	ctx, end := b.p.StartSpan(ctx, "magus.cache.remote.get", Attr{Key: "magus.project", Value: projectPath})
	start := time.Now()
	rc, err := b.RemoteBackend.GetArtifact(ctx, projectPath, hash)
	if err != nil {
		b.p.RecordRemoteOp(ctx, RemoteOp{Method: "get", Outcome: "error", Duration: time.Since(start).Seconds()})
		end(err)
		return nil, err
	}
	if rc == nil {
		b.p.RecordRemoteOp(ctx, RemoteOp{Method: "get", Outcome: "miss", Duration: time.Since(start).Seconds()})
		end(nil)
		return nil, nil //nolint:nilnil // documented miss: nil reader = not found (see GetArtifact)
	}
	return &countingReadCloser{ReadCloser: rc, onClose: func(n int64) {
		b.p.RecordRemoteOp(ctx, RemoteOp{Method: "get", Outcome: "hit", Duration: time.Since(start).Seconds(), Bytes: n})
		end(nil)
	}}, nil
}

// PutArtifact traces the upload and records the bytes streamed to the backend.
func (b *instrumentedBackend) PutArtifact(ctx context.Context, projectPath, hash string, r io.Reader) error {
	ctx, end := b.p.StartSpan(ctx, "magus.cache.remote.put", Attr{Key: "magus.project", Value: projectPath})
	cr := &countingReader{Reader: r}
	start := time.Now()
	err := b.RemoteBackend.PutArtifact(ctx, projectPath, hash, cr)
	outcome := "stored"
	if err != nil {
		outcome = "error"
	}
	b.p.RecordRemoteOp(ctx, RemoteOp{Method: "put", Outcome: outcome, Duration: time.Since(start).Seconds(), Bytes: cr.n})
	end(err)
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

// countingReader tallies bytes read from a put stream.
type countingReader struct {
	io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += int64(n)
	return n, err
}

// countingReadCloser tallies bytes read from a get stream and fires onClose once,
// when the cache has finished importing the artifact.
type countingReadCloser struct {
	io.ReadCloser
	n       int64
	once    sync.Once
	onClose func(n int64)
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(func() { c.onClose(c.n) })
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
