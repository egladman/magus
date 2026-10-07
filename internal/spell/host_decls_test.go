package spell

import (
	"io/fs"
	"testing"

	"github.com/egladman/magus/internal/hostmodules"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModuleDeclsParse is the guard for a failure mode with no runtime symptom.
// SetModuleDecls DROPS a source that does not parse, so a malformed declaration does
// not raise an error; it silently un-types the whole module, and every call through
// it goes back to being unchecked while still running fine. Nothing in a passing test
// suite would look different.
//
// That is not hypothetical: KnowledgeGodNode mirrored a Go field named `in` to a bare
// `in:`, which the lexer reads as the foreach keyword, and the file sat committed and
// unparseable because no import path registered the magus mirrors as declarations.
func TestModuleDeclsParse(t *testing.T) {
	for _, mod := range hostmodules.All() {
		src, ok := ModuleDecls(mod.Name)
		require.Truef(t, ok, "no declarations generated for the %s module", mod.Name)
		_, err := buzz.Parse(src)
		require.NoErrorf(t, err, "the generated %s declarations must parse", mod.Name)
	}
}

func TestModuleDeclsRepeatTheEmbeddedFile(t *testing.T) {
	for _, name := range []string{"magus", "fs", "no-such-module"} {
		b, err := fs.ReadFile(hostDeclsFS, "gen/decls/"+name+".buzz")
		for range 2 {
			src, ok := ModuleDecls(name)
			assert.Equal(t, err == nil, ok, name)
			assert.Equal(t, string(b), src, name)
		}
	}
}

// BenchmarkModuleDecls registers every host module's declarations, as each new Buzz
// session does.
func BenchmarkModuleDecls(b *testing.B) {
	mods := hostmodules.All()
	b.Run("memo=false", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, mod := range mods {
				if data, err := fs.ReadFile(hostDeclsFS, "gen/decls/"+mod.Name+".buzz"); err == nil {
					_ = string(data)
				}
			}
		}
	})
	b.Run("memo=true", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, mod := range mods {
				_, _ = ModuleDecls(mod.Name)
			}
		}
	})
}

// TestModuleDeclsDeclareEveryMethod pins the declarations to the module they describe:
// every method gets a signature, so a new one cannot be added and left untyped.
// Variadic methods are the deliberate exception: Buzz has no variadic parameter, so
// fs\join("a", "b", "c") cannot be spelled with a fixed parameter list.
func TestModuleDeclsDeclareEveryMethod(t *testing.T) {
	for _, mod := range hostmodules.All() {
		src, _ := ModuleDecls(mod.Name)
		for _, m := range mod.Methods {
			name := std.CamelCase(m.Name)
			if m.BuzzName != "" {
				name = m.BuzzName
			}
			variadic := false
			for _, a := range m.Args {
				variadic = variadic || a.Variadic
			}
			if variadic {
				assert.Containsf(t, src, "// "+name+" is variadic",
					"%s.%s is variadic, so the declarations must say why it stays untyped", mod.Name, name)
				continue
			}
			assert.Containsf(t, src, "export extern fun "+name+"(",
				"%s.%s has no generated declaration, so calls to it stay unchecked", mod.Name, name)
		}
	}
}
