package std

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covWalkCallback records what FsWalk visits and answers with stop to end the walk
// early. err, when set, is what the predicate raises instead of answering.
type covWalkCallback struct {
	seen []string
	dirs []string
	stop bool
	err  error
}

func (c *covWalkCallback) Call(_ context.Context, args ...any) ([]any, error) {
	if c.err != nil {
		return nil, c.err
	}
	path, _ := args[0].(string)
	isDir, _ := args[1].(bool)
	c.seen = append(c.seen, path)
	if isDir {
		c.dirs = append(c.dirs, path)
	}
	return []any{c.stop}, nil
}

func TestFsDirnameBasenameJoin(t *testing.T) {
	ctx := context.Background()

	dir, err := FsDirname(ctx, filepath.FromSlash("a/b/c.txt"))
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("a/b"), dir)

	base, err := FsBasename(ctx, filepath.FromSlash("a/b/c.txt"))
	require.NoError(t, err)
	assert.Equal(t, "c.txt", base)

	// Join cleans as it goes, which is what makes it safe to build a path from
	// pieces a magusfile computed.
	joined, err := FsJoin(ctx, "a", "b", "..", "c")
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("a/c"), joined)

	empty, err := FsJoin(ctx)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestFsReadFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("payload"), 0o644))

	got, err := FsReadFile(context.Background(), filepath.Join(dir, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, "payload", got)

	// A relative path resolves against the context cwd, not the process cwd.
	got, err = FsReadFile(WithCwd(context.Background(), dir), "f.txt")
	require.NoError(t, err)
	assert.Equal(t, "payload", got)

	_, err = FsReadFile(context.Background(), filepath.Join(dir, "absent.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fs.read_file")
}

// TestFsWalk covers the walk's reporting contract: the callback sees
// project-relative paths measured from the context cwd, matching the root it
// passed in, and directories are flagged as such.
func TestFsWalk(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o644))

	ctx := WithCwd(context.Background(), dir)

	cb := &covWalkCallback{}
	require.NoError(t, FsWalk(ctx, ".", cb))

	sort.Strings(cb.seen)
	assert.Equal(t, []string{".", "a.txt", "sub", filepath.Join("sub", "b.txt")}, cb.seen)
	sort.Strings(cb.dirs)
	assert.Equal(t, []string{".", "sub"}, cb.dirs)
}

// TestFsWalkStopsEarly: a truthy callback ends the whole walk, not just the
// current directory.
func TestFsWalkStopsEarly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o644))

	cb := &covWalkCallback{stop: true}
	require.NoError(t, FsWalk(WithCwd(context.Background(), dir), ".", cb))
	assert.Len(t, cb.seen, 1, "the first truthy answer ends the walk")
}

func TestFsWalkPropagatesACallbackFailure(t *testing.T) {
	boom := errors.New("callback failed")
	err := FsWalk(context.Background(), t.TempDir(), &covWalkCallback{err: boom})
	assert.ErrorIs(t, err, boom)
}

func TestFsWalkOnAMissingRoot(t *testing.T) {
	err := FsWalk(context.Background(), filepath.Join(t.TempDir(), "absent"), &covWalkCallback{})
	assert.Error(t, err, "walking a root that does not exist raises rather than visiting nothing")
}
