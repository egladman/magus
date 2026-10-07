package scipbuzz

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/require"
)

// indexCase indexes one snapshot input directory and returns the document at rel.
func indexCase(t *testing.T, name, rel string) *scip.Document {
	t.Helper()
	idx, warnings, err := indexDir(filepath.Join(snapshotInput, name))
	require.NoError(t, err)
	require.Empty(t, warnings)
	for _, doc := range idx.Documents {
		if doc.RelativePath == rel {
			return doc
		}
	}
	require.Failf(t, "no document", "%s has no document %s", name, rel)
	return nil
}

// spots returns the 1-based line:column of every occurrence of symbol in doc.
func spots(doc *scip.Document, symbol string) []string {
	var out []string
	for _, occ := range doc.Occurrences {
		if occ.Symbol != symbol {
			continue
		}
		r, _ := occ.SourceRange()
		out = append(out, fmt.Sprintf("%d:%d", r.Start.Line+1, r.Start.Character+1))
	}
	return out
}

// localNamed returns the symbol of the local called name that owner declares.
func localNamed(t *testing.T, doc *scip.Document, owner, name string) string {
	t.Helper()
	for _, info := range doc.Symbols {
		if scip.IsLocalSymbol(info.Symbol) && info.DisplayName == name && info.EnclosingSymbol == owner {
			return info.Symbol
		}
	}
	require.Failf(t, "no local", "%s declares no local %s", owner, name)
	return ""
}

// TestImplicitLabelsAreNotOccurrences pins that a bare identifier standing for a
// label as well as a value is left out: `f(a, b)` is `f(a, b: b)` and `Rect{ w }`
// is `Rect{ w = w }`. A rename rewriting that one range would rename the label
// too, so the only safe occurrence is none.
func TestImplicitLabelsAreNotOccurrences(t *testing.T) {
	doc := indexCase(t, "implicit_labels", "main.buzz")
	main := "scip-buzz buzz . . `main.buzz`/main()."
	require.Equal(t, []string{"11:11", "15:20", "16:20"}, spots(doc, localNamed(t, doc, main, "w")),
		"only the declaration and each first argument")
	require.Equal(t, []string{"12:11", "13:28", "14:25", "16:26"}, spots(doc, localNamed(t, doc, main, "h")),
		"only the declaration, explicit field values and a labeled argument")
}

// TestFlatImportBindsExportsUnqualified pins how gopherbuzz binds a file import
// with no alias or with `as _`: the file's exported names unqualified, the path's
// basename as a namespace, and the file's own `namespace a\b` path. A bare use of
// an export references the defining file's symbol, or a rename of it would miss
// the call.
func TestFlatImportBindsExportsUnqualified(t *testing.T) {
	twice := "scip-buzz buzz . . `lib/util.buzz`/twice()."
	point := "scip-buzz buzz . . `lib/util.buzz`/Point#"
	base := "scip-buzz buzz . . `lib/util.buzz`/base."
	hidden := "scip-buzz buzz . . `lib/util.buzz`/hidden()."

	doc := indexCase(t, "imports_unaliased", "main.buzz")
	require.Equal(t, []string{"4:33", "5:20", "6:26"}, spots(doc, twice))
	require.Equal(t, []string{"4:14", "4:22"}, spots(doc, point))
	require.Equal(t, []string{"4:39"}, spots(doc, base))
	require.Empty(t, spots(doc, hidden), "an unexported name is not imported")

	doc = indexCase(t, "imports_flat", "main.buzz")
	require.Equal(t, []string{"4:33", "6:20"}, spots(doc, twice))
	require.Empty(t, spots(doc, hidden), "an unexported name is not imported")
}

// TestTypeParametersShadowTopLevelTypes pins that `::<T>` declares a type of its
// own: inside the generic function, object or function type, T is that parameter
// and never the top-level object T, while a type argument names the top-level T.
func TestTypeParametersShadowTopLevelTypes(t *testing.T) {
	doc := indexCase(t, "generics", "main.buzz")
	require.Equal(t, []string{"1:8", "17:13", "17:51", "18:28"}, spots(doc, "scip-buzz buzz . . `main.buzz`/T#"))
	box := "scip-buzz buzz . . `main.buzz`/Box#"
	require.Equal(t, []string{"5:14", "6:12", "8:17"}, spots(doc, localNamed(t, doc, box, "T")))
	first := "scip-buzz buzz . . `main.buzz`/first()."
	require.Equal(t, []string{"13:13", "13:24", "13:30"}, spots(doc, localNamed(t, doc, first, "T")))
}

// TestCompoundAssignmentIsOneOccurrence walks `n += 1`, which the parser desugars
// to `n = n + 1` with one node in both places. The target is one use, so the walk
// records it once, as a write.
func TestCompoundAssignmentIsOneOccurrence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.buzz")
	require.NoError(t, os.WriteFile(path, []byte("fun main(args: [str]) > void {\n    var n = 1;\n    n += 2;\n}\n"), 0o644))
	ix := &indexer{project: dir, workspace: dir, files: map[string]*file{}, external: map[string]*scip.SymbolInformation{}}
	w := newWalker(ix, ix.load(path))
	w.walkFile()
	var roles []scip.SymbolRole
	for _, occ := range w.occs {
		if r, _ := occ.SourceRange(); r.Start.Line == 2 {
			roles = append(roles, scip.SymbolRole(occ.SymbolRoles))
		}
	}
	require.Equal(t, []scip.SymbolRole{scip.SymbolRole_WriteAccess}, roles)
}

type strayNode struct{ ast.Pos }

// TestVisitWarnsOnAnUnhandledNode keeps a node kind the walker does not know from
// passing in silence: the names inside it would get no occurrence.
func TestVisitWarnsOnAnUnhandledNode(t *testing.T) {
	var warnings []string
	ix := &indexer{onWarn: func(msg string) { warnings = append(warnings, msg) }}
	w := newWalker(ix, &file{rel: "x.buzz"})
	w.push()
	w.visit(strayNode{})
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "strayNode")
}

// TestUnplacedLocalDeclarationStillBindsItsName keeps a block-level object or
// enum whose header cannot be placed in scope: binding nothing would resolve its
// uses to an outer declaration of the same name, which a rename of that one would
// then rewrite.
func TestUnplacedLocalDeclarationStillBindsItsName(t *testing.T) {
	dir := t.TempDir()
	src := "object Square {\n    n: int,\n}\n\nenum Level { low }\n\n" +
		"fun main(args: [str]) > void {\n" +
		"    object Square {\n        side: int,\n    }\n" +
		"    enum Level { high }\n" +
		"    final s = Square{ side = 3 };\n" +
		"    final l = Level.high;\n" +
		"}\n"
	path := filepath.Join(dir, "main.buzz")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	var warnings []string
	ix := &indexer{project: dir, workspace: dir, files: map[string]*file{}, external: map[string]*scip.SymbolInformation{},
		onWarn: func(msg string) { warnings = append(warnings, msg) }}
	f := ix.load(path)
	require.NotNil(t, f.prog)
	body := f.prog.Stmts[2].(*ast.FunDecl).Body.Stmts
	body[0].(*ast.ObjectDecl).Pos = ast.Pos{}
	body[1].(*ast.EnumDecl).Pos = ast.Pos{}

	doc := ix.document(f)
	require.Equal(t, []string{"1:8"}, spots(doc, "scip-buzz buzz . . `main.buzz`/Square#"))
	require.Equal(t, []string{"5:6"}, spots(doc, "scip-buzz buzz . . `main.buzz`/Level#"))
	require.NotEmpty(t, warnings, "the unplaced headers are reported")
}
