package interp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindMagusBzz(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("import \"magus\";\nexport fun build(_args: [str]) > void {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src, err := Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if src == nil {
		t.Fatal("expected source, got nil")
	}
	if src.Dir != dir {
		t.Errorf("Dir = %q, want %q", src.Dir, dir)
	}
	if len(src.Files) != 1 {
		t.Errorf("Files = %v, want 1 entry", src.Files)
	}
}

func TestFindNothing(t *testing.T) {
	t.Parallel()
	src, err := Find(t.TempDir())
	if !errors.Is(err, ErrNoMagusfile) {
		t.Errorf("Find error = %v, want ErrNoMagusfile", err)
	}
	if src != nil {
		t.Errorf("Find src = %+v, want nil", src)
	}
}

func TestParseTargetsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `
import "magus";
export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
export fun go_vet(_args: [str]) > void {}
`
	path := filepath.Join(dir, "magusfile.buzz")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	src := &Source{Dir: dir, Files: []string{path}}
	targets, err := Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Target{}
	for _, tgt := range targets {
		got[tgt.Key] = tgt
	}

	if _, ok := got["build"]; !ok {
		t.Error("missing target 'build'")
	}
	if _, ok := got["test"]; !ok {
		t.Error("missing target 'test'")
	}
	if _, ok := got["go-vet"]; !ok {
		t.Error("missing target 'go-vet'")
	}
}

func TestRunAndParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `
import "magus";
export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &Source{Dir: dir, Files: []string{path}}

	if err := Run(context.Background(), src, "build", nil, dir); err != nil {
		t.Fatalf("Run build: %v", err)
	}

	targets, err := Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("Parse: expected 2 targets, got %d", len(targets))
	}
}

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
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "os";

export fun check(_args: [str]) > void {
    var rc = os.execSh("true", "", {"allow_failure": true}).code;
    if (rc != 0) {
        throw "os.execSh('true').code = {rc}";
    }
    var rc2 = os.execSh("false", "", {"allow_failure": true}).code;
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
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "os";

export fun check(_args: [str]) > void {
    os.withEnv({"MY_BUZZ_VAR": "hello"}, fun() > void {
        var captured = os.execSh("echo $MY_BUZZ_VAR").stdout;
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
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "fs";

export fun check(_args: [str]) > void {
    var p = fs.join("a", "b", "c");
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

	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "fs";

export fun check(_args: [str]) > void {
    var entries = fs.listDir("subdir");
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

	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "fs";

export fun check(_args: [str]) > void {
    fs.removeAll("todelete");
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
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "vcs";

export fun check(_args: [str]) > void {
    // All may be empty or false outside a repo; just confirm they don't crash.
    var h     = vcs.shortHash();
    var b     = vcs.branch();
    var d     = vcs.commitDate();
    var dirty = vcs.isDirty();
    var msg = h + b + d + "{dirty}";
    if (msg.len() < 0) { throw "unreachable"; }
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("vcs bindings: %v", err)
	}
}
