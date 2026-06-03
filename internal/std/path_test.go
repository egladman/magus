package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPathClean(t *testing.T) {
	got, err := PathClean(context.Background(), "a/b/../c/./d")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.FromSlash("a/c/d"); got != want {
		t.Fatalf("clean = %q, want %q", got, want)
	}
}

func TestPathRel(t *testing.T) {
	got, err := PathRel(context.Background(), "a/b", "a/b/c/d")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.FromSlash("c/d"); got != want {
		t.Fatalf("rel = %q, want %q", got, want)
	}
}

func TestPathIsAbs(t *testing.T) {
	abs, _ := PathAbs(context.Background(), "x")
	got, err := PathIsAbs(context.Background(), abs)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("is_abs(%q) = false, want true", abs)
	}
	if rel, _ := PathIsAbs(context.Background(), "x/y"); rel {
		t.Fatal("is_abs of a relative path should be false")
	}
}

func TestPathExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	ctx := context.Background()

	got, err := PathExpandUser(ctx, "~/proj")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "proj"); got != want {
		t.Fatalf("expand_user(~/proj) = %q, want %q", got, want)
	}

	bare, _ := PathExpandUser(ctx, "~")
	if bare != home {
		t.Fatalf("expand_user(~) = %q, want %q", bare, home)
	}

	// A non-~ path and another user's ~ are left untouched.
	for _, in := range []string{"/abs/path", "rel/path", "~other/x"} {
		if got, _ := PathExpandUser(ctx, in); got != in {
			t.Errorf("expand_user(%q) = %q, want unchanged", in, got)
		}
	}
}
