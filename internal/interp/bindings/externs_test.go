package bindings

import (
	"testing"

	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
)

// TestMagusExternsAreBound holds the two halves of an Extern member together.
//
// An Extern is DECLARED in std/magus.go and BOUND here, by buildMagusNS. Nothing
// else connects them: the declaration generates no trampoline, so the compiler
// cannot notice when one side moves. Both directions are failures, and they fail
// differently:
//
//   - Declared but not bound: the checker says the member exists, so a call to it
//     type-checks and then finds null at run time - `buzz: null is not callable`,
//     the exact class of bug the declarations were added to prevent, reintroduced
//     from the other side.
//   - Bound but not declared: the member is invisible to the checker, so a typo
//     near it reads as an unknown member (BZZ1007) while the real member gets no
//     signature at all.
//
// MagusModuleKeys reports what the bindings actually register, so it is the
// authority here rather than a list repeated in this file.
func TestMagusExternsAreBound(t *testing.T) {
	bound := map[string]bool{}
	for _, k := range MagusModuleKeys() {
		bound[k] = true
	}

	declared := map[string]bool{}
	for _, m := range std.All() {
		if m.Name != "magus" {
			continue
		}
		for _, meth := range m.Methods {
			if !meth.Extern {
				continue
			}
			key := std.CamelCase(meth.Name)
			if meth.BuzzName != "" {
				key = meth.BuzzName
			}
			declared[key] = true
			assert.Truef(t, bound[key],
				"magus\\%s is declared Extern in std/magus.go but nothing binds it in buildMagusNS;\n"+
					"a call to it type-checks and then fails at run time with 'null is not callable'", key)
		}
	}
	assert.NotEmpty(t, declared, "no Extern members found; this test would pass vacuously")
}

// TestMagusNamespacesAreBound is the same contract one level down, for the provider
// namespaces std declares as objects with static extern methods.
//
// Both halves matter. A declared member nothing binds type-checks and then finds null
// at run time; a bound member nothing declares is invisible, so a typo beside it
// cannot be distinguished from a member that does not exist - which is the whole
// reason these stopped being hand-wired-and-undeclared.
func TestMagusNamespacesAreBound(t *testing.T) {
	top := map[string]bool{}
	for _, k := range MagusModuleKeys() {
		top[k] = true
	}

	var checked int
	for _, m := range std.All() {
		if m.Name != "magus" {
			continue
		}
		for _, ns := range m.Namespaces {
			if !assert.Truef(t, top[ns.Name], "magus\\%s is declared as a Namespace but nothing binds it", ns.Name) {
				continue
			}
			bound := map[string]bool{}
			for _, k := range MagusNamespaceKeys(ns.Name) {
				bound[k] = true
			}
			for _, meth := range ns.Methods {
				key := std.CamelCase(meth.Name)
				if meth.BuzzName != "" {
					key = meth.BuzzName
				}
				assert.Truef(t, bound[key],
					"magus\\%s.%s is declared but not bound; a call to it type-checks and then\n"+
						"fails at run time with 'null is not callable'", ns.Name, key)
				checked++
			}
		}
	}
	assert.NotZero(t, checked, "no namespace methods checked; this test would pass vacuously")
}
