package knowledge

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// precedentFixture writes a graph the way scip-go indexes one: every file defines its
// package's namespace symbol, and an import is a file referencing the imported namespace.
type precedentFixture struct {
	g    *Graph
	line int
}

func newPrecedentFixture() *precedentFixture { return &precedentFixture{g: NewGraph()} }

func precedentNamespace(dir, lang string) string {
	pkg := "example.com/m"
	if dir != "." {
		pkg += "/" + dir
	}
	if lang != "go" {
		return "symbol:npm m 1.0 `" + dir + "`/"
	}
	return "symbol:gomod example.com/m `" + pkg + "`/"
}

func (f *precedentFixture) edge(from, to string, rel types.RelationID) {
	f.g.AddEdge(types.KnowledgeEdge{Source: from, Target: to, Relation: rel, Confidence: types.ConfidenceExtracted, Score: 1})
}

// file adds a file defining the namespace ns, labelled label, and the top-level dir holding it.
func (f *precedentFixture) file(file, ns, label, lang string) {
	if top, _, ok := strings.Cut(file, "/"); ok {
		f.g.AddNode(types.KnowledgeNode{ID: dirID(top), Kind: types.KindDir, Label: top, Source: top})
	}
	f.g.AddNode(types.KnowledgeNode{ID: ns, Kind: types.KindSymbol, Label: label, Source: file + ":1",
		Attrs: map[string]string{attrNamespace: ns, attrLanguage: lang, attrSymbolKind: "Package"}})
	f.g.AddNode(types.KnowledgeNode{ID: "file:" + file, Kind: types.KindFile, Label: file, Source: file})
	f.edge("file:"+file, ns, types.RelationDefines)
}

// pkg adds a Go package of n source files in dir.
func (f *precedentFixture) pkg(dir string, n int) {
	for i := range n {
		f.file(fmt.Sprintf("%s/f%d.go", dir, i), precedentNamespace(dir, "go"), path.Base(dir), "go")
	}
}

func (f *precedentFixture) imports(from, to string) {
	f.edge("file:"+from+"/f0.go", precedentNamespace(to, "go"), types.RelationReferences)
}

// value declares a package-level Go var of type typ in dir's first file.
func (f *precedentFixture) value(dir, name, typ string) string {
	f.line++
	ns := precedentNamespace(dir, "go")
	id := ns + name + "."
	f.g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: name, Source: fmt.Sprintf("%s/f0.go:%d", dir, f.line),
		Attrs: map[string]string{attrNamespace: ns, attrLanguage: "go", attrSymbolKind: "Variable", AttrSignature: "var " + name + " " + typ}})
	return id
}

// depCase is the case a Go package in dir makes for a dep family row.
func depCase(dir string, imports ...string) types.Case {
	return types.Case{Node: precedentNamespace(dir, "go"), Source: dir, Imports: imports}
}

func fileCase(file string) types.Case { return types.Case{Node: "file:" + file, Source: file} }

func precedentRow(t *testing.T, rows []types.Precedent, family types.PrecedentFamily, scope types.PrecedentScope) types.Precedent {
	t.Helper()
	for _, r := range rows {
		if r.Family == family && r.Scope.Language == scope.Language && slices.Equal(r.Scope.Layers, scope.Layers) {
			return r
		}
	}
	require.FailNow(t, "no row", "%s %+v in %+v", family, scope, rows)
	return types.Precedent{}
}

var goScope = types.PrecedentScope{Language: "go"}

