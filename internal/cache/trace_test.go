package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/cache"
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
	c, err := cache.Open(cdir, cache.WithMutable(true))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}

	srcDir := filepath.Join(root, "p")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package p"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(srcDir, "out.txt")
	spec := cache.Spec{
		ProjectPath:   "p",
		Sources:       []string{"p/*.go"},
		Outputs:       []string{"p/out.txt"},
		WorkspaceRoot: root,
	}

	// Miss: hash, then snapshot (no replay).
	miss := &recTracer{}
	_, err = c.Run(cache.ContextWithTracer(context.Background(), miss), spec, func(context.Context) error {
		return os.WriteFile(outPath, []byte("ok"), 0o644)
	})
	if err != nil {
		t.Fatalf("Run(miss): %v", err)
	}
	if !slices.Contains(miss.names, "magus.cache.hash") {
		t.Errorf("miss spans %v missing magus.cache.hash", miss.names)
	}
	if !slices.Contains(miss.names, "magus.cache.snapshot") {
		t.Errorf("miss spans %v missing magus.cache.snapshot", miss.names)
	}
	if slices.Contains(miss.names, "magus.cache.replay") {
		t.Errorf("miss spans %v should not include replay", miss.names)
	}

	// Hit: hash, then replay (no snapshot).
	hit := &recTracer{}
	r2, err := c.Run(cache.ContextWithTracer(context.Background(), hit), spec, func(context.Context) error {
		t.Error("fn should not run on a hit")
		return nil
	})
	if err != nil {
		t.Fatalf("Run(hit): %v", err)
	}
	if !r2.Hit {
		t.Fatalf("second Run should hit; got %+v", r2)
	}
	if !slices.Contains(hit.names, "magus.cache.hash") {
		t.Errorf("hit spans %v missing magus.cache.hash", hit.names)
	}
	if !slices.Contains(hit.names, "magus.cache.replay") {
		t.Errorf("hit spans %v missing magus.cache.replay", hit.names)
	}
	if slices.Contains(hit.names, "magus.cache.snapshot") {
		t.Errorf("hit spans %v should not include snapshot", hit.names)
	}
}
