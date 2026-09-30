package bindings

import (
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	objects := parseObjects(t, magusContextSource)
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

var typeIdent = regexp.MustCompile(`[A-Za-z_]\w*`)

// Every type the hand-written mirrors name is declared in the magus declarations.
func TestMagusUndeclaredTypeSourceNamesDeclaredTypes(t *testing.T) {
	prog, err := buzz.ParseEmbedded(magusDeclSource())
	require.NoError(t, err)
	declared := map[string]bool{
		"int": true, "double": true, "str": true, "bool": true, "void": true, "any": true, "fun": true,
	}
	for _, stmt := range prog.Stmts {
		switch d := stmt.(type) {
		case *ast.ObjectDecl:
			declared[d.Name] = true
		case *ast.EnumDecl:
			declared[d.Name] = true
		}
	}
	for _, d := range parseObjects(t, magusUndeclaredTypeSource) {
		annots := []string{}
		for _, f := range d.Fields {
			annots = append(annots, f.TypeAnnot)
		}
		for _, m := range d.Methods {
			annots = append(annots, m.RetAnnot)
			annots = append(annots, m.ParamAnnots...)
		}
		for _, a := range annots {
			for _, name := range typeIdent.FindAllString(a, -1) {
				assert.True(t, declared[name], "%s names undeclared type %q", d.Name, name)
			}
		}
	}
}

// The records run on their own; a failure here means the generated declarations name a
// type they do not declare, or are stale.
func TestMagusDeclarationsRun(t *testing.T) {
	records, err := magusRecords()
	require.NoError(t, err, "regenerate with `magus run spells-generate`")
	var names []string
	for _, r := range records {
		names = append(names, r.name)
	}
	assert.Subset(t, names, []string{"DirsOptions", "NeighborhoodOptions", "PathOptions", "CheckStatus", "TargetRun", "Run"})
	assert.NotContains(t, names, "Context", "the target context is the host's to build")
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
