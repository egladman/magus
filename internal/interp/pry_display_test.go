package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColorEnabledForFile_Nil(t *testing.T) {
	assert.False(t, interp.ColorEnabledForFile(nil))
}

func TestPrintSourceContext_NonexistentFile(t *testing.T) {
	var sb strings.Builder
	interp.PrintSourceContext(&sb, "/no/such/file/xyz.go", 1, 2, false)
	assert.Contains(t, sb.String(), "cannot read source")
}

func TestPrintSourceContext_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	var sb strings.Builder
	interp.PrintSourceContext(&sb, path, 3, 1, false)
	out := sb.String()
	assert.Contains(t, out, "line2")
	assert.Contains(t, out, "line3")
	assert.Contains(t, out, "line4")
}

func writeBzzBP(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(content), 0o644))
}

// TestSpellModuleForkTarget verifies that a fork target (which has no function
// in the compiled spell table — only a spells.json data entry) is still
// callable programmatically. registerSpells overlays a Go-backed function for
// each fork target; here go-vet is a fork no-op that must resolve to
// a function and run without error.
func TestSpellModuleForkTarget(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";
import "magus/spell/go";

export fun check(_args: [str]) > void {
    if (go.name != "go") { throw "spell not found"; }
    if (go["go-vet"] == null) { throw "fork go-vet must be a function (overlay)"; }
}
`)
	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src)
	require.NoError(t, interp.Run(context.Background(), src, "check", nil, dir))
}

// TestSpellModuleRequireBuiltin verifies a built-in spell can be imported as a
// typed module: import "magus/spell/docker" binds the handle under its basename
// (docker), and the resolved value is the live spell handle (docker-build is callable).
func TestSpellModuleRequireBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";
import "magus/spell/docker";

export fun check(_args: [str]) > void {
    if (docker.name != "docker") { error("name mismatch: " + docker.name); }
    if (docker["docker-build"] == null) { throw "docker-build op must be callable as a method"; }
}
`)
	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src)
	require.NoError(t, interp.Run(context.Background(), src, "check", nil, dir))
}

// TestSpellModuleRequireUnknownFailsToCompile verifies a misspelled built-in
// module is a compile error — the point of typed import — not a silent runtime nil.
func TestSpellModuleRequireUnknownFailsToCompile(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";
import "magus/spell/dockr";
magus.project.register(".", fun(p: any, cb: fun(m: any) > void) > bool { cb({"spells": [dockr]}); return true; });
`)
	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src)
	err = interp.Run(context.Background(), src, "noop", nil, dir)
	assert.Error(t, err, "expected a compile error for the misspelled module")
}

// TestSpellModuleRequireLocal verifies a workspace-local Buzz spell is
// importable by path — import "spells/locreq" resolves ./spells/locreq.buzz,
// binds the handle under the basename (locreq), so its target dispatches.
func TestSpellModuleRequireLocal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "spells"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spells", "locreq.buzz"), []byte(`
export fun mgs_getName() > str { return "locreq"; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "true"}};
}
`), 0o644))
	writeBzzBP(t, dir, `
import "magus";
import "spells/locreq";

export fun check(args: [str]) > void {
    if (locreq.name != "locreq") { error("name mismatch: " + locreq.name); }
}
magus.project.register(".", fun(p: any, cb: fun(m: any) > void) > bool { cb({"spells": [locreq]}); return true; });
`)
	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src)
	require.NoError(t, interp.Run(context.Background(), src, "check", nil, dir))
}

// TestSpellMultipleFields verifies that the spell handle for the go spell has
// the expected fork-target methods beyond just name.
func TestSpellMultipleFields(t *testing.T) {
	dir := t.TempDir()
	writeBzzBP(t, dir, `
import "magus";
import "magus/spell/go";

export fun check(args: [str]) > void {
    if (go.name != "go") { error("name mismatch: " + go.name); }
    if (go["go-build"] == null) { throw "go-build must be a function"; }
    if (go["go-fmt"] == null) { throw "go-fmt must be a function"; }
}
`)
	src, err := interp.Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src)
	require.NoError(t, interp.Run(context.Background(), src, "check", nil, dir))
}
