package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOsExecShExitCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function check(args: {string})
    local os = require("magus.extra.os")
    local rc = os.exec_sh("true", nil, {allow_failure = true}).code
    if rc ~= 0 then
        error("os.exec_sh('true').code = " .. tostring(rc))
    end
    local rc2 = os.exec_sh("false", nil, {allow_failure = true}).code
    if rc2 == 0 then
        error("os.exec_sh('false').code = 0, expected non-zero")
    end
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("os.exec_sh: %v", err)
	}
}

func TestOsWithEnvShellCapture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function check(args: {string})
    local os = require("magus.extra.os")
    local captured = ""
    os.with_env({MY_TEAL_VAR = "hello"}, function()
        captured = os.exec_sh("echo $MY_TEAL_VAR").stdout
    end)
    if captured ~= "hello" then
        error("expected 'hello', got: " .. captured)
    end
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("os.with_env: %v", err)
	}
}

func TestFsJoinBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function check(args: {string})
    local fs = require("magus.extra.fs")
    local p = fs.join("a", "b", "c")
    if p == "" then
        error("fs.join returned empty string")
    end
end
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

	path := writeTL(t, dir, "magusfile.tl", `
global function check(args: {string})
    local fs = require("magus.extra.fs")
    local entries = fs.list_dir("subdir")
    if #entries == 0 then
        error("expected at least one entry in subdir")
    end
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("fs.list_dir: %v", err)
	}
}

func TestFsRemoveAllBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "todelete")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	path := writeTL(t, dir, "magusfile.tl", `
global function check(args: {string})
    local fs = require("magus.extra.fs")
    fs.remove_all("todelete")
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, dir); err != nil {
		t.Fatalf("fs.remove_all: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("expected todelete to be removed")
	}
}

func TestVcsBindings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTL(t, dir, "magusfile.tl", `
global function check(args: {string})
    local vcs = require("magus.extra.vcs")
    -- All may be empty or false outside a repo; just confirm they don't crash.
    local h    = vcs.short_hash()
    local b    = vcs.branch()
    local d    = vcs.commit_date()
    local dirty = vcs.is_dirty()
    local msg = h .. b .. d .. tostring(dirty)
    if msg == nil then error("unreachable") end
end
`)
	src := &Source{Dir: dir, Files: []string{path}}
	if err := Run(context.Background(), src, "check", nil, ""); err != nil {
		t.Fatalf("vcs bindings: %v", err)
	}
}
