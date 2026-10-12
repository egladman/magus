package cache

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// spanNames mocks a Tracer that opens any span and returns the names it was asked for, in
// order. Which spans open is a Contains assertion, so the mock expects no fixed set.
func spanNames(t *testing.T) (*MockTracer, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var names []string
	tr := NewMockTracer(t)
	tr.EXPECT().StartSpan(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, name string) (context.Context, func(error)) {
			mu.Lock()
			names = append(names, name)
			mu.Unlock()
			return ctx, func(error) {}
		}).Maybe()
	return tr, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(names)
	}
}

// TestRun_PhaseSpans verifies cache.Run opens the expected phase spans through a
// context-installed Tracer: hash+snapshot on a miss, hash+replay on a hit.
func TestRun_PhaseSpans(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	c, err := Open(t.Context(), cdir, WithLocalWrite(true))
	require.NoError(t, err)

	srcDir := filepath.Join(root, "p")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644))
	outPath := filepath.Join(srcDir, "out.txt")
	step := Step{
		ProjectPath:   "p",
		Sources:       types.MustParseGlobs("p/*.go"),
		Outputs:       types.MustParseGlobs("p/out.txt"),
		WorkspaceRoot: root,
	}

	// Miss: hash, then snapshot (no replay).
	miss, missNames := spanNames(t)
	_, err = c.Run(ContextWithTracer(context.Background(), miss), step, func(context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	})
	require.NoError(t, err)
	assert.Contains(t, missNames(), "magus.cache.hash", "miss spans missing magus.cache.hash")
	assert.Contains(t, missNames(), "magus.cache.snapshot", "miss spans missing magus.cache.snapshot")
	assert.NotContains(t, missNames(), "magus.cache.replay", "miss spans should not include replay")

	// Hit: hash, then replay (no snapshot).
	hit, hitNames := spanNames(t)
	r2, err := c.Run(ContextWithTracer(context.Background(), hit), step, func(context.Context) error {
		t.Error("fn should not run on a hit")
		return nil
	})
	require.NoError(t, err)
	require.Truef(t, r2.Hit, "second Run should hit; got %+v", r2)
	assert.Contains(t, hitNames(), "magus.cache.hash", "hit spans missing magus.cache.hash")
	assert.Contains(t, hitNames(), "magus.cache.replay", "hit spans missing magus.cache.replay")
	assert.NotContains(t, hitNames(), "magus.cache.snapshot", "hit spans should not include snapshot")
}
