package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeBzz(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOsExecShExitCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.bzz", `
import "magus";
import "magus/extra";

export fun check(_args: [str]) > void {
    var rc = extra.os.execSh("true", "", {"allow_failure": true}).code;
    if (rc != 0) {
        throw "os.execSh('true').code = {rc}";
    }
    var rc2 = extra.os.execSh("false", "", {"allow_failure": true}).code;
    if (rc2 == 0) {
        throw "os.execSh('false').code = 0, expected non-zero";
    }
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("os.execSh: %v", err)
	}
}

func TestOsWithEnvShellCapture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.bzz", `
import "magus";
import "magus/extra";

export fun check(_args: [str]) > void {
    extra.os.withEnv({"MY_BUZZ_VAR": "hello"}, fun() > void {
        var captured = extra.os.execSh("echo $MY_BUZZ_VAR").stdout;
        if (captured != "hello") {
            throw "expected 'hello', got: [" + captured + "]";
        }
    });
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("os.withEnv: %v", err)
	}
}

func TestFsJoinBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.bzz", `
import "magus";
import "magus/extra";

export fun check(_args: [str]) > void {
    var p = extra.fs.join("a", "b", "c");
    if (p == "") {
        throw "fs.join returned empty string";
    }
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("fs.join: %v", err)
	}
}

func TestFsListDirBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path := writeBzz(t, dir, "magusfile.bzz", `
import "magus";
import "magus/extra";

export fun check(_args: [str]) > void {
    var entries = extra.fs.listDir("subdir");
    if (entries.len() == 0) {
        throw "expected at least one entry in subdir";
    }
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("fs.listDir: %v", err)
	}
}

func TestFsRemoveAllBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "todelete")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	path := writeBzz(t, dir, "magusfile.bzz", `
import "magus";
import "magus/extra";

export fun check(_args: [str]) > void {
    extra.fs.removeAll("todelete");
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("fs.removeAll: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("expected todelete to be removed")
	}
}

func TestVcsBindings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.bzz", `
import "magus";
import "magus/extra";

export fun check(_args: [str]) > void {
    // All may be empty or false outside a repo; just confirm they don't crash.
    var h     = extra.vcs.shortHash();
    var b     = extra.vcs.branch();
    var d     = extra.vcs.commitDate();
    var dirty = extra.vcs.isDirty();
    var msg = h + b + d + "{dirty}";
    if (msg.len() < 0) { throw "unreachable"; }
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("vcs bindings: %v", err)
	}
}
