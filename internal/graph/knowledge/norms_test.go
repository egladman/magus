package knowledge

import (
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// normFixture writes a graph the way scip-go indexes one: every file defines its package's
// namespace symbol, and an import is a file referencing the imported namespace.
type normFixture struct {
	g    *Graph
	line int
}

func newNormFixture() *normFixture { return &normFixture{g: NewGraph()} }

func normNamespace(dir, lang string) string {
	pkg := "example.com/m"
	if dir != "." {
		pkg += "/" + dir
	}
	if lang != "go" {
		return "symbol:npm m 1.0 `" + dir + "`/"
	}
	return "symbol:gomod example.com/m `" + pkg + "`/"
}

func (f *normFixture) edge(from, to string, rel types.RelationID) {
	f.g.AddEdge(types.KnowledgeEdge{Source: from, Target: to, Relation: rel, Confidence: types.ConfidenceExtracted, Score: 1})
}

// file adds a file defining the namespace ns, labelled label, and the top-level dir holding it.
func (f *normFixture) file(file, ns, label, lang string) {
	if top, _, ok := strings.Cut(file, "/"); ok {
		f.g.AddNode(types.KnowledgeNode{ID: dirID(top), Kind: types.KindDir, Label: top, Source: top})
	}
	f.g.AddNode(types.KnowledgeNode{ID: ns, Kind: types.KindSymbol, Label: label, Source: file + ":1",
		Attrs: map[string]string{attrNamespace: ns, attrLanguage: lang, attrSymbolKind: "Package"}})
	f.g.AddNode(types.KnowledgeNode{ID: "file:" + file, Kind: types.KindFile, Label: file, Source: file})
	f.edge("file:"+file, ns, types.RelationDefines)
}

// pkg adds a Go package of n source files in dir.
func (f *normFixture) pkg(dir string, n int) {
	for i := range n {
		f.file(fmt.Sprintf("%s/f%d.go", dir, i), normNamespace(dir, "go"), path.Base(dir), "go")
	}
}

func (f *normFixture) imports(from, to string) {
	f.edge("file:"+from+"/f0.go", normNamespace(to, "go"), types.RelationReferences)
}

// value declares a package-level Go var of type typ in dir's first file.
func (f *normFixture) value(dir, name, typ string) string {
	f.line++
	ns := normNamespace(dir, "go")
	id := ns + name + "."
	f.g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: name, Source: fmt.Sprintf("%s/f0.go:%d", dir, f.line),
		Attrs: map[string]string{attrNamespace: ns, attrLanguage: "go", attrSymbolKind: "Variable", AttrSignature: "var " + name + " " + typ}})
	return id
}

func normRow(t *testing.T, rows []types.Norm, family, scope string) types.Norm {
	t.Helper()
	for _, r := range rows {
		if r.Family == family && r.Scope == scope {
			return r
		}
	}
	require.FailNow(t, "no row", "%s %s in %+v", family, scope, rows)
	return types.Norm{}
}

// TestNormsPinThisTreesMeasuredShape reproduces what the miner counted on magus's own tree
// at 28ba1e69e: cmd -> internal 102/103, Err-named error values 85/87, in-package test files
// 846/854, with the real deviations.
func TestNormsPinThisTreesMeasuredShape(t *testing.T) {
	t.Parallel()

	f := newNormFixture()
	f.pkg("cmd/magus", 2)
	f.pkg("cmd/magus/gen", 1)
	f.pkg("internal/guard", 2)
	f.imports("cmd/magus", "internal/guard")
	f.imports("internal/guard", "cmd/magus/gen")
	f.imports("cmd/magus/gen", "internal/guard")
	internal := []string{"internal/guard"}
	for i := range 101 {
		dir := fmt.Sprintf("internal/p%d", i)
		internal = append(internal, dir)
		f.pkg(dir, 1)
		f.imports("cmd/magus", dir)
	}
	for i := range 85 {
		f.value(internal[i%len(internal)], fmt.Sprintf("ErrCase%d", i), "error")
	}
	f.value("cmd/magus", "magusErr", "error")
	f.value("cmd/magus", "inspectErr", "error")
	f.value("cmd/magus", "ErrPattern", "*regexp.Regexp")

	for i := range 846 {
		dir := internal[i%len(internal)]
		f.file(fmt.Sprintf("%s/t%d_test.go", dir, i), normNamespace(dir, "go"), path.Base(dir), "go")
	}
	f.pkg("libs/gopherbuzz", 2)
	external := []string{"conformance", "example", "fiber", "parity", "parser", "session", "marshal", "marshal_fuzz"}
	for _, name := range external {
		f.file("libs/gopherbuzz/"+name+"_test.go", normNamespace("libs/gopherbuzz_test", "go"), "buzz_test", "go")
	}

	table := f.g.Norms(NormOptions{Generated: map[string]bool{"cmd/magus/gen/f0.go": true}})

	assert.Equal(t, types.Norm{
		Family: types.NormDepDirection, Scope: "cmd, internal", Key: "cmd -> internal",
		Agree: 102, Cohort: 103, Share: 102.0 / 103,
		Examples:   []string{"cmd/magus -> internal/guard", "cmd/magus -> internal/p0", "cmd/magus -> internal/p1"},
		Deviations: []types.NormSite{{Subject: "internal/guard -> cmd/magus/gen", Source: "internal/guard"}},
	}, normRow(t, table, types.NormDepDirection, "cmd, internal"))

	assert.Equal(t, types.Norm{
		Family: types.NormErrSentinelName, Scope: "go", Key: "err<X>",
		Agree: 85, Cohort: 87, Share: 85.0 / 87,
		Examples: []string{"internal/guard.ErrCase0", "internal/p0.ErrCase1", "internal/p10.ErrCase11"},
		Deviations: []types.NormSite{
			{Subject: "cmd/magus.inspectErr", Source: "cmd/magus/f0.go:87"},
			{Subject: "cmd/magus.magusErr", Source: "cmd/magus/f0.go:86"},
		},
	}, normRow(t, table, types.NormErrSentinelName, "go"))

	tests := normRow(t, table, types.NormTestPackageName, "go")
	assert.Equal(t, [4]any{846, 854, false, 8}, [4]any{tests.Agree, tests.Cohort, tests.Silent, len(tests.Deviations)})
	assert.Equal(t, types.NormSite{
		Subject: "libs/gopherbuzz/conformance_test.go (package buzz_test)", Source: "libs/gopherbuzz/conformance_test.go",
	}, tests.Deviations[0])
}

