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

func (f *fakeBackend) Active(context.Context) bool { return true }

func (f *fakeBackend) GetArtifact(context.Context, string, string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.data == nil {
		return nil, nil
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func (f *fakeBackend) PutArtifact(_ context.Context, _, _ string, r io.Reader) error {
	if f.putErr != nil {
		return f.putErr
	}
	b, err := io.ReadAll(r)
	f.put = b
	return err
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
	require.NoError(t, err)
	assert.Nil(t, rc, "expected nil reader on miss")
	require.Len(t, rec.remoteOps, 1)
	assert.Equal(t, "miss", rec.remoteOps[0].Outcome)
	assert.Equal(t, int64(0), rec.remoteOps[0].Bytes)
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

// TestInstrumentRemoteBackend_DisabledPassthrough verifies that a disabled
// provider yields the original backend unwrapped — no byte counting overhead on
// the default (telemetry-off) path.
func TestInstrumentRemoteBackend_DisabledPassthrough(t *testing.T) {
	t.Parallel()
	disabled, err := New(context.Background(), Config{Enabled: false})
	require.NoError(t, err)
	fb := &fakeBackend{}
	assert.Equal(t, cache.RemoteBackend(fb), InstrumentRemoteBackend(fb, disabled), "disabled provider should return the backend unwrapped")
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
