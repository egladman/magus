package gen

import (
	"context"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// camelCase mirrors the snake_case→camelCase transform the Buzz emitter applies
// to method keys (magus-utils bindings). A single-word name is unchanged.
func camelCase(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	out := parts[0]
	for _, p := range parts[1:] {
		if p != "" {
			out += strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return out
}

// TestModulesMatchStd guards the Modules registry against drift from the canonical
// std.Module surface: every host module std declares, except the magus namespace
// (not a bare import), must appear in Modules, and Modules must name nothing extra.
func TestModulesMatchStd(t *testing.T) {
	want := map[string]bool{}
	for _, m := range std.All() {
		if m.Name == "magus" {
			continue
		}
		want[m.Name] = true
	}
	for name := range Modules {
		assert.Containsf(t, want, name, "Modules registry has %q but std.All() does not", name)
		delete(want, name)
	}
	assert.Emptyf(t, want, "std.All() modules missing from the Modules registry: %v", setKeys(want))
}

// setKeys returns the keys of a set, for a readable failure message.
func setKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestBuzzBindingsMatchHostModules guards against the host/gen trampolines
// drifting from the canonical std.Module surface: every method a module
// declares must be exposed as a key on the generated module map. Buzz camelCases
// the host's snake_case names, so the lookup key is camelCase(meth.Name).
func TestBuzzBindingsMatchHostModules(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	checked := 0
	for _, m := range std.All() {
		var reg RegisterFunc
		if m.Name == "magus" {
			reg = RegisterMagus // the magus.* namespace has no Modules entry
		} else if mr, ok := Modules[m.Name]; ok {
			reg = mr.Register
		} else {
			continue
		}
		mod := reg(ctx, sess)
		require.Truef(t, mod.IsMap(), "Register%s did not return a map", m.Name)
		for _, meth := range m.Methods {
			// extra is self-complete: every declared method must be on the Buzz
			// surface, even ones Buzz's stdlib also covers (see std.BuzzStdlibEquiv).
			key := camelCase(meth.Name)
			if meth.BuzzName != "" {
				key = meth.BuzzName
			}
			_, ok := mod.MapGet(key)
			assert.Truef(t, ok, "buzz %s.%s is missing (host declares it as %q); host/gen has drifted from std.Module",
				m.Name, key, meth.Name)
			checked++
		}
	}
	require.NotZero(t, checked, "no host methods were checked; the host module registry or buzz registries map changed shape")
	t.Logf("verified %d host methods are present in the Buzz bindings", checked)
}
