package playground

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tourFile reads a website/tour fixture relative to the package dir. The fixtures
// are the acceptance inputs, so the playground must evaluate the exact bytes the
// docs ship.
func tourFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "website", "tour", name))
	require.NoError(t, err, "read tour fixture %s", name)
	return string(b)
}

// TestLoadSpell_services loads the canonical service fixture (09-services.buzz) as a
// SPELL buffer and asserts the whole spell path: it loads clean (no diagnostic), the
// buffer is recognized as a spell, `serve` is discovered as an op, and a `run serve`
// dry-run classifies it as a SERVICE op (not a command). This is the flip side of the
// magusfile path — targets come from mgs_listTargets, not the export set.
func TestLoadSpell_services(t *testing.T) {
	ctx := context.Background()
	src := tourFile(t, "09-services.buzz")

	g := LoadMagusfile(ctx, src)
	require.True(t, g.OK, "spell must load without a diagnostic: %+v", g.Diag)
	require.Nil(t, g.Diag, "no load diagnostic expected")
	assert.True(t, g.Spell, "09-services is a spell buffer (exports mgs_listTargets)")

	var found bool
	for _, tg := range g.Targets {
		if tg.Key == "serve" {
			found = true
		}
	}
	assert.True(t, found, "serve should be discovered as an op; got %+v", g.Targets)

	r := DryRun(ctx, src, "serve", nil)
	require.True(t, r.OK, "dry-run of serve failed: %+v", r.Diag)
	require.NotEmpty(t, r.Trace, "serve dry-run should record the service op")
	assert.Equal(t, "service", r.Trace[0].Kind, "serve returns a Service, so it is a service op")
	assert.Equal(t, "serve", r.Trace[0].Name)
	assert.Contains(t, r.Trace[0].Detail, "docker", "the op detail carries the declared command")
	// A clean service op raises no ward.
	for _, op := range r.Trace {
		assert.NotEqual(t, "ward", op.Kind, "09-services has no ward")
	}
}

// TestLoadSpell_wardMGS5002 loads the ward fixture (10-wards.buzz, a service whose
// `docker run -d` detaches) and asserts the MGS5002 kind-coherence diagnostic is
// surfaced by a `run serve` dry-run. The op still loads and lists (a ward is not a
// load failure), and the diagnostic is visible with its code and message.
func TestLoadSpell_wardMGS5002(t *testing.T) {
	ctx := context.Background()
	src := tourFile(t, "10-wards.buzz")

	// The warded op still loads clean at the graph level — the ward is an op-level
	// diagnostic surfaced at dry-run, not a parse/load failure.
	g := LoadMagusfile(ctx, src)
	require.True(t, g.OK, "10-wards must still load: %+v", g.Diag)
	require.Nil(t, g.Diag)
	assert.True(t, g.Spell)

	r := DryRun(ctx, src, "serve", nil)
	require.True(t, r.OK, "dry-run of serve failed: %+v", r.Diag)

	var wardOp *Op
	for i := range r.Trace {
		if r.Trace[i].Kind == "ward" {
			wardOp = &r.Trace[i]
		}
	}
	require.NotNil(t, wardOp, "a detached service op must surface a ward; trace=%+v", r.Trace)
	assert.Equal(t, "MGS5002", wardOp.Name, "the ward is the detached-service diagnostic")
	assert.Contains(t, wardOp.Detail, "MGS5002", "the ward detail carries the code")
	assert.Contains(t, wardOp.Detail, "detach", "the ward message explains the detach contradiction")
}

// TestTourFilesLoadClean is the regression guard: every website/tour/*.buzz file
// must evaluate to a graph without a load diagnostic, whether it is a magusfile
// (targets from exports) or a spell (ops from mgs_listTargets). A ward is an op-level
// diagnostic, not a load one, so even 10-wards loads clean here.
func TestTourFilesLoadClean(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join("..", "..", "website", "tour")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var seen int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".buzz") {
			continue
		}
		seen++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			g := LoadMagusfile(ctx, string(b))
			assert.True(t, g.OK, "%s failed to load: %+v", name, g.Diag)
			assert.Nil(t, g.Diag, "%s produced a load diagnostic", name)
		})
	}
	require.NotZero(t, seen, "expected at least one tour fixture")
}

// TestConsole_spellServiceAndWard drives the terminal end to end on the two spell
// fixtures: `ls` lists the spell's op, `run serve` on the clean fixture renders the
// service hint, and `run serve` on the ward fixture renders the MGS5002 line as an
// error. This proves the console distinguishes a spell op and a ward from a plain
// magusfile op.
func TestConsole_spellServiceAndWard(t *testing.T) {
	ctx := context.Background()

	svc := NewConsole(testInfo)
	ok, status := svc.SetSource(ctx, tourFile(t, "09-services.buzz"))
	require.True(t, ok, "09-services did not load: %s", status)
	assert.Contains(t, status, "op", "a spell buffer's status counts ops, not targets")
	assert.Contains(t, joinHTML(svc.Exec(ctx, "ls").Lines), "serve", "ls lists the spell op")

	runOut := joinHTML(svc.Exec(ctx, "run serve").Lines)
	assert.Contains(t, runOut, "service", "run serve shows the service op with its kind hint")
	assert.Contains(t, runOut, "supervised", "the service hint reads supervised, shared")

	ward := NewConsole(testInfo)
	ok, _ = ward.SetSource(ctx, tourFile(t, "10-wards.buzz"))
	require.True(t, ok, "10-wards did not load")
	wardOut := joinHTML(ward.Exec(ctx, "run serve").Lines)
	assert.Contains(t, wardOut, "MGS5002", "run serve on the ward fixture surfaces the code")
}
