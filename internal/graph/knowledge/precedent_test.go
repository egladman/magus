package knowledge

import (
	"fmt"
	"path"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
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

// file adds a file defining the namespace ns, labelled label. No dir node is added: a layer is
// read off the path.
func (f *precedentFixture) file(file, ns, label, lang string) {
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

// value declares `var name = newName()` of type typ in dir's first file. scip-go renders it
// `var name typ` either way; the call on its line is what tells it from state.
func (f *precedentFixture) value(dir, name, typ string) string {
	id := f.state(dir, name, typ)
	f.g.AddEdge(types.KnowledgeEdge{
		Source: "file:" + dir + "/f0.go", Target: precedentNamespace(dir, "go") + "new" + name + "().",
		Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted, Score: 1,
		Provenance: refProvenance(types.KnowledgeSymbolRef{Count: 1, Lines: []int{f.line}}),
	})
	return id
}

// state declares `var name typ` in dir's first file: no initializer, so nothing else is
// referenced on its line.
func (f *precedentFixture) state(dir, name, typ string) string {
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
// at 28ba1e69e: cmd -> internal 102/103 with its real departure. Err-named error values hold
// 85/85 once the two memo slots declared without an initializer leave the cohort, and
// in-package test files 846/846 once the eight external packages an import cycle forces do.
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
	f.state("cmd/magus", "magusErr", "error")
	f.state("cmd/magus", "inspectErr", "error")
	f.value("cmd/magus", "ErrPattern", "*regexp.Regexp")

	for i := range 846 {
		dir := internal[i%len(internal)]
		f.file(fmt.Sprintf("%s/t%d_test.go", dir, i), precedentNamespace(dir, "go"), path.Base(dir), "go")
	}
	// gopherbuzz/std imports gopherbuzz, so a test of gopherbuzz that needs std cannot be in
	// package buzz.
	f.pkg("libs/gopherbuzz", 2)
	f.pkg("libs/gopherbuzz/std", 1)
	f.imports("libs/gopherbuzz/std", "libs/gopherbuzz")
	for _, name := range []string{"conformance", "example", "fiber", "marshal_fuzz", "marshal", "parity", "parser", "session"} {
		file := "libs/gopherbuzz/" + name + "_test.go"
		f.file(file, precedentNamespace("libs/gopherbuzz_test", "go"), "buzz_test", "go")
		f.edge("file:"+file, precedentNamespace("libs/gopherbuzz", "go"), types.RelationReferences)
		f.edge("file:"+file, precedentNamespace("libs/gopherbuzz/std", "go"), types.RelationReferences)
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
		Follow: 85, Cohort: 85, Share: 1, Established: true,
		Cited: []types.Case{
			{Node: precedentNamespace("internal/guard", "go") + "ErrCase0.", Source: "internal/guard/f0.go:1"},
			{Node: precedentNamespace("internal/p0", "go") + "ErrCase1.", Source: "internal/p0/f0.go:2"},
			{Node: precedentNamespace("internal/p10", "go") + "ErrCase11.", Source: "internal/p10/f0.go:12"},
		},
	}, precedentRow(t, rows, types.PrecedentErrSentinelName, goScope))

	assert.Equal(t, types.Precedent{
		Family: types.PrecedentTestPackageName, Scope: goScope,
		Follow: 846, Cohort: 846, Share: 1, Established: true,
		Cited: []types.Case{
			fileCase("internal/guard/t0_test.go"), fileCase("internal/guard/t102_test.go"), fileCase("internal/guard/t204_test.go"),
		},
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

// A reference list the index capped may hide the call on a value's line, so a value in that
// file is judged as a sentinel rather than dropped as state.
func TestPrecedentsJudgeAValueWhoseLineACappedReferenceListMayHide(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 1)
	f.pkg("internal/b", 1)
	for i := range 5 {
		f.value("internal/a", fmt.Sprintf("ErrX%d", i), "error")
	}
	bare := f.state("internal/a", "lastErr", "error")
	hidden := f.state("internal/b", "cachedErr", "error")
	f.g.AddEdge(types.KnowledgeEdge{
		Source: "file:internal/b/f0.go", Target: "symbol:gomod std `fmt`/Errorf().", Relation: types.RelationReferences,
		Confidence: types.ConfidenceExtracted, Score: 1,
		Provenance: refProvenance(types.KnowledgeSymbolRef{Count: 3, Lines: []int{40, 41}}),
	})

	row := precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentErrSentinelName, goScope)
	// lastErr is state; cachedErr's line may hold the third Errorf. Whether the row clears the
	// gate and which followers it cites are other tests' business, so they are carried over.
	want := types.Precedent{
		Family: types.PrecedentErrSentinelName, Scope: goScope, Key: types.PrecedentKey{Prefix: "err"},
		Follow: 5, Cohort: 6, Share: 5.0 / 6, Established: row.Established, Cited: row.Cited,
		Departures: []types.Case{{Node: hidden, Source: "internal/b/f0.go:7"}},
	}
	assert.Equal(t, want, row)
	assert.NotContains(t, row.Departures, types.Case{Node: bare, Source: "internal/a/f0.go:6"})
}

// An external test package is forced only when a package it imports reaches back to its own;
// importing its own package directly is what every external test does.
func TestPrecedentsJudgeAnExternalTestPackageNoCycleForces(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 2)
	f.pkg("internal/leaf", 1)
	f.imports("internal/a", "internal/leaf")
	f.file("internal/a/b_test.go", precedentNamespace("internal/a", "go"), "a", "go")
	f.file("internal/a/a_test.go", precedentNamespace("internal/a_test", "go"), "a_test", "go")
	f.edge("file:internal/a/a_test.go", precedentNamespace("internal/a", "go"), types.RelationReferences)
	f.edge("file:internal/a/a_test.go", precedentNamespace("internal/leaf", "go"), types.RelationReferences)

	assert.Equal(t, types.Precedent{
		Family: types.PrecedentTestPackageName, Scope: goScope,
		Follow: 1, Cohort: 2, Share: 0.5, Cited: []types.Case{fileCase("internal/a/b_test.go")},
		Departures: []types.Case{fileCase("internal/a/a_test.go")},
	}, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentTestPackageName, goScope))
}

