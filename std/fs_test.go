package std

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/egladman/magus/types"
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
			var values []string // nil, not empty: a no-match case expects nil
			for _, p := range got {
				values = append(values, p.Value)
				// Every match names the directory its value is measured from. That is
				// the fact the old []string dropped, leaving each caller to supply it
				// from memory.
				assert.Equal(t, dir, p.Base, "a match is based at the context cwd")
			}
			sort.Strings(values)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			assert.Equal(t, want, values)
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
// does not exist" behavior.
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
// not honor Unix mode bits, so the assertion is skipped there.
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

// TestRecordModeSkipsFilesystemWrites is the safety gate for deep dry-run: under a
// recording context, effectful filesystem ops must record-and-skip (return nil
// without touching disk), while a normal context still performs the write.
func TestRecordModeSkipsFilesystemWrites(t *testing.T) {
	dir := t.TempDir()
	rec := types.WithTrace(context.Background())
	plain := context.Background()

	t.Run("write_file", func(t *testing.T) {
		p := filepath.Join(dir, "w.txt")
		require.NoError(t, FsWriteFile(rec, p, "data"))
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "record mode must not write the file")

		require.NoError(t, FsWriteFile(plain, p, "data"))
		_, err = os.Stat(p)
		assert.NoError(t, err, "normal mode must write the file")
	})

	t.Run("mkdir_all", func(t *testing.T) {
		p := filepath.Join(dir, "sub", "deep")
		require.NoError(t, FsMkdirAll(rec, p, 0o755))
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "record mode must not create the directory")
	})

	t.Run("remove_all", func(t *testing.T) {
		p := filepath.Join(dir, "keep.txt")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		require.NoError(t, FsRemoveAll(rec, p))
		_, err := os.Stat(p)
		assert.NoError(t, err, "record mode must not delete the file")
	})

	t.Run("temp_dir_returns_stub_without_creating", func(t *testing.T) {
		got, err := FsTempDir(rec, "pre-")
		require.NoError(t, err)
		assert.NotEmpty(t, got, "record mode temp dir must return a non-empty path")
		_, statErr := os.Stat(got)
		assert.True(t, os.IsNotExist(statErr), "record mode must not create the temp dir")
	})
}

func TestFsRemove(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	f := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	require.NoError(t, FsRemove(ctx, f))
	_, err := os.Stat(f)
	assert.True(t, os.IsNotExist(err))

	// Missing is not an error, matching remove_all.
	require.NoError(t, FsRemove(ctx, filepath.Join(dir, "gone.txt")))

	// An empty directory goes; a populated one does not. That refusal is the
	// whole reason remove exists beside remove_all.
	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.Mkdir(empty, 0o755))
	require.NoError(t, FsRemove(ctx, empty))

	full := filepath.Join(dir, "full")
	require.NoError(t, os.Mkdir(full, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(full, "child"), []byte("x"), 0o644))
	require.Error(t, FsRemove(ctx, full), "a non-empty directory must not be removed")
	_, err = os.Stat(filepath.Join(full, "child"))
	require.NoError(t, err, "the child must survive the refused remove")
}

