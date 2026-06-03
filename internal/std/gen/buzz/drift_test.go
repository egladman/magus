package buzzgen

import (
	"context"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/std"
)

// camelCase mirrors the snake_case→camelCase transform the Buzz emitter applies
// to method keys (magus-bindings-gen). A single-word name is unchanged.
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

// registries maps each module name to its generated Register function. It must
// list every module that has a gen/buzz trampoline; buildBuzzStd in the bindings
// package wires the same set.
var registries = map[string]func(context.Context, *buzz.Session) buzz.Value{
	"os":       RegisterOs,
	"platform": RegisterPlatform,
	"fs":       RegisterFs,
	"vcs":      RegisterVcs,
	"archive":  RegisterArchive,
	"crypto":   RegisterCrypto,
	"env":      RegisterEnv,
	"json":     RegisterJson,
	"http":     RegisterHttp,
	"time":     RegisterTime,
	"fmt":      RegisterFmt,
	"markdown": RegisterMarkdown,
	"charm":    RegisterCharm,
	"encoding": RegisterEncoding,
	"path":     RegisterPath,
	"strings":  RegisterStrings,
}

// TestBuzzBindingsMatchHostModules guards against the gen/buzz trampolines
// drifting from the canonical std.Module surface: every method a module
// declares must be exposed as a key on the generated module map. Buzz camelCases
// the host's snake_case names, so the lookup key is camelCase(meth.Name).
func TestBuzzBindingsMatchHostModules(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	defer sess.Close()

	checked := 0
	for _, m := range std.All() {
		reg, ok := registries[m.Name]
		if !ok {
			// Hand-built modules (e.g. "magus") have no gen/buzz trampoline.
			continue
		}
		mod := reg(ctx, sess)
		if !mod.IsMap() {
			t.Fatalf("Register%s did not return a map", m.Name)
		}
		for _, meth := range m.Methods {
			// extra is self-complete: every declared method must be on the Buzz
			// surface, even ones Buzz's stdlib also covers (see std.NativeBuzzEquiv).
			key := camelCase(meth.Name)
			if _, ok := mod.MapGet(key); !ok {
				t.Errorf("buzz %s.%s is missing (host declares it as %q); gen/buzz has drifted from std.Module",
					m.Name, key, meth.Name)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no host methods were checked; the host module registry or buzz registries map changed shape")
	}
	t.Logf("verified %d host methods are present in the Buzz bindings", checked)
}