func TestNormsStaySilentBelowEitherGate(t *testing.T) {
	t.Parallel()

	f := newNormFixture()
	f.pkg("internal/a", 1)
	for i := range 4 {
		f.value("internal/a", fmt.Sprintf("ErrX%d", i), "error")
	}
	assert.True(t, normRow(t, f.g.Norms(NormOptions{}), types.NormErrSentinelName, "go").Silent,
		"a cohort of four is under the minimum of five")

	f.value("internal/a", "ErrX4", "error")
	f.value("internal/a", "notFound", "error")
	f.value("internal/a", "badInput", "error")
	row := normRow(t, f.g.Norms(NormOptions{}), types.NormErrSentinelName, "go")
	assert.Equal(t, [3]any{5, 7, true}, [3]any{row.Agree, row.Cohort, row.Silent}, "5 of 7 is under 80%")
}

func TestNormsSkipGeneratedAndTestCode(t *testing.T) {
	t.Parallel()

	f := newNormFixture()
	f.pkg("internal/a", 2)
	f.pkg("internal/a/gen", 1)
	for i := range 5 {
		f.value("internal/a", fmt.Sprintf("ErrX%d", i), "error")
	}
	f.value("internal/a/gen", "generatedErr", "error")
	f.g.AddNode(types.KnowledgeNode{ID: normNamespace("internal/a", "go") + "testErr.", Kind: types.KindSymbol, Label: "testErr",
		Source: "internal/a/f_test.go:3", Attrs: map[string]string{
			attrNamespace: normNamespace("internal/a", "go"), attrLanguage: "go", attrSymbolKind: "Variable", AttrSignature: "var testErr error",
		}})
	f.file("internal/a/gen/x_test.go", normNamespace("internal/a/gen_test", "go"), "gen_test", "go")
	f.file("internal/a/a_test.go", normNamespace("internal/a", "go"), "a", "go")
	f.pkg("mocks/a", 1)
	f.imports("mocks/a", "internal/a")

	table := f.g.Norms(NormOptions{Generated: map[string]bool{
		"internal/a/gen/f0.go": true, "internal/a/gen/x_test.go": true, "mocks/a/f0.go": true,
	}})
	for _, r := range table {
		assert.NotEqual(t, "internal, mocks", r.Scope, "a generated package's imports are its generator's: %+v", r)
	}
	errs := normRow(t, table, types.NormErrSentinelName, "go")
	assert.Equal(t, [2]int{5, 5}, [2]int{errs.Agree, errs.Cohort})
	tests := normRow(t, table, types.NormTestPackageName, "go")
	assert.Equal(t, [2]int{1, 1}, [2]int{tests.Agree, tests.Cohort})
}

// TestNormsJudgeTestPackagesOnlyWhereALanguagePackagesByDirectory: a language that makes each
// file its own module has no directory package for a test file to agree with.
func TestNormsJudgeTestPackagesOnlyWhereALanguagePackagesByDirectory(t *testing.T) {
	t.Parallel()

	f := newNormFixture()
	for _, dir := range []string{"web/a", "web/b", "web/c"} {
		for _, name := range []string{"x.ts", "y.ts", "x.test.ts"} {
			file := dir + "/" + name
			f.file(file, normNamespace(file, "typescript"), name, "typescript")
		}
	}
	for _, r := range f.g.Norms(NormOptions{}) {
		assert.NotEqual(t, types.NormTestPackageName, r.Family, "%+v", r)
	}
}

func TestNormsFanoutIsADistributionOverImporters(t *testing.T) {
	t.Parallel()

	f := newNormFixture()
	for i := range 12 {
		f.pkg(fmt.Sprintf("internal/p%d", i), 1)
	}
	for i := range 20 {
		f.pkg(fmt.Sprintf("cmd/c%02d", i), 1)
		f.imports(fmt.Sprintf("cmd/c%02d", i), "internal/p0")
	}
	f.pkg("cmd/big", 1)
	for i := range 12 {
		f.imports("cmd/big", fmt.Sprintf("internal/p%d", i))
	}
	// A package that never imports the layer is not in its cohort.
	f.pkg("cmd/none", 1)

	assert.Equal(t, types.Norm{
		Family: types.NormDepFanout, Scope: "internal", Key: "at most 1",
		Agree: 20, Cohort: 21, Share: 20.0 / 21,
		Quantiles:  &types.NormQuantiles{P95: 1, Max: 12},
		Examples:   []string{"cmd/c00 imports 1", "cmd/c01 imports 1", "cmd/c02 imports 1"},
		Deviations: []types.NormSite{{Subject: "cmd/big imports 12", Source: "cmd/big"}},
	}, normRow(t, f.g.Norms(NormOptions{}), types.NormDepFanout, "internal"))
}
