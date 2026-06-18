package interactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkfile(t *testing.T, root, rel string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, nil, 0o644))
}

func ignorePatternFn(t *testing.T, root string, typ watch.PatternType, pattern string) func(string) bool {
	t.Helper()
	p := watch.IgnorePattern{Type: typ, Pattern: pattern}
	require.NoError(t, watch.ValidatePattern(p), "invalid pattern")
	return watch.IgnorePatterns(root, []watch.IgnorePattern{p})
}

func TestSearchFilesBasic(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a/b/foo.go")
	mkfile(t, root, "a/b/bar.go")
	mkfile(t, root, "c/foo.txt")

	got, err := SearchFiles(context.Background(), root, []string{"foo"}, nil)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, f := range got {
		assert.Contains(t, []string{"a/b/foo.go", "c/foo.txt"}, f.Path)
	}
	if got[0].Score <= got[1].Score {
		assert.LessOrEqual(t, got[0].Path, got[1].Path, "sort order wrong: %v", got)
	}
}

func TestSearchFilesIgnoreDirs(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "node_modules/x/foo.js")
	mkfile(t, root, ".git/HEAD")
	mkfile(t, root, "src/real.go")

	got, err := SearchFiles(context.Background(), root, []string{"foo"}, nil)
	require.NoError(t, err)
	assert.Empty(t, got, "ignore dirs should be pruned")

	got2, err := SearchFiles(context.Background(), root, []string{"real"}, nil)
	require.NoError(t, err)
	require.Len(t, got2, 1)
	assert.Equal(t, "src/real.go", got2[0].Path)
}

func TestSearchFilesIgnoreDirsRootNotPruned(t *testing.T) {
	// Workspace root whose base name is in IgnoreDirs must not be skipped.
	parent := t.TempDir()
	root := filepath.Join(parent, "vendor")
	mkfile(t, root, "pkg/foo.go")

	got, err := SearchFiles(context.Background(), root, []string{"foo"}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "pkg/foo.go", got[0].Path)
}

func TestSearchFilesAndFilters(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "api/users.go")
	mkfile(t, root, "web/users.go")

	got, err := SearchFiles(context.Background(), root, []string{"api", "users"}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "api/users.go", got[0].Path)
}

func TestSearchFilesEmptyFilters(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a/b.go")

	got, err := SearchFiles(context.Background(), root, []string{}, nil)
	require.NoError(t, err)
	assert.Nil(t, got, "want nil on empty filters and nil matchFn")

	got2, err := SearchFiles(context.Background(), root, []string{"  ", ""}, nil)
	require.NoError(t, err)
	assert.Nil(t, got2, "want nil on whitespace-only filters")
}

func TestSearchFilesEmptyResult(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a/b.go")

	// Asked for something, found nothing — returns empty slice, not nil.
	got, err := SearchFiles(context.Background(), root, []string{"zzznomatch"}, nil)
	require.NoError(t, err)
	assert.NotNil(t, got, "want empty slice (not nil) when asked but found nothing")
	assert.Empty(t, got)
}

func TestSearchFilesSymlinkSkipped(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "external.go")
	require.NoError(t, os.WriteFile(target, nil, 0o644))
	link := filepath.Join(root, "external.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	got, err := SearchFiles(context.Background(), root, []string{"external"}, nil)
	require.NoError(t, err)
	assert.Empty(t, got, "expected symlinked file to be skipped")
}

func TestSearchFilesPatternGlob(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "api/users.go")
	mkfile(t, root, "api/users_test.go")
	mkfile(t, root, "api/config.yaml")
	mkfile(t, root, "web/users.go")

	matchFn := ignorePatternFn(t, root, watch.PatternGlob, "**/*.go")

	// With filter token: only .go files under api.
	got, err := SearchFiles(context.Background(), root, []string{"api"}, matchFn)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, f := range got {
		assert.Contains(t, []string{"api/users.go", "api/users_test.go"}, f.Path)
	}

	// Without filter token: all .go files in workspace.
	got2, err := SearchFiles(context.Background(), root, []string{}, matchFn)
	require.NoError(t, err)
	assert.Len(t, got2, 3)
}

func TestSearchFilesPatternRegex(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "api/users.go")
	mkfile(t, root, "api/users_test.go")
	mkfile(t, root, "web/handler_test.go")

	matchFn := ignorePatternFn(t, root, watch.PatternRegex, `_test\.go$`)
	got, err := SearchFiles(context.Background(), root, []string{}, matchFn)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, f := range got {
		assert.True(t, strings.HasSuffix(f.Path, "_test.go"), "unexpected path: %s", f.Path)
	}
}

func TestSearchFilesPatternLiteral(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "api/Dockerfile")
	mkfile(t, root, "web/Dockerfile")
	mkfile(t, root, "api/main.go")

	matchFn := ignorePatternFn(t, root, watch.PatternLiteral, "Dockerfile")
	got, err := SearchFiles(context.Background(), root, []string{}, matchFn)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, f := range got {
		assert.True(t, strings.HasSuffix(f.Path, "Dockerfile"), "unexpected path: %s", f.Path)
	}
}

func TestSearchFilesCancelledContext(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		mkfile(t, root, fmt.Sprintf("pkg%d/main.go", i))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := SearchFiles(ctx, root, []string{"main"}, nil)
	assert.Error(t, err, "want error on cancelled context")
}
