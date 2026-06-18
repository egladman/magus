package bindings_test

import (
	"context"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/hostbuzz"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/std"
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
		s := buzz.NewSession(ctx)
		defer s.Close()
		require.NoError(t, s.Exec(ctx, ispell.CharmModuleSource), "load charm.buzz")
		require.NoError(t, s.Exec(ctx, "final __r = "+expr+";"), "eval %s", expr)
		return hostbuzz.ValueToAny(s.GetGlobal("__r"))
	}
	ok := func(v map[string]any, err error) any {
		t.Helper()
		require.NoError(t, err)
		return v
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
			require.Equal(t, c.want, eval(t, c.expr))
		})
	}
}