func TestFsRename(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	src := filepath.Join(dir, "src.txt")
	require.NoError(t, os.WriteFile(src, []byte("payload"), 0o644))

	// The destination's parent does not exist yet: rename creates it rather than
	// making every caller pair this with mkdirall.
	dst := filepath.Join(dir, "nested", "dst.txt")
	require.NoError(t, FsRename(ctx, src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
	_, err = os.Stat(src)
	assert.True(t, os.IsNotExist(err), "the source must be gone")

	require.Error(t, FsRename(ctx, filepath.Join(dir, "missing.txt"), dst))
}

func TestFsSize(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	p := filepath.Join(dir, "f.bin")
	require.NoError(t, os.WriteFile(p, []byte("12345"), 0o644))

	n, err := FsSize(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	empty := filepath.Join(dir, "empty.bin")
	require.NoError(t, os.WriteFile(empty, nil, 0o644))
	n, err = FsSize(ctx, empty)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	_, err = FsSize(ctx, filepath.Join(dir, "gone"))
	require.Error(t, err, "a missing path raises rather than reporting 0")
}

func TestFsTempFile(t *testing.T) {
	ctx := context.Background()

	p, err := FsTempFile(ctx, "magus-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(p) })

	// It exists and is empty: the caller writes it, unlike temp_dir which hands
	// back a directory to fill.
	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, int64(0), info.Size())
	assert.Contains(t, filepath.Base(p), "magus-test-")

	// Two calls never collide.
	q, err := FsTempFile(ctx, "magus-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(q) })
	assert.NotEqual(t, p, q)
}

func TestFsWriteFileAtomic(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")

	require.NoError(t, FsWriteFileAtomic(ctx, p, "first"))
	got, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "first", string(got))

	// Overwrites in place.
	require.NoError(t, FsWriteFileAtomic(ctx, p, "second"))
	got, err = os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "second", string(got))

	// Permissions match write_file's 0644, not CreateTemp's 0600.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(p)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	}

	// No temporary file is left behind to glob into a later target's sources.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"out.txt"}, names)

	// A missing parent directory is created rather than raising.
	nested := filepath.Join(dir, "a", "b", "deep.txt")
	require.NoError(t, FsWriteFileAtomic(ctx, nested, "deep"))
	got, err = os.ReadFile(nested)
	require.NoError(t, err)
	assert.Equal(t, "deep", string(got))
}

func TestRecordModeSkipsNewFilesystemWrites(t *testing.T) {
	dir := t.TempDir()
	rec := types.WithTrace(context.Background())

	t.Run("write_file_atomic", func(t *testing.T) {
		p := filepath.Join(dir, "atomic.txt")
		require.NoError(t, FsWriteFileAtomic(rec, p, "data"))
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "record mode must not write the file")
	})

	t.Run("rename", func(t *testing.T) {
		src := filepath.Join(dir, "src.txt")
		require.NoError(t, os.WriteFile(src, []byte("x"), 0o644))
		require.NoError(t, FsRename(rec, src, filepath.Join(dir, "moved.txt")))
		_, err := os.Stat(src)
		assert.NoError(t, err, "record mode must leave the source in place")
	})

	t.Run("remove", func(t *testing.T) {
		p := filepath.Join(dir, "keep.txt")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		require.NoError(t, FsRemove(rec, p))
		_, err := os.Stat(p)
		assert.NoError(t, err, "record mode must not delete the file")
	})
}

// The bootstrap trap this guards: `magus run go-build .` is driven by the magus on
// PATH, so an installed binary older than the checkout regenerates with its own
// renderer. Measured 2026-09-01: a PATH magus at schema v9 rewrote a committed v10
// MAGUS.md down to v9 during a go-build.
func TestFsWriteFileRefusesASchemaDowngrade(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "MAGUS.md")

	newer := fmt.Sprintf("This workspace has a knowledge graph (schema v%d).\n", types.KnowledgeSchemaVersion+1)
	require.NoError(t, os.WriteFile(p, []byte(newer), 0o644))

	err := FsWriteFile(ctx, p, "rendered by an older build")
	require.Error(t, err, "a write over newer-stamped output must be refused")
	assert.Contains(t, err.Error(), "would downgrade committed output")

	got, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, newer, string(got), "the refused write must leave the file untouched")

	assert.Error(t, FsWriteFileAtomic(ctx, p, "rendered by an older build"),
		"the atomic spelling shares the precondition")
}

func TestFsWriteFileAllowsEqualOrOlderStamps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	cases := map[string]string{
		"equal":     fmt.Sprintf("knowledge graph (schema v%d)\n", types.KnowledgeSchemaVersion),
		"older":     fmt.Sprintf("knowledge graph (schema v%d)\n", types.KnowledgeSchemaVersion-1),
		"export":    fmt.Sprintf("{\"schema_version\": %d}\n", types.KnowledgeSchemaVersion),
		"unstamped": "ordinary prose with no version in it\n",
	}
	for name, existing := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name+".txt")
			require.NoError(t, os.WriteFile(p, []byte(existing), 0o644))
			require.NoError(t, FsWriteFile(ctx, p, "fresh"))
			got, err := os.ReadFile(p)
			require.NoError(t, err)
			assert.Equal(t, "fresh", string(got))
		})
	}

	absent := filepath.Join(dir, "new.txt")
	require.NoError(t, FsWriteFile(ctx, absent, "fresh"), "a file that does not exist yet carries no stamp")
}