// TestPrecedentsPinThisTreesMeasuredShape reproduces what the miner counted on magus's own tree
// at 28ba1e69e: cmd -> internal 102/103, Err-named error values 85/87, in-package test files
// 846/854, with the real departures.
func TestPrecedentsPinThisTreesMeasuredShape(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
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
	magusErr := f.value("cmd/magus", "magusErr", "error")
	inspectErr := f.value("cmd/magus", "inspectErr", "error")
	f.value("cmd/magus", "ErrPattern", "*regexp.Regexp")

	for i := range 846 {
		dir := internal[i%len(internal)]
		f.file(fmt.Sprintf("%s/t%d_test.go", dir, i), precedentNamespace(dir, "go"), path.Base(dir), "go")
	}
	f.pkg("libs/gopherbuzz", 2)
	var external []types.Case
	for _, name := range []string{"conformance", "example", "fiber", "marshal_fuzz", "marshal", "parity", "parser", "session"} {
		file := "libs/gopherbuzz/" + name + "_test.go"
		f.file(file, precedentNamespace("libs/gopherbuzz_test", "go"), "buzz_test", "go")
		external = append(external, fileCase(file))
	}

	rows := f.g.Precedents(PrecedentOptions{Generated: map[string]bool{"cmd/magus/gen/f0.go": true}})

	cmdInternal := types.PrecedentScope{Layers: []string{"cmd", "internal"}}
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentDepDirection, Scope: cmdInternal, Key: types.PrecedentKey{From: "cmd", To: "internal"},
		Follow: 102, Cohort: 103, Share: 102.0 / 103, Established: true,
		Cited: []types.Case{
			depCase("cmd/magus", "internal/guard"), depCase("cmd/magus", "internal/p0"), depCase("cmd/magus", "internal/p1"),
		},
		Departures: []types.Case{depCase("internal/guard", "cmd/magus/gen")},
	}, precedentRow(t, rows, types.PrecedentDepDirection, cmdInternal))

	assert.Equal(t, types.Precedent{
		Family: types.PrecedentErrSentinelName, Scope: goScope, Key: types.PrecedentKey{Prefix: "err"},
		Follow: 85, Cohort: 87, Share: 85.0 / 87, Established: true,
		Cited: []types.Case{
			{Node: precedentNamespace("internal/guard", "go") + "ErrCase0.", Source: "internal/guard/f0.go:1"},
			{Node: precedentNamespace("internal/p0", "go") + "ErrCase1.", Source: "internal/p0/f0.go:2"},
			{Node: precedentNamespace("internal/p10", "go") + "ErrCase11.", Source: "internal/p10/f0.go:12"},
		},
		Departures: []types.Case{
			{Node: inspectErr, Source: "cmd/magus/f0.go:87"},
			{Node: magusErr, Source: "cmd/magus/f0.go:86"},
		},
	}, precedentRow(t, rows, types.PrecedentErrSentinelName, goScope))

	assert.Equal(t, types.Precedent{
		Family: types.PrecedentTestPackageName, Scope: goScope,
		Follow: 846, Cohort: 854, Share: 846.0 / 854, Established: true,
		Cited: []types.Case{
			fileCase("internal/guard/t0_test.go"), fileCase("internal/guard/t102_test.go"), fileCase("internal/guard/t204_test.go"),
		},
		Departures: external,
	}, precedentRow(t, rows, types.PrecedentTestPackageName, goScope))
}

func TestPrecedentsAreNotEstablishedBelowEitherGate(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 1)
	var cited []types.Case
	for i := range 4 {
		id := f.value("internal/a", fmt.Sprintf("ErrX%d", i), "error")
		cited = append(cited, types.Case{Node: id, Source: fmt.Sprintf("internal/a/f0.go:%d", i+1)})
	}
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentErrSentinelName, Scope: goScope, Key: types.PrecedentKey{Prefix: "err"},
		Follow: 4, Cohort: 4, Share: 1, Cited: cited[:3],
	}, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentErrSentinelName, goScope),
		"a cohort of four is under the minimum of five")

	f.value("internal/a", "ErrX4", "error")
	bad := f.value("internal/a", "badInput", "error")
	missing := f.value("internal/a", "notFound", "error")
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentErrSentinelName, Scope: goScope, Key: types.PrecedentKey{Prefix: "err"},
		Follow: 5, Cohort: 7, Share: 5.0 / 7, Cited: cited[:3],
		Departures: []types.Case{{Node: bad, Source: "internal/a/f0.go:6"}, {Node: missing, Source: "internal/a/f0.go:7"}},
	}, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentErrSentinelName, goScope),
		"5 of 7 is under 80%")
}

