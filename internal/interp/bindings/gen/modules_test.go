package gen

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hostmodules"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These guard the hand-maintained Modules registry (modules.go and modules_wasm.go)
// against drift from the canonical std.Module surface.

// camelCaseKey mirrors the snake_case->camelCase transform the Buzz emitter applies to
// method keys (magus-utils bindings). A single-word name is unchanged.
func camelCaseKey(s string) string {
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
// host module surface: every host module magus declares (hostmodules.All(): std's
// own registry plus std/encoding's), except the magus namespace (not a bare
// import), must appear in Modules, and Modules must name nothing extra.
func TestModulesMatchStd(t *testing.T) {
	want := map[string]bool{}
	paths := map[string]string{}
	for _, m := range hostmodules.All() {
		if m.Name == "magus" {
			continue
		}
		want[m.Name] = true
		paths[m.Name] = m.Path
	}
	for name, reg := range Modules {
		assert.Containsf(t, want, name, "Modules registry has %q but hostmodules.All() does not", name)
		// The import path has to agree too: a Path here that the host surface does
		// not declare would register the module at an import path nothing else in
		// magus knows, so `import` would resolve while describe, the docs, and the
		// knowledge graph all named something else.
		assert.Equalf(t, paths[name], reg.Path, "Modules[%q].Path disagrees with std.Module.Path", name)
		delete(want, name)
	}
	assert.Emptyf(t, want, "hostmodules.All() modules missing from the Modules registry: %v", modKeySet(want))
}

// modKeySet returns the keys of a set, for a readable failure message.
func modKeySet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestBuzzBindingsMatchHostModules guards against the generated trampolines
// drifting from the canonical std.Module surface: every method a module
// declares must be exposed as a key on the generated module map. Buzz camelCases
// the host's snake_case names, so the lookup key is camelCaseKey(meth.Name).
func TestBuzzBindingsMatchHostModules(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()

	checked := 0
	for _, m := range hostmodules.All() {
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
			key := camelCaseKey(meth.Name)
			if meth.BuzzName != "" {
				key = meth.BuzzName
			}
			_, ok := mod.MapGet(key)
			assert.Truef(t, ok, "buzz %s.%s is missing (host declares it as %q); generated bindings have drifted from std.Module",
				m.Name, key, meth.Name)
			checked++
		}
	}
	require.NotZero(t, checked, "no host methods were checked; the host module registry or buzz registries map changed shape")
	t.Logf("verified %d host methods are present in the Buzz bindings", checked)
}

// TestWASMRegistryMatchesCompatibleSubset guards modules_wasm.go against drift
// from modules.go. The two Modules maps are //go:build-exclusive, so no runtime
// value comparison can see both; instead this parses modules_wasm.go's source and
// asserts its module set equals exactly the WASM-capable entries here. It
// catches a pure-compute module added to modules.go but not mirrored into the wasm
// table (which would silently vanish from the playground), and vice versa.
func TestWASMRegistryMatchesCompatibleSubset(t *testing.T) {
	want := map[string]bool{}
	for name, reg := range Modules {
		if reg.Capabilities.Has(WASM) {
			want[name] = true
		}
	}
	// Test runs with the working directory set to this package's dir, so the wasm
	// table is a plain relative path.
	got := parseModulesMapKeys(t, "modules_wasm.go")
	assert.Equal(t, want, got,
		"modules_wasm.go must mirror exactly the WASM-capable entries of modules.go")
}

func TestSetWithDoesNotMutateSource(t *testing.T) {
	original := Set{"fs": {Capabilities: Capabilities(WASM)}}
	replaced := original.With("fs", ModuleReg{})

	assert.True(t, original["fs"].Capabilities.Has(WASM))
	assert.False(t, replaced["fs"].Capabilities.Has(WASM))
}

// parseModulesMapKeys parses filename as Go source (build constraints are ignored
// when parsing, so a wasm-tagged file is readable from the native test build) and
// returns the string keys of its top-level `var Modules = map[...]{...}` literal.
func parseModulesMapKeys(t *testing.T, filename string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	require.NoError(t, err)
	keys := map[string]bool{}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if name.Name != "Modules" || i >= len(vs.Values) {
				continue
			}
			cl, ok := vs.Values[i].(*ast.CompositeLit)
			if !ok {
				continue
			}
			found = true
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				lit, ok := kv.Key.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, uerr := strconv.Unquote(lit.Value)
				require.NoError(t, uerr)
				keys[s] = true
			}
		}
		return true
	})
	require.True(t, found, "no `var Modules = map[...]{...}` found in %s", filename)
	return keys
}
