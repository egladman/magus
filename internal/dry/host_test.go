package dry

import (
	"context"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/interp/bindings"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMagusSurfaceMatchesBindings is the drift guard between the two host
// implementations of the magus.* surface: the real Buzz bindings
// (internal/interp/bindings) and this package's tracing dry-run host (buildMagus). A
// magusfile referencing a member the playground omits would fail to evaluate, so the
// playground must implement every member the real bindings register. Adding or
// removing a binding without mirroring it here fails this test instead of silently
// breaking the playground.
func TestMagusSurfaceMatchesBindings(t *testing.T) {
	realTop := bindings.MagusModuleKeys()
	require.NotEmpty(t, realTop, "bindings.MagusModuleKeys returned no members")

	m := buildMagus(buzz.NewSession(context.Background(), buzz.WithEmbedded()), newTracer())
	have := keySet(m)
	for _, k := range realTop {
		assert.True(t, have[k], "playground magus.* is missing %q (registered by the real bindings); add a stub in buildMagus", k)
	}

	// And the inverse: the playground must not expose members the real host dropped
	// (e.g. the removed magus.target namespace), which would teach a dead API.
	for _, k := range m.MapKeys() {
		assert.True(t, slices.Contains(realTop, k), "playground magus.%s has no counterpart in the real bindings; remove it or it teaches a dead API", k)
	}
}

// TestCtxDeclarationsMatchAcrossHosts is TestMagusSurfaceMatchesBindings one surface
// down, over the ctx a target receives. Three places enumerate its members
// independently - buildTargetContext, the magus\Exec refusal list beside it, and this
// package's buildCtx - and each omission fails differently and quietly: an Exec that
// does not know a member answers "no such member" instead of naming where to declare
// it, and a dry host that does not bind one cannot trace a body that calls it. A
// declaration that reaches the cache key while one of the two copies has never heard
// of it is exactly the kind of half-wired that reads as done.
func TestCtxDeclarationsMatchAcrossHosts(t *testing.T) {
	// Not a fourth list: the members of the real ctx ARE the enumeration, and the two
	// copies are measured against it. Dropped are the internal marker and the three
	// v0.4 removal shims, which exist to raise a better error than either copy could.
	notDeclarations := []string{"__magus_context", "inputs", "outputs", "updates"}
	// Known absences, each a pre-existing gap this test documents rather than fixes -
	// listing them is what keeps the rest of the surface gated instead of the whole
	// check being deleted the first time it goes red.
	//
	// TODO: bind envInputs/withEnv/withCwd in buildCtx; a body calling one of them is
	// untraceable in the playground today.
	knownAbsentFromDry := []string{"envInputs", "withEnv", "withCwd"}
	knownAbsentFromExec := []string{}

	realCtx := bindings.TargetContextKeys()
	require.NotEmpty(t, realCtx, "bindings.TargetContextKeys returned no members")

	inDry := keySet(buildCtx(newTracer()))
	inExec := map[string]bool{}
	for _, k := range bindings.ExecRefusedKeys() {
		inExec[k] = true
	}
	// withEnv/withCwd are chainable, so an Exec carries them as members rather than as
	// refusals; they are declarations of nothing and belong in neither list.
	inExec["withEnv"], inExec["withCwd"] = true, true

	for _, decl := range realCtx {
		if slices.Contains(notDeclarations, decl) {
			continue
		}
		if !slices.Contains(knownAbsentFromDry, decl) {
			assert.True(t, inDry[decl], "ctx.%s is bound by the real host but not by this package's buildCtx; a body calling it does not trace", decl)
		}
		if !slices.Contains(knownAbsentFromExec, decl) {
			assert.True(t, inExec[decl], "ctx.%s is bound by the real host but a magus\\Exec does not know it; ctx.withEnv({}).%s(..) fails without naming where to declare it", decl, decl)
		}
	}

	// And the fourth enumeration, from the other end: every name the static reader will
	// collect off a magusfile has to be a member the runtime actually binds, or the
	// magusfile declares something no ctx can answer.
	for _, decl := range describe.CtxDeclNames() {
		assert.Contains(t, realCtx, decl, "describe collects ctx.%s statically but the real ctx does not bind it", decl)
	}

	// The positive pin, stated separately so a future exclusion cannot quietly swallow
	// it: observes reaches the cache key, so a host that has never heard of it is the
	// under-declaration the declaration exists to prevent.
	assert.Contains(t, realCtx, "observes", "the real ctx must bind observes")
	assert.True(t, inDry["observes"], "buildCtx must bind observes")
	assert.True(t, inExec["observes"], "a magus\\Exec must refuse observes")
}

func keySet(m vm.Value) map[string]bool {
	s := map[string]bool{}
	for _, k := range m.MapKeys() {
		s[k] = true
	}
	return s
}

// TestPlaygroundChecksHostCallTypes is the point of registering the magus
// declarations beside the stub module: a dry run must reject a snippet the real
// runtime would reject. Before them this host was untyped, so a probe could return
// the wrong type from a host call and the playground reported success - a Run button
// that validates less than the language does teaches worse than none.
func TestPlaygroundChecksHostCallTypes(t *testing.T) {
	run := func(body string) Result {
		src := "import \"magus\";\n" + body + "\nexport fun work(ctx: magus\\Context, args: [str]) > void {}\n"
		return Run(context.Background(), src, "work", nil)
	}

	bad := run(`fun probe() > int !> any { return magus\where("x"); }`)
	require.False(t, bad.OK, "a host call returning str must not satisfy a fun declared > int")
	require.NotNil(t, bad.Diag)
	assert.Contains(t, bad.Diag.Msg, "return type mismatch")

	good := run(`fun probe() > str !> any { return magus\where("x"); }`)
	assert.True(t, good.OK, "the correctly typed call must still compile: %+v", good.Diag)
}
