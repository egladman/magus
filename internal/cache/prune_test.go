package cache

import (
	"context"
	"testing"
	"time"
)

func openMutableCache(t *testing.T) *Cache {
	t.Helper()
	c, err := Open(t.TempDir(), WithMutable(true))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	return c
}

func TestPrune_EmptyCacheIsNoop(t *testing.T) {
	c := openMutableCache(t)
	n, freed, err := c.Prune(context.Background(), time.Now(), false)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 0 || freed != 0 {
		t.Errorf("Prune on empty cache: n=%d freed=%d, want 0 0", n, freed)
	}
}

func TestPrune_DryRun_NothingDeleted(t *testing.T) {
	c := openMutableCache(t)

	// Populate one entry with a no-op function.
	spec := Spec{
		WorkspaceRoot: t.TempDir(),
		ProjectPath:   "api/",
		Target:        "build",
	}
	_, err := c.Run(context.Background(), spec, func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatalf("cache.Run: %v", err)
	}

	n, freed, err := c.Prune(context.Background(), time.Now().Add(time.Hour), true)
	if err != nil {
		t.Fatalf("dry-run Prune: %v", err)
	}
	if n == 0 {
		t.Error("dry-run Prune: expected to count at least one entry")
	}
	_ = freed // non-zero because at least manifest exists

	// Real prune after dry-run: should also remove entries.
	n2, _, err := c.Prune(context.Background(), time.Now().Add(time.Hour), false)
	if err != nil {
		t.Fatalf("real Prune: %v", err)
	}
	if n2 == 0 {
		t.Error("real Prune: expected to remove at least one entry")
	}
}
