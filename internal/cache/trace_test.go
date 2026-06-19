package cache

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recTracer records the names of spans the cache opens, in order.
type recTracer struct {
	mu    sync.Mutex
	names []string
}

func (r *recTracer) StartSpan(ctx context.Context, name string) (context.Context, func(error)) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	return ctx, func(error) {}
}

// TestRun_PhaseSpans verifies cache.Run opens the expected phase spans through a
// context-installed Tracer: hash+snapshot on a miss, hash+replay on a hit.
func TestRun_PhaseSpans(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	c, err := Open(cdir, WithMutable(true))
	require.NoError(t, err)

	srcDir := filepath.Join(root, "p")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644))
	outPath := filepath.Join(srcDir, "out.txt")
	step := Step{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
	}

	// Miss: hash, then snapshot (no replay).
	miss := &recTracer{}
	_, err = c.Run(ContextWithTracer(context.Background(), miss), step, func(context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	})
	require.NoError(t, err)
	assert.Contains(t, miss.names, "magus.cache.hash", "miss spans missing magus.cache.hash")
	assert.Contains(t, miss.names, "magus.cache.snapshot", "miss spans missing magus.cache.snapshot")
	assert.NotContains(t, miss.names, "magus.cache.replay", "miss spans should not include replay")

	// Hit: hash, then replay (no snapshot).
	hit := &recTracer{}
	r2, err := c.Run(ContextWithTracer(context.Background(), hit), step, func(context.Context) error {
		t.Error("fn should not run on a hit")
		return nil
	})
	require.NoError(t, err)
	require.Truef(t, r2.Hit, "second Run should hit; got %+v", r2)
	assert.Contains(t, hit.names, "magus.cache.hash", "hit spans missing magus.cache.hash")
	assert.Contains(t, hit.names, "magus.cache.replay", "hit spans missing magus.cache.replay")
	assert.NotContains(t, hit.names, "magus.cache.snapshot", "hit spans should not include snapshot")
}
