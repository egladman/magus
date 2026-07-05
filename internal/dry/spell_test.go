package dry

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
// are the acceptance inputs: the playground must evaluate the exact bytes the docs
// ship.
func tourFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "website", "tour", name))
	require.NoError(t, err, "read tour fixture %s", name)
	return string(b)
}

// TestLoadSpell_services loads the canonical service fixture (09-services.buzz) as a
// SPELL buffer and asserts the whole spell path: it loads clean, is recognized as a
// spell, `serve` is discovered as an op, and a `run serve` dry-run classifies it as a
// SERVICE op (not a command). The flip side of the magusfile path - targets come from
// mgs_listTargets, not the export set.
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

	r := Run(ctx, src, "serve", nil)
	require.True(t, r.OK, "dry-run of serve failed: %+v", r.Diag)
	require.NotEmpty(t, r.Trace, "serve dry-run should trace the service op")
	assert.Equal(t, "service", r.Trace[0].Kind, "serve returns a Service, so it is a service op")
	assert.Equal(t, "serve", r.Trace[0].Name)
	assert.Contains(t, r.Trace[0].Detail, "docker", "the op detail carries the declared command")
	// A clean service op raises no ward.
	for _, op := range r.Trace {
		assert.NotEqual(t, "ward", op.Kind, "09-services has no ward")
	}
}

// TestLoadSpell_wardMGS5002 loads the ward fixture (10-wards.buzz, a service whose
// `docker run -d` detaches) and asserts a `run serve` dry-run surfaces the MGS5002
// kind-coherence diagnostic. The op still loads and lists (a ward is not a load
// failure), and the diagnostic is visible with its code and message.
func TestLoadSpell_wardMGS5002(t *testing.T) {
	ctx := context.Background()
	src := tourFile(t, "10-wards.buzz")

	// The warded op still loads clean at the graph level - the ward is an op-level
	// diagnostic surfaced at dry-run, not a parse/load failure.
	g := LoadMagusfile(ctx, src)
	require.True(t, g.OK, "10-wards must still load: %+v", g.Diag)
	require.Nil(t, g.Diag)
	assert.True(t, g.Spell)

	r := Run(ctx, src, "serve", nil)
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
// must evaluate to a graph without a load diagnostic, whether magusfile (targets
// from exports) or spell (ops from mgs_listTargets). A ward is an op-level
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

// TestLoadSpell_customCharms loads the custom-charm fixture (15-charms-custom.buzz, a
// spell whose `lint` op declares `fix` and `verbose` charms as JSON patches over its
// argv) and asserts a `run lint:charm` dry-run reshapes the command exactly as the
// engine would - the point of routing the sandbox through spell.ApplyCharms rather
// than a second charm reader. Covers bare, one charm, and both (order-independent).
func TestLoadSpell_customCharms(t *testing.T) {
	ctx := context.Background()
	src := tourFile(t, "15-charms-custom.buzz")

	cases := []struct {
		name   string
		charms []string
		want   string
	}{
		{"bare", nil, "golangci-lint run ./..."},
		{"fix", []string{"fix"}, "golangci-lint run --fix ./..."},
		{"verbose", []string{"verbose"}, "golangci-lint run ./... -v"},
		{"both", []string{"fix", "verbose"}, "golangci-lint run --fix ./... -v"},
		{"both-reversed", []string{"verbose", "fix"}, "golangci-lint run --fix ./... -v"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Run(ctx, src, "lint", c.charms)
			require.True(t, r.OK, "dry-run of lint failed: %+v", r.Diag)
			require.NotEmpty(t, r.Trace, "lint dry-run should trace the command op")
			assert.Equal(t, "command", r.Trace[0].Kind, "lint returns a Command")
			assert.Equal(t, c.want, r.Trace[0].Detail, "charm-applied argv")
		})
	}
}

// TestRunSpell_badCharmPatchSurfaces locks in that a charm patch which passes the
// structural decode check but fails to apply (an out-of-range JSON pointer) surfaces
// as a diagnostic, not swallowed. The engine returns that error and refuses the run;
// the sandbox must do the same rather than render the un-reshaped command as if the
// charm applied. The bare run (no charm) still plans cleanly.
func TestRunSpell_badCharmPatchSurfaces(t *testing.T) {
	ctx := context.Background()
	src := `import "magus/target";
export fun mgs_getName() > str { return "linter"; }
fun lint(t: Target) > Command {
    return Command{ bin = "x", args = ["run"], charms = {
        "bad": Charm{ ops = [PatchOp{ op = "replace", path = "/9", value = "z" }] },
    }};
}
export fun mgs_listTargets() > any { return {"lint": lint}; }
`
	bare := Run(ctx, src, "lint", nil)
	require.True(t, bare.OK, "bare lint should plan cleanly: %+v", bare.Diag)
	require.NotEmpty(t, bare.Trace)
	assert.Equal(t, "x run", bare.Trace[0].Detail, "no charm active, base command")

	bad := Run(ctx, src, "lint", []string{"bad"})
	assert.False(t, bad.OK, "an out-of-range charm patch must fail the dry run, not silently no-op")
	require.NotNil(t, bad.Diag, "the patch-apply error must surface as a diagnostic")
	assert.Contains(t, bad.Diag.Msg, "lint", "the diagnostic names the op")
	assert.NotContains(t, bad.Diag.Msg, `spell ""`, "the by-value decoder must not leak an empty spell/op prefix")
}
