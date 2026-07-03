package spell_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// TestCharmBuzzParityWithHost keeps the pure-Buzz magus/charm module
// (internal/spell/buzzlib/charm.buzz) in lockstep with the Go charm host module
// (std/charm.go): every constructor magus/charm exports must produce a
// byte-identical RFC 6902 patch record. The Buzz module is hand-written (charm is
// logic, not a struct, so it can't be codegen'd), so this guard is what licenses
// the duplication — diverge the two and this fails.
func TestCharmBuzzParityWithHost(t *testing.T) {
	ctx := context.Background()

	// eval loads the magus/charm source and evaluates a bare constructor call
	// (exports are flat-imported, like magus/target's Target), returning the
	// marshalled Go value.
	eval := func(t *testing.T, expr string) any {
		t.Helper()
		s := buzz.NewSession(ctx, buzz.WithEmbedded())
		defer s.Close()
		// charm.buzz imports magus/target for the Charm/PatchOp object types; register
		// the same bundle the runtime does so the import resolves in this bare session.
		s.SetSourceModule(spell.TargetModulePath, strings.Join([]string{
			spell.TargetModuleSource, spell.PatchOpSource, spell.CharmTypeSource, spell.CommandSource,
		}, "\n"))
		require.NoError(t, s.Exec(ctx, spell.CharmModuleSource), "load charm.buzz")
		require.NoError(t, s.Exec(ctx, "final __r = "+expr+";"), "eval %s", expr)
		return host.ValueToAny(s.GetGlobal("__r"))
	}
	ok := func(v map[string]any, err error) any {
		t.Helper()
		require.NoError(t, err)
		return v
	}
	// norm collapses both shapes — the host's map[string]any and the Buzz Charm
	// object's field map — through types.Charm, so the comparison ignores whether an
	// empty value/from key is present (the object carries all fields; the host omits
	// empties) and pins only the RFC 6902 content.
	norm := func(v any) types.Charm {
		t.Helper()
		b, err := json.Marshal(v)
		require.NoError(t, err)
		var c types.Charm
		require.NoError(t, json.Unmarshal(b, &c))
		return c
	}

	argv := []string{"tool", "golangci-lint", "run", "./..."}

	cases := []struct {
		name string
		expr string
		want any
	}{
		{"append", `append(["-v","-x"])`, ok(std.CharmAppend(ctx, []string{"-v", "-x"}))},
		{"prepend", `prepend(["a","b"])`, ok(std.CharmPrepend(ctx, []string{"a", "b"}))},
		{"after", `after(["tool","golangci-lint","run","./..."], "run", ["--fix"])`, ok(std.CharmAfter(ctx, argv, "run", []string{"--fix"}))},
		{"before", `before(["tool","golangci-lint","run","./..."], "run", ["--fix"])`, ok(std.CharmBefore(ctx, argv, "run", []string{"--fix"}))},
		{"set", `set(["-l","."], "-l", "-w")`, ok(std.CharmSet(ctx, []string{"-l", "."}, "-l", "-w"))},
		{"drop", `drop(["mod","tidy","--diff"], "--diff")`, ok(std.CharmDrop(ctx, []string{"mod", "tidy", "--diff"}, "--diff"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, norm(c.want), norm(eval(t, c.expr)))
		})
	}
}
