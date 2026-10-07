package knowledge

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// fn declares a function in dir's first file, its signature rendered as lang's indexer renders
// one: scip-go's `func name(...)` or scip-buzz's `fun name(...)`.
func (f *precedentFixture) fn(dir, lang, name, sig string) types.Case {
	f.line++
	ns, file := precedentNamespace(dir, "go"), dir+"/f0.go"
	if lang == "buzz" {
		file = dir + "/f0.buzz"
		ns = "symbol:scip-buzz buzz `" + file + "`/"
	}
	id := ns + name + "()."
	source := fmt.Sprintf("%s:%d", file, f.line)
	f.g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: name, Source: source,
		Attrs: map[string]string{attrNamespace: ns, attrLanguage: lang, attrSymbolKind: "Function", AttrSignature: sig}})
	return types.Case{Node: id, Source: source}
}

func keyedRow(t *testing.T, rows []types.Precedent, family types.PrecedentFamily, lang string, key types.PrecedentKey) types.Precedent {
	t.Helper()
	for _, r := range rows {
		if r.Family == family && r.Scope.Language == lang && r.Key.Type == key.Type && slices.Equal(r.Key.Order, key.Order) {
			return r
		}
	}
	require.FailNow(t, "no row", "%s %s %+v in %+v", family, lang, key, rows)
	return types.Precedent{}
}

func familyRows(rows []types.Precedent, family types.PrecedentFamily, lang string) []types.PrecedentKey {
	var out []types.PrecedentKey
	for _, r := range rows {
		if r.Family == family && r.Scope.Language == lang {
			out = append(out, r.Key)
		}
	}
	return out
}

// A function naming two parameters of one type, or leaving one blank, has no say in what that
// type is called; generated code has none at all. In Buzz the name is the call label.
func TestPrecedentsNameAParameterByItsType(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.fn("internal/a", "go", "ignore", "func ignore(_ context.Context) error")
	f.fn("internal/a", "go", "merge", "func merge(ctx, other context.Context) error")
	var runs []types.Case
	for i := range 5 {
		runs = append(runs, f.fn("internal/a", "go", fmt.Sprintf("run%d", i), fmt.Sprintf("func run%d(ctx context.Context, root string) error", i)))
	}
	watch := f.fn("internal/a", "go", "watch", "func watch(c context.Context) error")
	f.fn("internal/gen", "go", "generated", "func generated(cx context.Context) error")
	var loads []types.Case
	for i := range 5 {
		loads = append(loads, f.fn("hack/lib", "buzz", fmt.Sprintf("load%d", i), fmt.Sprintf("fun load%d(path: str) > str", i)))
	}
	read := f.fn("hack/lib", "buzz", "read", "fun read(file: str) > str")

	rows := f.g.Precedents(PrecedentOptions{Generated: map[string]bool{"internal/gen/f0.go": true}})

	assert.Equal(t, types.Precedent{
		Family: types.PrecedentParamNameByType, Scope: goScope, Key: types.PrecedentKey{Type: "ctx", Name: "ctx"},
		Follow: 5, Cohort: 6, Share: 5.0 / 6, Established: true,
		Cited: runs[:3], Departures: []types.Case{watch},
	}, keyedRow(t, rows, types.PrecedentParamNameByType, "go", types.PrecedentKey{Type: "ctx"}))
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentParamNameByType, Scope: types.PrecedentScope{Language: "buzz"}, Key: types.PrecedentKey{Type: "str", Name: "path"},
		Follow: 5, Cohort: 6, Share: 5.0 / 6, Established: true,
		Cited: loads[:3], Departures: []types.Case{read},
	}, keyedRow(t, rows, types.PrecedentParamNameByType, "buzz", types.PrecedentKey{Type: "str"}))
	assert.Equal(t, []types.PrecedentKey{{Type: "ctx", Name: "ctx"}, {Type: "string", Name: "root"}},
		familyRows(rows, types.PrecedentParamNameByType, "go"), "a type with fewer carriers than the gate makes no row")
}

// The order a pair is taken in is the one most of its carriers use, whichever sorts first.
func TestPrecedentsOrderAParameterPair(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	back := f.fn("internal/a", "go", "back", "func back(dir, root string) error")
	var copies []types.Case
	for i := range 5 {
		copies = append(copies, f.fn("internal/a", "go", fmt.Sprintf("copy%d", i), fmt.Sprintf("func copy%d(root, dir string) error", i)))
	}
	f.fn("internal/a", "go", "lone", "func lone(dir, base string) error")

	rows := f.g.Precedents(PrecedentOptions{})

	assert.Equal(t, types.Precedent{
		Family: types.PrecedentParamOrder, Scope: goScope, Key: types.PrecedentKey{Order: []string{"root", "dir"}},
		Follow: 5, Cohort: 6, Share: 5.0 / 6, Established: true,
		Cited: copies[:3], Departures: []types.Case{back},
	}, keyedRow(t, rows, types.PrecedentParamOrder, "go", types.PrecedentKey{Order: []string{"root", "dir"}}))
	assert.Len(t, familyRows(rows, types.PrecedentParamOrder, "go"), 1, "a pair one function takes makes no row")
}

// A result list Key collapses to "values" still has an order. Buzz's raise is not a positional
// result, so error-last has no Buzz row.
func TestPrecedentsPlaceTheContextFirstAndTheErrorLast(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	var cited []types.Case
	for i := range 4 {
		cited = append(cited, f.fn("internal/a", "go", fmt.Sprintf("fetch%d", i), fmt.Sprintf("func fetch%d(ctx context.Context) error", i)))
	}
	f.fn("internal/a", "go", "split", "func split(ctx context.Context, p string) (string, string, error)")
	late := f.fn("internal/a", "go", "late", "func late(name string, ctx context.Context) (error, int)")
	f.fn("internal/a", "go", "pure", "func pure(name string) string")
	lint := f.fn("hack/lib", "buzz", "lint", `fun lint(ctx: magus\Context) > void !> any`)

	rows := f.g.Precedents(PrecedentOptions{})

	assert.Equal(t, types.Precedent{
		Family: types.PrecedentCtxFirst, Scope: goScope,
		Follow: 5, Cohort: 6, Share: 5.0 / 6, Established: true,
		Cited: cited[:3], Departures: []types.Case{late},
	}, precedentRow(t, rows, types.PrecedentCtxFirst, goScope))
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentCtxFirst, Scope: types.PrecedentScope{Language: "buzz"},
		Follow: 1, Cohort: 1, Share: 1, Cited: []types.Case{lint},
	}, precedentRow(t, rows, types.PrecedentCtxFirst, types.PrecedentScope{Language: "buzz"}))
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentErrorLast, Scope: goScope,
		Follow: 5, Cohort: 6, Share: 5.0 / 6, Established: true,
		Cited: cited[:3], Departures: []types.Case{late},
	}, precedentRow(t, rows, types.PrecedentErrorLast, goScope))
	assert.Empty(t, familyRows(rows, types.PrecedentErrorLast, "buzz"))
}
