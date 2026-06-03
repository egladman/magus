package std

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFsExt(t *testing.T) {
	got, err := FsExt(context.Background(), "a/b/c.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if want := ".gz"; got != want {
		t.Fatalf("ext = %q, want %q", got, want)
	}
}

func TestFsIsDirIsFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if ok, _ := FsIsDir(ctx, dir); !ok {
		t.Error("is_dir(dir) = false, want true")
	}
	if ok, _ := FsIsDir(ctx, file); ok {
		t.Error("is_dir(file) = true, want false")
	}
	if ok, _ := FsIsFile(ctx, file); !ok {
		t.Error("is_file(file) = false, want true")
	}
	if ok, _ := FsIsFile(ctx, dir); ok {
		t.Error("is_file(dir) = true, want false")
	}
	// A missing path is reported as neither, without error.
	if ok, err := FsIsDir(ctx, filepath.Join(dir, "nope")); ok || err != nil {
		t.Errorf("is_dir(missing) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestFsStat(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}

	st, err := FsStat(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if got := st["size"].(int64); got != 5 {
		t.Errorf("size = %d, want 5", got)
	}
	if got := st["is_dir"].(bool); got {
		t.Error("is_dir = true, want false")
	}
	if got := st["mode"].(int64); got != 0o640 {
		t.Errorf("mode = %o, want 640", got)
	}
	if _, ok := st["mtime"].(float64); !ok {
		t.Errorf("mtime is %T, want float64", st["mtime"])
	}

	if _, err := FsStat(ctx, filepath.Join(dir, "missing")); err == nil {
		t.Error("stat of a missing path should error")
	}
}

func TestFsCopyFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := FsCopyFile(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("copied content = %q, want %q", got, "payload")
	}
}

func TestFsCopyDir(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := FsCopyDir(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"a.txt":     "a",
		"sub/b.txt": "b",
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

func TestJSONStringify(t *testing.T) {
	ctx := context.Background()
	val := map[string]any{"a": 1.0}

	// No indent → compact (single line).
	compact, err := JSONStringify(ctx, val, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compact, "\n") {
		t.Fatalf("no-indent output should be compact, got:\n%s", compact)
	}

	// A non-empty indent → pretty, multi-line with that indent.
	tabbed, err := JSONStringify(ctx, val, "\t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tabbed, "\n") || !strings.Contains(tabbed, "\t") {
		t.Fatalf("indented output should be multi-line and tab-indented, got:\n%s", tabbed)
	}
}

func TestEnvExpand(t *testing.T) {
	t.Setenv("MAGUS_EXTRA_TEST", "world")
	got, err := EnvExpand(context.Background(), "hello $MAGUS_EXTRA_TEST ${MAGUS_EXTRA_TEST}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "hello world world"; got != want {
		t.Fatalf("expand = %q, want %q", got, want)
	}
}

func TestEnvUnset(t *testing.T) {
	t.Setenv("MAGUS_EXTRA_UNSET", "x")
	if err := EnvUnset(context.Background(), "MAGUS_EXTRA_UNSET"); err != nil {
		t.Fatal(err)
	}
	if _, ok := os.LookupEnv("MAGUS_EXTRA_UNSET"); ok {
		t.Fatal("env.unset did not remove the variable")
	}
}

func TestEnvHome(t *testing.T) {
	got, err := EnvHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("env.home returned empty")
	}
}

func TestOsNumCPU(t *testing.T) {
	got, err := OsNumCPU(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got < 1 {
		t.Fatalf("num_cpu = %d, want >= 1", got)
	}
}

func TestOsHostname(t *testing.T) {
	got, err := OsHostname(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("os.hostname returned empty")
	}
}
