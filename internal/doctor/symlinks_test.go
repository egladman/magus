package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckSymlinks(t *testing.T) {
	t.Run("no symlinks", func(t *testing.T) {
		root := canonicalTempDir(t)
		mustMkdir(t, filepath.Join(root, "api"))
		if got := checkSymlinks(root); got.Status != StatusOK {
			t.Fatalf("status = %v (%s), want ok", got.Status, got.Message)
		}
	})

	t.Run("in-tree symlink is ok", func(t *testing.T) {
		root := canonicalTempDir(t)
		mustMkdir(t, filepath.Join(root, "api"))
		mustSymlink(t, "api", filepath.Join(root, "alias"))
		got := checkSymlinks(root)
		if got.Status != StatusOK {
			t.Fatalf("status = %v (%s), want ok", got.Status, got.Message)
		}
	})

	t.Run("escaping symlink fails", func(t *testing.T) {
		root := canonicalTempDir(t)
		outside := canonicalTempDir(t)
		mustSymlink(t, outside, filepath.Join(root, "escape"))
		got := checkSymlinks(root)
		if got.Status != StatusFail {
			t.Fatalf("status = %v (%s), want fail", got.Status, got.Message)
		}
	})

	t.Run("dangling symlink to outside fails", func(t *testing.T) {
		root := canonicalTempDir(t)
		mustSymlink(t, "../../nonexistent", filepath.Join(root, "escape"))
		got := checkSymlinks(root)
		if got.Status != StatusFail {
			t.Fatalf("status = %v (%s), want fail", got.Status, got.Message)
		}
	})

	t.Run("symlinks inside ignore dirs are skipped", func(t *testing.T) {
		root := canonicalTempDir(t)
		outside := canonicalTempDir(t)
		gitDir := filepath.Join(root, ".git")
		mustMkdir(t, gitDir)
		mustSymlink(t, outside, filepath.Join(gitDir, "escape"))
		got := checkSymlinks(root)
		if got.Status != StatusOK {
			t.Fatalf("status = %v (%s), want ok (ignore dir not scanned)", got.Status, got.Message)
		}
	})
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval-symlinks temp dir: %v", err)
	}
	return dir
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}
