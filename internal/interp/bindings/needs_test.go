package bindings

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNeedsNonTargetFunctionFails verifies a needs on a function that is not an
// exported target (a non-exported helper, or a typo that resolves to some other
// in-scope function) fails fast rather than silently no-op'ing. (A name that
// resolves to nothing at all is a Buzz undefined-variable error even earlier; this
// guards the reachable footgun of passing a real function value that names no target.)
func TestNeedsNonTargetFunctionFails(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
fun helper(args: [str]) > void {}
export fun top(ctx: magus\Context, args: [str]) > void {
    ctx.needs(helper);
}
`)
	err := runTargetIn(t, dir, "top")
	require.Error(t, err, "expected an error for a needs on a non-target function")
	assert.Contains(t, err.Error(), "does not name an exported target")
}

// TestNeedsForwardReference verifies a magus.needs on an exported target declared
// LATER in the file resolves: target bodies run post-load, so the forward reference
// to the later export is already bound by the time top runs.
func TestNeedsForwardReference(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun top(ctx: magus\Context, _a: [str]) > void !> any {
    ctx.needs(dep);
    fs\writeFile("ran", "top");
}
export fun dep(ctx: magus\Context, _a: [str]) > void !> any { fs\writeFile("dep-ran", "dep"); }
`)
	require.NoError(t, runTargetIn(t, dir, "top"))
	_, err := os.Stat(filepath.Join(dir, "dep-ran"))
	require.NoError(t, err, "forward-referenced dependency did not run")
}

// TestNeedsStringArgumentFails verifies magus.needs rejects a bare string (the
// classic footgun) and points the author at magus.glob for patterns.
func TestNeedsStringArgumentFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
export fun dep(ctx: magus\Context, _a: [str]) > void {}
export fun top(ctx: magus\Context, _a: [str]) > void { ctx.needs("dep"); }
`), 0o644))
	err := runTargetIn(t, dir, "top")
	require.Error(t, err, "expected an error for a string argument to magus.needs")
	assert.Contains(t, err.Error(), "must be a target function")
}

// TestNeedsAnonymousFunctionFails verifies magus.needs rejects an anonymous function
// literal: it names no target, so it cannot declare a dependency.
func TestNeedsAnonymousFunctionFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
export fun top(ctx: magus\Context, _a: [str]) > void { ctx.needs(fun (_a: [str]) > void {}); }
`), 0o644))
	err := runTargetIn(t, dir, "top")
	require.Error(t, err, "expected an error for an anonymous function argument to magus.needs")
	assert.Contains(t, err.Error(), "anonymous function is not a target")
}

// TestNeedsDedup verifies magus.needs runs a duplicated target once — the footgun
// where the same exported target is listed more than once in one needs call.
func TestNeedsDedup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(`
import "magus";
import "os";
import "proc";

export fun dep(ctx: magus\Context, _a: [str]) > void !> any { final c = proc\shell("printf x >> mark"); proc\exec(c.bin, c.args, "", {}); }
export fun top(ctx: magus\Context, _a: [str]) > void { ctx.needs(dep, dep); }
`), 0o644))
	require.NoError(t, runTargetIn(t, dir, "top"))
	got, err := os.ReadFile(filepath.Join(dir, "mark"))
	require.NoError(t, err, "dep did not run")
	assert.Equal(t, "x", string(got), "dep should run once")
}

// TestNeedsCycleThroughEntryTargetErrors covers the cycle that comes back around to
// the target the USER named. The entry target is invoked directly (runBuzz calls its
// callable), so it entered neither the memo nor the ancestor stack: a dependency that
// needed it back re-executed its body, and with two dependencies in flight the second
// one parked on the first's memo entry while the re-executed entry parked on the
// second — a hang, not a diagnostic. The timeout is the assertion; a cycle must
// always surface as an error.
func TestNeedsCycleThroughEntryTargetErrors(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
export fun ci(ctx: magus\Context, _a: [str]) > void { ctx.needs(lint, docs); }
export fun lint(ctx: magus\Context, _a: [str]) > void { ctx.needs(ci); }
export fun docs(ctx: magus\Context, _a: [str]) > void { ctx.needs(ci); }
`)
	done := make(chan error, 1)
	go func() { done <- runPooledTargetIn(t, dir, "ci") }()
	select {
	case err := <-done:
		require.Error(t, err, "a cycle back to the entry target must be an error")
		assert.Contains(t, err.Error(), "cycle detected")
	case <-time.After(20 * time.Second):
		t.Fatal("run hung on a cycle back to the entry target instead of erroring")
	}
}

// TestNeedsGlobHandle covers ctx.needs(ctx.glob(...)) feeding a meta-target: the matched
// targets run (sorted) before the body, and non-matching targets are skipped. With
// no pool in ctx the deps run sequentially in the current VM, so the order is
// deterministic.
func TestNeedsGlobHandle(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "os";
import "proc";
fun note(s: str) > void !> any {
   final c = proc\shell("printf '%s\n' " + s + " >> ran");
   proc\exec(c.bin, c.args, "", {});
}
export fun go_build(ctx: magus\Context, _a: [str]) > void !> any { note("go-build"); }
export fun image_build(ctx: magus\Context, _a: [str]) > void !> any { note("image-build"); }
export fun go_test(ctx: magus\Context, _a: [str]) > void !> any { note("go-test"); }
export fun build(ctx: magus\Context, _a: [str]) > void !> any {
   ctx.needs(ctx.glob("*-build"));
   note("build-body");
}
`)
	require.NoError(t, runTargetIn(t, dir, "build"))
	got, err := os.ReadFile(sentinel(dir))
	require.NoError(t, err, "sentinel not created")
	// Deps (sorted) run before the body; go-test does not match "*-build".
	want := "go-build\nimage-build\nbuild-body\n"
	assert.Equal(t, want, string(got))
	assert.NotContains(t, string(got), "go-test", "go-test ran but does not match *-build")
}

// TestNeedsGlobMatchingNothingRaises runs the shape that hid a stale symbol index: a
// target needing a glob that matches nothing must fail before its body continues, not
// pass having needed nothing.
func TestNeedsGlobMatchingNothingRaises(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun lint(ctx: magus\Context, _a: [str]) > void !> any {
   ctx.needs(ctx.glob("scip"));
   fs\writeFile("body-ran", "1");
}
`)
	err := runTargetIn(t, dir, "lint")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `ctx.glob: "scip" matches no exported target`)
	assert.NoFileExists(t, filepath.Join(dir, "body-ran"), "the body must not run past an empty dependency")
}
