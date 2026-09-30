package bindings

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

func parseObjects(t *testing.T, src string) map[string]*ast.ObjectDecl {
	t.Helper()
	prog, err := buzz.ParseEmbedded(src)
	require.NoError(t, err)
	out := map[string]*ast.ObjectDecl{}
	for _, stmt := range prog.Stmts {
		if d, ok := stmt.(*ast.ObjectDecl); ok {
			out[d.Name] = d
		}
	}
	return out
}

func objectMembers(d *ast.ObjectDecl) []string {
	var out []string
	for _, f := range d.Fields {
		out = append(out, f.Name)
	}
	for _, m := range d.Methods {
		out = append(out, m.Name)
	}
	slices.Sort(out)
	return out
}

// A member added to buildTargetContext must be mirrored on magus\Context, or the
// checker refuses every target that calls it. The removed declarations stay
// unmirrored so a call to one fails the check.
func TestMagusContextMirrorsTheTargetContext(t *testing.T) {
	objects := parseObjects(t, spell.MagusContextSource)
	require.Contains(t, objects, "Context")
	require.Contains(t, objects, "Exec")

	want := slices.DeleteFunc(TargetContextKeys(), func(k string) bool {
		return k == ctxMarker || k == "inputs" || k == "outputs" || k == "updates"
	})
	slices.Sort(want)
	assert.Equal(t, want, objectMembers(objects["Context"]))

	assert.Equal(t, []string{"cwd", "env", "withCwd", "withEnv"}, objectMembers(objects["Exec"]))
	for _, refused := range ExecRefusedKeys() {
		assert.NotContains(t, objectMembers(objects["Exec"]), refused)
	}
}

// magus\ records and enums exist at run time, directly and through an aliased import.
func TestMagusRecordsAreConstructible(t *testing.T) {
	ctx := t.Context()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	DeclareMagusTypes(sess)
	require.NoError(t, sess.Exec(ctx, `
import "magus";
import "magus" as m;

export fun build() > str {
    final dirs = magus\DirsOptions{ language = "go", depth = 2 };
    final path = m\PathOptions{ relations = ["imports"] };
    final hood = magus\NeighborhoodOptions{ depth = 1 };
    final check = magus\Check{};
    final none = check.status == magus\CheckStatus.none;
    return "{dirs.language} {dirs.depth} {dirs.layer == ""} {path.relations[0]} {hood.depth} {none}";
}
`))
	got, err := sess.CallValue(ctx, sess.Exports()["build"], nil)
	require.NoError(t, err)
	assert.Equal(t, "go 2 true imports 1 true", got.AsString())
}

// A program's own type outranks a magus record of the same name at run time too.
func TestMagusRecordsYieldToAProgramsOwnType(t *testing.T) {
	ctx := t.Context()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	DeclareMagusTypes(sess)
	require.NoError(t, sess.Exec(ctx, `
object Path { label: str = "mine" }

export fun own() > str {
    return Path{}.label;
}
`))
	got, err := sess.CallValue(ctx, sess.Exports()["own"], nil)
	require.NoError(t, err)
	assert.Equal(t, "mine", got.AsString())
}
