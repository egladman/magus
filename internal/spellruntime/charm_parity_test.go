// This file is the ONE deliberate exception to two conventions this repo
// otherwise holds to: a test file pairs with a source file of the same name, and
// tests live in the package they test.
//
// It can satisfy neither, because it is not a test OF a file - it is a parity gate
// BETWEEN files in three packages: the Buzz module in internal/spellruntime, the Go
// helpers in std/charm.go, and the vm.Value marshaller in host. The import chain
// runs internal/spellruntime <- std <- host, so the only in-package home is `host`, where
// no charm source file exists to pair with; and `package spellruntime` cannot import std
// or host without a cycle.
//
// Every alternative pays for the naming with worse production code: exporting std
// or internal/spellruntime internals for a test's benefit, adding a non-upstream
// ValueToAny to gopherbuzz's vm, or copying host's ~30-line marshaller in here.
// Leave it external, and leave the name saying what it guards.
package spellruntime_test

import (
	"context"
	"strings"
	"testing"

	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/spellruntime"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/require"
)

// TestCharmBuzzParityWithHost keeps the pure-Buzz magus/charm module
// (internal/spellruntime/charm.buzz) in lockstep with the Go charm host module
// (std/charm.go): every constructor magus/charm exports must produce a
// byte-identical RFC 6902 patch record. The Buzz module is hand-written (charm is
// logic, not a struct, so it can't be codegen'd), so this guard is what licenses
// the duplication — diverge the two and this fails.
func TestCharmBuzzParityWithHost(t *testing.T) {
	ctx := context.Background()

	// eval loads the magus/charm source and evaluates a bare constructor call
	// (exports are flat-imported, like magus/spell's Target), returning the
	// marshalled Go value.
	eval := func(t *testing.T, expr string) any {
		t.Helper()
		s := buzz.NewSession(ctx, buzz.WithEmbedded())
		defer s.Close()
		// charm.buzz imports magus/spell for the Charm/PatchOp object types; register
		// the same bundle the runtime does so the import resolves in this bare session.
		s.SetModuleDecls(spellruntime.SpellModulePath, strings.Join([]string{
			spellruntime.TargetModuleSource, spellruntime.PatchOpSource, spellruntime.CharmTypeSource, spellruntime.CommandSource,
		}, "\n"))
		require.NoError(t, s.Exec(ctx, spellruntime.CharmModuleSource), "load charm.buzz")
		require.NoError(t, s.Exec(ctx, "final __r = "+expr+";"), "eval %s", expr)
		return bindinggen.ValueToAny(s.GetGlobal("__r"))
	}
	// spells.Charm now, not map[string]any: the host constructors return the typed value
	// the Buzz side already mirrored. norm marshals both sides through spells.Charm
	// anyway, so the comparison is unchanged.
	ok := func(v spells.Charm, err error) any {
		t.Helper()
		require.NoError(t, err)
		return v
	}
	// norm collapses both shapes - the host's spells.Charm and the Buzz Charm object's
	// field map - through spells.Charm, so the comparison ignores whether an empty
	// value/fromPtr key is present (the object carries all fields; the host omits
	// empties) and pins only the RFC 6902 content.
	norm := func(v any) spells.Charm {
		t.Helper()
		b, err := json.Marshal(v)
		require.NoError(t, err)
		var c spells.Charm
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