// TestPrecedentsMarshalByteStable: a test file defining namespaces of two languages is judged
// under whichever comes first, so that has to be the same one every run.
func TestPrecedentsMarshalByteStable(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 2)
	f.file("internal/a/a_test.go", precedentNamespace("internal/a", "go"), "a", "go")
	f.file("internal/a/a_test.go", precedentNamespace("internal/a/a_test.go", "typescript"), "a_test", "typescript")

	rows := f.g.Precedents(PrecedentOptions{})
	assert.Equal(t, []types.Precedent{{
		Family: types.PrecedentTestPackageName, Scope: goScope,
		Follow: 1, Cohort: 1, Share: 1, Cited: []types.Case{fileCase("internal/a/a_test.go")},
	}}, rows)
	first, err := json.Marshal(rows)
	require.NoError(t, err)
	for range 20 {
		again, err := json.Marshal(f.g.Precedents(PrecedentOptions{}))
		require.NoError(t, err)
		require.Equal(t, string(first), string(again))
	}
}

// TestPrecedentsTakeALayerWhoseTopDirectoryHoldsOnlyNestedProjects: libs has no dir or
// project node of its own, only the projects under it.
func TestPrecedentsTakeALayerWhoseTopDirectoryHoldsOnlyNestedProjects(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/x", 1)
	for _, dir := range []string{"libs/a", "libs/b"} {
		f.g.AddNode(types.KnowledgeNode{ID: "project:" + dir, Kind: types.KindProject, Label: dir, Source: dir})
		f.pkg(dir, 1)
		f.imports(dir, "internal/x")
	}

	layers := types.PrecedentScope{Layers: []string{"internal", "libs"}}
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentDepDirection, Scope: layers, Key: types.PrecedentKey{From: "libs", To: "internal"},
		Follow: 2, Cohort: 2, Share: 1,
		Cited: []types.Case{depCase("libs/a", "internal/x"), depCase("libs/b", "internal/x")},
	}, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentDepDirection, layers))
}

