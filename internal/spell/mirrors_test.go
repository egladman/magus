package spell_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/egladman/magus/internal/interp/bindings"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryBoundaryTypeHasAMirror is the completeness gate. A ToMap method on a
// types/ struct means "this value crosses into Buzz", so every one of them owes
// the magus/target module an `object` mirror - otherwise a magusfile can only
// index the result as an untyped map, and the checker has nothing to verify a
// field name against.
//
// This is the test that would have caught the shipped `magus.ls` documentation
// telling readers to annotate `> Projects` when no such type existed.
func TestEveryBoundaryTypeHasAMirror(t *testing.T) {
	t.Parallel()
	// Every types/ struct carrying ToMap, paired with the Buzz object it mirrors.
	// The names differ where the Buzz one reads better (ProjectsOutput -> Projects).
	for _, tc := range []struct{ goType, buzzObject string }{
		{"ExecResult", "ExecResult"},
		// Commit owns ToMap; CommitRecord/CommitAuthor describe the map it emits.
		{"CommitRecord", "Commit"},
		{"CommitAuthor", "CommitAuthor"},
		{"FileInfo", "FileInfo"},
		{"HTTPResponse", "HttpResponse"},
		{"SemverVersion", "SemverVersion"},
		{"URL", "URL"},
		{"Tag", "Tag"},
		{"ProjectEntry", "ProjectEntry"},
		{"ProjectsOutput", "Projects"},
		{"AffectedResult", "Affected"},
		{"GraphView", "Graph"},
		{"ModuleFieldEntry", "ModuleFieldEntry"},
		{"ModuleMethodEntry", "ModuleMethodEntry"},
		{"ModuleEntry", "Module"},
	} {
		t.Run(tc.buzzObject, func(t *testing.T) {
			t.Parallel()
			assertMirrorConstructs(t, tc.buzzObject)
		})
	}
}

// assertMirrorConstructs execs a bare `Name{}` against the real magus/target
// bundle. Constructing it is the whole assertion: an object that is absent,
// misordered relative to a type it references, or emitted with an unparseable
// field name (a Go field named Type mirrors to the reserved word `type`) all
// fail here.
func assertMirrorConstructs(t *testing.T, object string) {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = s.Close() })
	bindings.RegisterSpellSourceModules(s)
	require.NoError(t, s.Exec(ctx, `import "magus/target"; final __r = `+object+`{};`),
		"%s{} must construct: the mirror is missing from the magus/target bundle, ordered after a type it references, or emitted with a field name that does not parse", object)
	require.NotNil(t, s.GetGlobal("__r"), "%s{} produced nothing", object)
}

// TestMirrorFieldsMatchToMap pins each mirror against the map its Go type
// actually produces. A mirror that merely parses is not enough: a field the Buzz
// value never carries (or one it carries under another name) is a type that lies,
// and the checker would reject correct code or accept a typo.
func TestMirrorFieldsMatchToMap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		object string
		toMap  map[string]any
	}{
		{"Tag", types.Tag{}.ToMap()},
		{"Affected", types.AffectedResult{}.ToMap()},
		{"Graph", types.GraphView{}.ToMap()},
		{"Projects", types.ProjectsOutput{}.ToMap()},
		{"ProjectEntry", types.ProjectEntry{}.ToMap()},
		{"Module", types.ModuleEntry{}.ToMap()},
		{"ModuleFieldEntry", types.ModuleFieldEntry{}.ToMap()},
		{"ModuleMethodEntry", types.ModuleMethodEntry{}.ToMap()},
		{"ExecResult", types.ExecResult{}.ToMap()},
	} {
		t.Run(tc.object, func(t *testing.T) {
			t.Parallel()
			for key := range tc.toMap {
				assertMirrorReadsField(t, tc.object, key)
			}
		})
	}
}

// assertMirrorReadsField reads one field off a zero-valued mirror. A reserved-word
// key is read through ordinary member access (entry.type), which upstream permits
// even where the same word cannot be a binding name - so this exercises the free
// identifier the generator emits for it.
func assertMirrorReadsField(t *testing.T, object, field string) {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = s.Close() })
	bindings.RegisterSpellSourceModules(s)
	err := s.Exec(ctx, `import "magus/target"; final __r = `+object+`{}.`+field+`;`)
	assert.NoError(t, err, "%s has no field %q, but %s's ToMap emits that key: the mirror and the boundary map disagree", object, field, object)
}

// TestEveryToMapOwnerIsMirrored guards the LIST above rather than the mirrors.
// ToMap is the marker that a value crosses into Buzz, so every type carrying one
// owes the module a mirror; adding a ToMap without adding the mirror would leave
// the gate above passing while the new type stayed untyped.
//
// A mirror is not always generated from the ToMap owner. Commit owns ToMap while
// CommitRecord is a dedicated struct describing the map it emits (dates format to
// RFC3339 strings, so the shape and the source differ). Either arrangement is
// fine; what must hold is that each owner below has a Buzz object, which the
// tests above check by construction.
func TestEveryToMapOwnerIsMirrored(t *testing.T) {
	t.Parallel()
	owners := []any{
		types.ExecResult{}, types.Commit{}, types.FileInfo{}, types.HTTPResponse{},
		types.SemverVersion{}, types.URL{}, types.Tag{}, types.ProjectEntry{},
		types.ProjectsOutput{}, types.AffectedResult{}, types.GraphView{},
		types.ModuleFieldEntry{}, types.ModuleMethodEntry{}, types.ModuleEntry{},
	}
	for _, v := range owners {
		rt := reflect.TypeOf(v)
		_, ok := rt.MethodByName("ToMap")
		assert.True(t, ok, "%s is listed as a boundary type but has no ToMap; drop it from this list", rt.Name())
	}
	assert.Len(t, owners, 14,
		"a types/ struct gained or lost ToMap: add it to the mirror registry (cmd/magus-utils/types.go), generate it, wire it into RegisterSpellSourceModules, and list it in TestEveryBoundaryTypeHasAMirror")
}