func TestPrecedentsSkipGeneratedAndTestCode(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 2)
	f.pkg("internal/a/gen", 1)
	var cited []types.Case
	for i := range 5 {
		id := f.value("internal/a", fmt.Sprintf("ErrX%d", i), "error")
		cited = append(cited, types.Case{Node: id, Source: fmt.Sprintf("internal/a/f0.go:%d", i+1)})
	}
	f.value("internal/a/gen", "generatedErr", "error")
	f.g.AddNode(types.KnowledgeNode{ID: precedentNamespace("internal/a", "go") + "testErr.", Kind: types.KindSymbol, Label: "testErr",
		Source: "internal/a/f_test.go:3", Attrs: map[string]string{
			attrNamespace: precedentNamespace("internal/a", "go"), attrLanguage: "go", attrSymbolKind: "Variable", AttrSignature: "var testErr error",
		}})
	f.file("internal/a/gen/x_test.go", precedentNamespace("internal/a/gen_test", "go"), "gen_test", "go")
	f.file("internal/a/a_test.go", precedentNamespace("internal/a", "go"), "a", "go")
	f.pkg("mocks/a", 1)
	f.imports("mocks/a", "internal/a")

	rows := f.g.Precedents(PrecedentOptions{Generated: map[string]bool{
		"internal/a/gen/f0.go": true, "internal/a/gen/x_test.go": true, "mocks/a/f0.go": true,
	}})
	assert.Equal(t, []types.Precedent{
		{
			Family: types.PrecedentErrSentinelName, Scope: goScope, Key: types.PrecedentKey{Prefix: "err"},
			Follow: 5, Cohort: 5, Share: 1, Established: true, Cited: cited[:3],
		},
		{
			Family: types.PrecedentTestPackageName, Scope: goScope,
			Follow: 1, Cohort: 1, Share: 1, Cited: []types.Case{fileCase("internal/a/a_test.go")},
		},
	}, rows, "a generated package's imports are its generator's, and generated or test values never count")
}

// TestPrecedentsJudgeTestPackagesOnlyWhereALanguagePackagesByDirectory: a language that makes
// each file its own module has no directory package for a test file to agree with.
func TestPrecedentsJudgeTestPackagesOnlyWhereALanguagePackagesByDirectory(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	for _, dir := range []string{"web/a", "web/b", "web/c"} {
		for _, name := range []string{"x.ts", "y.ts", "x.test.ts"} {
			file := dir + "/" + name
			f.file(file, precedentNamespace(file, "typescript"), name, "typescript")
		}
	}
	assert.Empty(t, f.g.Precedents(PrecedentOptions{}))
}

func TestPrecedentsFanoutIsADistributionOverImporters(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	var all []string
	for i := range 12 {
		dir := fmt.Sprintf("internal/p%d", i)
		all = append(all, dir)
		f.pkg(dir, 1)
	}
	slices.Sort(all)
	for i := range 20 {
		f.pkg(fmt.Sprintf("cmd/c%02d", i), 1)
		f.imports(fmt.Sprintf("cmd/c%02d", i), "internal/p0")
	}
	f.pkg("cmd/big", 1)
	for _, dir := range all {
		f.imports("cmd/big", dir)
	}
	// A package that never imports the layer is not in its cohort.
	f.pkg("cmd/none", 1)

	internal := types.PrecedentScope{Layers: []string{"internal"}}
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentDepFanout, Scope: internal, Key: types.PrecedentKey{MaxImports: 1},
		Follow: 20, Cohort: 21, Share: 20.0 / 21, Established: true,
		Cited: []types.Case{
			depCase("cmd/c00", "internal/p0"), depCase("cmd/c01", "internal/p0"), depCase("cmd/c02", "internal/p0"),
		},
		Departures: []types.Case{depCase("cmd/big", all...)},
	}, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentDepFanout, internal))
}
