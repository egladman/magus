package observability

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBackend is a minimal cache.RemoteBackend whose get/put behaviour the test
// controls directly, so assertions don't depend on a real transport.
type fakeBackend struct {
	data   []byte // artifact returned on get; nil = miss
	getErr error
	putErr error
	put    []byte // captured put payload
	pruned bool
}

func (f *fakeBackend) Name() string { return "fake" }

func (f *fakeBackend) Active(context.Context) bool { return true }

func (f *fakeBackend) GetArtifact(context.Context, string, string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.data == nil {
		return nil, cache.ErrRemoteMiss
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func (f *fakeBackend) PutArtifact(_ context.Context, _, _ string, r io.Reader) error {
	b, err := io.ReadAll(r)
	f.put = b
	if f.putErr != nil {
		return f.putErr
	}
	return err
}

func (f *fakeBackend) HasArtifact(context.Context, string, string) (bool, error) {
	return f.data != nil, nil
}

// fakePruner adds the optional RemotePruner capability.
type fakePruner struct{ *fakeBackend }

func (f *fakePruner) PruneArtifacts(context.Context, cache.RetentionPolicy) error {
	f.pruned = true
	return nil
}

func TestInstrumentRemoteBackend_GetHit(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	b := InstrumentRemoteBackend(&fakeBackend{data: []byte("hello")}, rec)

	rc, err := b.GetArtifact(context.Background(), "p", "h")
	require.NoError(t, err)
	// Metrics for a hit close with the reader, so the byte count is the artifact size.
	assert.Empty(t, rec.remoteOps, "op recorded before reader close")
	got, _ := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	assert.Equal(t, "hello", string(got))
	require.Len(t, rec.remoteOps, 1)
	op := rec.remoteOps[0]
	assert.Equal(t, "get", op.Method)
	assert.Equal(t, "hit", op.Outcome)
	assert.Equal(t, int64(5), op.Bytes)
	assert.Equal(t, []string{"magus.cache.remote.get"}, rec.spans)
}

func TestInstrumentRemoteBackend_GetMiss(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	b := InstrumentRemoteBackend(&fakeBackend{data: nil}, rec)

	rc, err := b.GetArtifact(context.Background(), "p", "h")
	require.ErrorIs(t, err, cache.ErrRemoteMiss, "a miss passes through as the sentinel")
	assert.Nil(t, rc)
	require.Len(t, rec.remoteOps, 1)
	assert.Equal(t, "miss", rec.remoteOps[0].Outcome)
	assert.Equal(t, int64(0), rec.remoteOps[0].Bytes)
}

// A put the store answers "already present" is its own outcome, not an error.
func TestInstrumentRemoteBackend_PutExists(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	b := InstrumentRemoteBackend(&fakeBackend{putErr: cache.ErrRemoteExists}, rec)

	err := b.PutArtifact(context.Background(), "p", "h", bytes.NewReader([]byte("world")))
	require.ErrorIs(t, err, cache.ErrRemoteExists)
	require.Len(t, rec.remoteOps, 1)
	assert.Equal(t, "exists", rec.remoteOps[0].Outcome)
}

// HasArtifact reaches the wrapped backend, so backfill works with telemetry on.
func TestInstrumentRemoteBackend_HasPassesThrough(t *testing.T) {
	t.Parallel()
	b := InstrumentRemoteBackend(&fakeBackend{data: []byte("x")}, &recorder{})
	has, err := b.HasArtifact(context.Background(), "p", "h")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestInstrumentRemoteBackend_GetError(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	b := InstrumentRemoteBackend(&fakeBackend{getErr: errors.New("boom")}, rec)

	_, err := b.GetArtifact(context.Background(), "p", "h")
	assert.Error(t, err)
	require.Len(t, rec.remoteOps, 1)
	assert.Equal(t, "error", rec.remoteOps[0].Outcome)
}

func TestInstrumentRemoteBackend_Put(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	fb := &fakeBackend{}
	b := InstrumentRemoteBackend(fb, rec)

	require.NoError(t, b.PutArtifact(context.Background(), "p", "h", bytes.NewReader([]byte("world"))))
	assert.Equal(t, "world", string(fb.put))
	require.Len(t, rec.remoteOps, 1)
	op := rec.remoteOps[0]
	assert.Equal(t, "put", op.Method)
	assert.Equal(t, "stored", op.Outcome)
	assert.Equal(t, int64(5), op.Bytes)
	assert.Equal(t, []string{"magus.cache.remote.put"}, rec.spans)
}

// disabledRec is a recorder that reports Enabled()==false, standing in for the
// concrete disabled provider (which now lives in the otlp subpackage and cannot
// be imported here without an import cycle).
type disabledRec struct{ recorder }

func (*disabledRec) Enabled() bool { return false }

// TestInstrumentRemoteBackend_DisabledPassthrough verifies that a disabled
// provider yields the original backend unwrapped — no byte counting overhead on
// the default (telemetry-off) path.
func TestInstrumentRemoteBackend_DisabledPassthrough(t *testing.T) {
	t.Parallel()
	fb := &fakeBackend{}
	assert.Equal(t, cache.RemoteBackend(fb), InstrumentRemoteBackend(fb, &disabledRec{}), "disabled provider should return the backend unwrapped")
}

// TestInstrumentRemoteBackend_PrunePreserved verifies the optional RemotePruner
// capability survives wrapping when present, and is absent otherwise.
func TestInstrumentRemoteBackend_PrunePreserved(t *testing.T) {
	t.Parallel()
	rec := &recorder{}

	plain := InstrumentRemoteBackend(&fakeBackend{}, rec)
	assert.NotImplements(t, (*cache.RemotePruner)(nil), plain, "plain backend should not gain a prune capability")

	fp := &fakePruner{fakeBackend: &fakeBackend{}}
	wrapped := InstrumentRemoteBackend(fp, rec)
	pr, ok := wrapped.(cache.RemotePruner)
	require.True(t, ok, "prune capability lost after wrapping")
	require.NoError(t, pr.PruneArtifacts(context.Background(), cache.RetentionPolicy{}))
	assert.True(t, fp.pruned, "underlying prune not invoked")
	assert.Equal(t, []string{"magus.cache.remote.prune"}, rec.spans)
}

func TestCacheTracer(t *testing.T) {
	t.Parallel()
	// A nil or disabled provider yields a nil Tracer; cache stores that as a no-op.
	assert.Nil(t, CacheTracer(nil))
	assert.Nil(t, CacheTracer(&graphRecorder{enabled: false}))

	// An enabled provider yields a Tracer that delegates StartSpan through to it.
	rec := &recorder{}
	tr := CacheTracer(rec)
	require.NotNil(t, tr)

	ctx, done := tr.StartSpan(context.Background(), "cache.hash")
	require.NotNil(t, ctx)
	require.NotNil(t, done)
	done(nil) // the finish func must be safe to call

	assert.Equal(t, []string{"cache.hash"}, rec.spans)
}
