package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindMagusBzz(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("import \"magus\";\nexport fun build(args: [str]) > void {}\n"), 0o644))

	src, err := Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src, "expected source, got nil")
	assert.Equal(t, dir, src.Dir)
	assert.Len(t, src.Files, 1)
}

func TestFindNothing(t *testing.T) {
	t.Parallel()
	src, err := Find(t.TempDir())
	assert.ErrorIs(t, err, ErrNoMagusfile)
	assert.Nil(t, src)
}

func TestParseTargetsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `
import "magus";
export fun build(args: [str]) > void {}
export fun test(args: [str]) > void {}
export fun go_vet(args: [str]) > void {}
`
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))

	src := &Source{Dir: dir, Files: []string{path}}
	targets, err := Parse(context.Background(), src)
	require.NoError(t, err)

	got := map[string]Target{}
	for _, tgt := range targets {
		got[tgt.Key] = tgt
	}

	assert.Contains(t, got, "build", "missing target 'build'")
	assert.Contains(t, got, "test", "missing target 'test'")
	assert.Contains(t, got, "go-vet", "missing target 'go-vet'")
}

func TestRunAndParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	content := `
import "magus";
export fun build(args: [str]) > void {}
export fun test(args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	src := &Source{Dir: dir, Files: []string{path}}

	require.NoError(t, Run(context.Background(), src, "build", nil, dir))

	targets, err := Parse(context.Background(), src)
	require.NoError(t, err)
	require.Len(t, targets, 2)
}

func writeBzz(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestOsExecShExitCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "os";

export fun check(args: [str]) > void {
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
	require.NoError(t, Run(context.Background(), src, "check", nil, ""))
}

func TestOsWithEnvShellCapture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "os";

export fun check(args: [str]) > void {
    os.withEnv({"MY_BUZZ_VAR": "hello"}, fun() > void {
        var captured = os.execSh("echo $MY_BUZZ_VAR").stdout;
        if (captured != "hello") {
            throw "expected 'hello', got: [" + captured + "]";
        }
    });
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	require.NoError(t, Run(context.Background(), src, "check", nil, ""))
}

func TestFsJoinBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "fs";

export fun check(args: [str]) > void {
    var p = fs.join("a", "b", "c");
    if (p == "") {
        throw "fs.join returned empty string";
    }
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	require.NoError(t, Run(context.Background(), src, "check", nil, ""))
}

func TestFsListDirBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "a.txt"), []byte("x"), 0o644))

	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "fs";

export fun check(args: [str]) > void {
    var entries = fs.listDir("subdir");
    if (entries.len() == 0) {
        throw "expected at least one entry in subdir";
    }
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	require.NoError(t, Run(context.Background(), src, "check", nil, dir))
}

func TestFsRemoveAllBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "todelete")
	require.NoError(t, os.MkdirAll(target, 0o755))

	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "fs";

export fun check(args: [str]) > void {
    fs.removeAll("todelete");
}
`)
	src := &Source{Dir: dir, Files: []string{path}}
	require.NoError(t, Run(context.Background(), src, "check", nil, dir))
	_, err := os.Stat(target)
	assert.True(t, os.IsNotExist(err), "expected todelete to be removed")
}

func TestVcsBindings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeBzz(t, dir, "magusfile.buzz", `
import "magus";
import "vcs";

export fun check(args: [str]) > void {
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
	require.NoError(t, Run(context.Background(), src, "check", nil, ""))
}
