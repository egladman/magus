package interactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/file/watch"
)

func mkfile(t *testing.T, root, rel string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ignorePatternFn(t *testing.T, root string, typ watch.PatternType, pattern string) func(string) bool {
	t.Helper()
	p := watch.IgnorePattern{Type: typ, Pattern: pattern}
	if err := watch.ValidatePattern(p); err != nil {
		t.Fatalf("invalid pattern: %v", err)
	}
	return watch.IgnorePatterns(root, []watch.IgnorePattern{p})
}

func TestSearchFilesBasic(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a/b/foo.go")
	mkfile(t, root, "a/b/bar.go")
	mkfile(t, root, "c/foo.txt")

	got, err := SearchFiles(context.Background(), root, []string{"foo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d: %v", len(got), got)
	}
	for _, f := range got {
		if f.Path != "a/b/foo.go" && f.Path != "c/foo.txt" {
			t.Errorf("unexpected path: %s", f.Path)
		}
	}
	if got[0].Score <= got[1].Score && got[0].Path > got[1].Path {
		t.Errorf("sort order wrong: %v", got)
	}
}

func TestSearchFilesIgnoreDirs(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "node_modules/x/foo.js")
	mkfile(t, root, ".git/HEAD")
	mkfile(t, root, "src/real.go")

	got, err := SearchFiles(context.Background(), root, []string{"foo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no results (ignore dirs should be pruned), got: %v", got)
	}

	got2, err := SearchFiles(context.Background(), root, []string{"real"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 || got2[0].Path != "src/real.go" {
		t.Errorf("want src/real.go, got: %v", got2)
	}
}

func TestSearchFilesIgnoreDirsRootNotPruned(t *testing.T) {
	// Workspace root whose base name is in IgnoreDirs must not be skipped.
	parent := t.TempDir()
	root := filepath.Join(parent, "vendor")
	mkfile(t, root, "pkg/foo.go")

	got, err := SearchFiles(context.Background(), root, []string{"foo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "pkg/foo.go" {
		t.Errorf("want pkg/foo.go, got: %v", got)
	}
}

func TestSearchFilesAndFilters(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "api/users.go")
	mkfile(t, root, "web/users.go")

	got, err := SearchFiles(context.Background(), root, []string{"api", "users"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "api/users.go" {
		t.Errorf("want only api/users.go, got: %v", got)
	}
}

func TestSearchFilesEmptyFilters(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a/b.go")

	got, err := SearchFiles(context.Background(), root, []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("want nil on empty filters and nil matchFn, got: %v", got)
	}

	got2, err := SearchFiles(context.Background(), root, []string{"  ", ""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != nil {
		t.Errorf("want nil on whitespace-only filters, got: %v", got2)
	}
}

func TestSearchFilesEmptyResult(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a/b.go")

	// Asked for something, found nothing — returns empty slice, not nil.
	got, err := SearchFiles(context.Background(), root, []string{"zzznomatch"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("want empty slice (not nil) when asked but found nothing")
	}
	if len(got) != 0 {
		t.Errorf("want 0 results, got: %v", got)
	}
}

func TestSearchFilesSymlinkSkipped(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "external.go")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "external.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	got, err := SearchFiles(context.Background(), root, []string{"external"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected symlinked file to be skipped, got: %v", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 .go files under api, got %d: %v", len(got), got)
	}
	for _, f := range got {
		if f.Path != "api/users.go" && f.Path != "api/users_test.go" {
			t.Errorf("unexpected path: %s", f.Path)
		}
	}

	// Without filter token: all .go files in workspace.
	got2, err := SearchFiles(context.Background(), root, []string{}, matchFn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 3 {
		t.Fatalf("want 3 .go files total, got %d: %v", len(got2), got2)
	}
}

func TestSearchFilesPatternRegex(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "api/users.go")
	mkfile(t, root, "api/users_test.go")
	mkfile(t, root, "web/handler_test.go")

	matchFn := ignorePatternFn(t, root, watch.PatternRegex, `_test\.go$`)
	got, err := SearchFiles(context.Background(), root, []string{}, matchFn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 test files, got %d: %v", len(got), got)
	}
	for _, f := range got {
		if !strings.HasSuffix(f.Path, "_test.go") {
			t.Errorf("unexpected path: %s", f.Path)
		}
	}
}

func TestSearchFilesPatternLiteral(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "api/Dockerfile")
	mkfile(t, root, "web/Dockerfile")
	mkfile(t, root, "api/main.go")

	matchFn := ignorePatternFn(t, root, watch.PatternLiteral, "Dockerfile")
	got, err := SearchFiles(context.Background(), root, []string{}, matchFn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 Dockerfiles, got %d: %v", len(got), got)
	}
	for _, f := range got {
		if !strings.HasSuffix(f.Path, "Dockerfile") {
			t.Errorf("unexpected path: %s", f.Path)
		}
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
	if err == nil {
		t.Error("want error on cancelled context, got nil")
	}
}