// A layer magus.project declares on a dir node wins over the first path segment, and a
// package no declaration covers keeps the segment.
func TestPrecedentsTakeTheDeclaredLayer(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	for _, dir := range []string{"cmd/app", "internal/handler/a", "internal/handler/b", "internal/store"} {
		f.pkg(dir, 1)
	}
	for _, dir := range []string{"internal/handler/a", "internal/handler/b"} {
		f.g.AddNode(types.KnowledgeNode{ID: "dir:" + dir, Kind: types.KindDir, Label: dir, Source: dir,
			Attrs: map[string]string{types.AttrLayer: "handler"}})
		f.imports("cmd/app", dir)
		f.imports(dir, "internal/store")
	}
	rows := f.g.Precedents(PrecedentOptions{})

	toHandler := types.PrecedentScope{Layers: []string{"cmd", "handler"}}
	assert.Equal(t, types.Precedent{
		Family: types.PrecedentDepDirection, Scope: toHandler, Key: types.PrecedentKey{From: "cmd", To: "handler"},
		Follow: 2, Cohort: 2, Share: 1,
		Cited: []types.Case{depCase("cmd/app", "internal/handler/a"), depCase("cmd/app", "internal/handler/b")},
	}, precedentRow(t, rows, types.PrecedentDepDirection, toHandler))
	fromHandler := types.PrecedentScope{Layers: []string{"handler", "internal"}}
	assert.Equal(t, types.PrecedentKey{From: "handler", To: "internal"},
		precedentRow(t, rows, types.PrecedentDepDirection, fromHandler).Key)
}

func TestPrecedentsNeverCountAGeneratedFileInAMixedPackage(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 1)
	f.pkg("cmd/app", 1)
	f.file("internal/a/zz_generated.go", precedentNamespace("internal/a", "go"), "a", "go")
	f.imports("cmd/app", "internal/a")
	f.edge("file:internal/a/zz_generated.go", precedentNamespace("cmd/app", "go"), types.RelationReferences)

	cmdInternal := types.PrecedentScope{Layers: []string{"cmd", "internal"}}
	internal := types.PrecedentScope{Layers: []string{"internal"}}
	assert.Equal(t, []types.Precedent{
		{
			Family: types.PrecedentDepDirection, Scope: cmdInternal, Key: types.PrecedentKey{From: "cmd", To: "internal"},
			Follow: 1, Cohort: 1, Share: 1, Cited: []types.Case{depCase("cmd/app", "internal/a")},
		},
		{
			Family: types.PrecedentDepFanout, Scope: internal, Key: types.PrecedentKey{MaxImports: 1},
			Follow: 1, Cohort: 1, Share: 1, Cited: []types.Case{depCase("cmd/app", "internal/a")},
		},
	}, f.g.Precedents(PrecedentOptions{Generated: map[string]bool{"internal/a/zz_generated.go": true}}),
		"internal/a's generated file importing cmd/app is its generator's decision, not a departure")
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

// TestPrecedentsFanoutIsADistributionOverImporters holds a cohort of 20, the smallest whose
// nearest-rank p95 sits below its max, so the one heavy importer departs.
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
	for i := range 19 {
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
		Follow: 19, Cohort: 20, Share: 19.0 / 20, Established: true,
		Cited: []types.Case{
			depCase("cmd/c00", "internal/p0"), depCase("cmd/c01", "internal/p0"), depCase("cmd/c02", "internal/p0"),
		},
		Departures: []types.Case{depCase("cmd/big", all...)},
	}, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentDepFanout, internal))
}

func TestPrecedentsFanoutIsGatedOnItsCohortAlone(t *testing.T) {
	t.Parallel()

	f := newPrecedentFixture()
	f.pkg("internal/a", 1)
	var cited []types.Case
	for i := range 4 {
		dir := fmt.Sprintf("cmd/c%d", i)
		f.pkg(dir, 1)
		f.imports(dir, "internal/a")
		cited = append(cited, depCase(dir, "internal/a"))
	}
	internal := types.PrecedentScope{Layers: []string{"internal"}}
	want := types.Precedent{
		Family: types.PrecedentDepFanout, Scope: internal, Key: types.PrecedentKey{MaxImports: 1},
		Follow: 4, Cohort: 4, Share: 1, Cited: cited[:3],
	}
	assert.Equal(t, want, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentDepFanout, internal),
		"four importers are under the minimum cohort of five")

	f.pkg("cmd/c4", 1)
	f.imports("cmd/c4", "internal/a")
	want.Follow, want.Cohort, want.Established = 5, 5, true
	assert.Equal(t, want, precedentRow(t, f.g.Precedents(PrecedentOptions{}), types.PrecedentDepFanout, internal))
}
