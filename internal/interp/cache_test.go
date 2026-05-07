package interp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheDirRespectsXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	dir, err := cacheDir()
	if err != nil {
		t.Fatalf("cacheDir: %v", err)
	}
	want := filepath.Join(tmp, "magus", "teal")
	if dir != want {
		t.Errorf("cacheDir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("cacheDir not created: %v", err)
	}
}
