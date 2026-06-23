package std

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFsReadWriteLines(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")

	require.NoError(t, FsWriteLines(ctx, p, []string{"a", "b", "c"}))

	// Written with a trailing newline; read back without a spurious empty line.
	got, err := FsReadLines(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got)

	// Round-trips a newline-terminated file unchanged.
	require.NoError(t, FsWriteLines(ctx, p, got))
	again, err := FsReadLines(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, got, again)

	// Empty list writes an empty file, which reads back as an empty list (not [""]).
	require.NoError(t, FsWriteLines(ctx, p, nil))
	empty, err := FsReadLines(ctx, p)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestFsGlob covers the doublestar matcher and its project-relative reporting:
// with a context cwd, FsGlob resolves the pattern against it and reports each
// match relative to that base, so the returned paths read like the pattern.
func TestFsGlob(t *testing.T) {
	dir := t.TempDir()
	// Lay out a small tree: two top-level .txt files, one nested .txt, one .md.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.md"), []byte("c"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "d.txt"), []byte("d"), 0o644))

	// With a context cwd, matches come back relative to it (slash-separated by the
	// pattern), independent of the process working directory.
	ctx := WithCwd(context.Background(), dir)

	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{"single level", "*.txt", []string{"a.txt", "b.txt"}},
		{"distinct extension", "*.md", []string{"c.md"}},
		{"recursive doublestar", "**/*.txt", []string{"a.txt", "b.txt", filepath.Join("sub", "d.txt")}},
		{"no match", "*.go", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FsGlob(ctx, tc.pattern)
			require.NoError(t, err)
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			assert.Equal(t, want, got)
		})
	}
}

// TestFsRemoveAll verifies recursive removal and the documented no-error-on-missing
// contract.
func TestFsRemoveAll(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "sub", "f.txt"), []byte("x"), 0o644))

	require.NoError(t, FsRemoveAll(ctx, tree))
	_, err := os.Stat(tree)
	assert.True(t, os.IsNotExist(err), "tree should be gone")

	// Removing a path that does not exist is not an error.
	assert.NoError(t, FsRemoveAll(ctx, filepath.Join(dir, "never-existed")))
}

// TestFsListDir covers entry listing and the documented "empty (nil) if the path
// does not exist" behaviour.
func TestFsListDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))

	names, err := FsListDir(ctx, dir)
	require.NoError(t, err)
	sort.Strings(names)
	assert.Equal(t, []string{"a.txt", "sub"}, names)

	// A missing directory lists as empty without error.
	missing, err := FsListDir(ctx, filepath.Join(dir, "nope"))
	require.NoError(t, err)
	assert.Empty(t, missing)
}

// TestFsAppendFile verifies append creates the file when absent and appends
// (rather than truncating) when it already exists.
func TestFsAppendFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "log.txt")

	// Append to a non-existent path creates it.
	require.NoError(t, FsAppendFile(ctx, p, "one\n"))
	// A second append adds to the existing content.
	require.NoError(t, FsAppendFile(ctx, p, "two\n"))

	got, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(got))
}

// TestFsChmod verifies the permission bits are changed. POSIX-only: Windows does
// not honour Unix mode bits, so the assertion is skipped there.
func TestFsChmod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permission bits are not meaningful on Windows")
	}
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))

	require.NoError(t, FsChmod(ctx, p, 0o600))
	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestFsSymlinkReadlink round-trips FsSymlink and FsReadlink: the link stores the
// target verbatim, and FsReadlink returns it. Skips cleanly where the platform or
// privileges prevent symlink creation (e.g. Windows without the privilege).
func TestFsSymlinkReadlink(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.WriteFile(target, []byte("payload"), 0o644))

	if err := FsSymlink(ctx, target, link); err != nil {
		t.Skipf("symlink unsupported on this platform/privilege level: %v", err)
	}

	// FsReadlink returns the stored target unchanged.
	got, err := FsReadlink(ctx, link)
	require.NoError(t, err)
	assert.Equal(t, target, got)

	// The link resolves to the target's contents.
	data, err := os.ReadFile(link)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(data))

	// FsReadlink on a regular (non-symlink) file errors.
	_, err = FsReadlink(ctx, target)
	assert.Error(t, err, "readlink of a non-symlink should error")
}
