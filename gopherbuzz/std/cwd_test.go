package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/gopherbuzz/vm"
)

// TestResolveAgainstContextCwd checks the pure resolver: a relative path joins
// the context cwd, while an absolute path or an unset cwd is left untouched — so
// scripts run without an embedder-set cwd behave exactly as before (extend, not
// replace).
func TestResolveAgainstContextCwd(t *testing.T) {
	if got := resolve(context.Background(), "rel/path"); got != "rel/path" {
		t.Fatalf("resolve without cwd = %q, want unchanged", got)
	}
	ctx := WithCwd(context.Background(), "/work/dir")
	if got := resolve(ctx, "rel/path"); got != filepath.Join("/work/dir", "rel/path") {
		t.Fatalf("resolve with cwd = %q, want /work/dir/rel/path", got)
	}
	if got := resolve(ctx, "/abs/path"); got != "/abs/path" {
		t.Fatalf("resolve of absolute path = %q, want unchanged", got)
	}
}

// TestStdlibResolvesCwd verifies the fs builtins resolve relative paths against
// an embedder-set context cwd (WithCwd) — so a script run "from" a directory
// sees that directory without the process actually chdir-ing.
func TestStdlibResolvesCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := WithCwd(context.Background(), dir)

	// fs.makeDirectory("made") creates the dir under the context cwd, not the
	// process cwd.
	if _, err := fsMakeDirectory(ctx, []vm.Value{vm.StrValue("made")}); err != nil {
		t.Fatalf("fsMakeDirectory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "made")); err != nil {
		t.Fatalf("fs.makeDirectory should create dir under the context cwd: %v", err)
	}

	// fs.list(".") resolves to the cwd and lists its entries.
	listed, err := fsList(ctx, []vm.Value{vm.StrValue(".")})
	if err != nil {
		t.Fatalf("fsList: %v", err)
	}
	names := map[string]bool{}
	for _, it := range listed.ListItems() {
		names[it.AsString()] = true
	}
	if !names["hello.txt"] || !names["made"] {
		t.Fatalf("fs.list(.) = %v, want hello.txt and made", names)
	}

	// fs.currentDirectory returns the context cwd.
	cd, err := fsCurrentDirectory(ctx, nil)
	if err != nil {
		t.Fatalf("fsCurrentDirectory: %v", err)
	}
	if cd.AsString() != dir {
		t.Fatalf("fs.currentDirectory = %q, want %q", cd.AsString(), dir)
	}
}
