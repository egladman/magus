package knowledge

import (
	"maps"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// duplicationFixture builds functions whose call sets are the whole of what the lens reads.
// fn is name -> (source, end line, callees).
func duplicationFixture(fns map[string]struct {
	source  string
	end     string
	callees []string
}) *Graph {
	g := NewGraph()
	for name, f := range fns {
		g.AddNode(types.KnowledgeNode{
			ID: name, Kind: types.KindSymbol, Label: name, Source: f.source,
			Attrs: map[string]string{attrSymbolKind: "Function", attrDefEndLine: f.end},
		})
	}
	for name, f := range fns {
		for _, c := range f.callees {
			g.AddEdge(types.KnowledgeEdge{Source: name, Target: c, Relation: types.RelationCalls, Confidence: types.ConfidenceExtracted, Score: 1})
		}
	}
	return g
}

// fixtureOpts sets MinShared to 3 because the fixtures share three callees: these tests pin
// the filters, not the shipped defaults.
var fixtureOpts = types.DuplicationOptions{MinCallees: 3, MinShared: 3, MinScore: 0.6, MinSpanRatio: 0.5}

type fixtureFn = struct {
	source  string
	end     string
	callees []string
}

// TestDuplicatesFindsCopiesAndSkipsWhatIsNotOne pins the three filters that made the lens
// usable on a real repository: two functions calling the same rare helpers pair up, a
// wrapper around a function does not pair with it, and tests do not pair at all.
func TestDuplicatesFindsCopiesAndSkipsWhatIsNotOne(t *testing.T) {
	t.Parallel()

	g := duplicationFixture(map[string]fixtureFn{
		// The copy: same three rare callees, comparable length.
		"loadCfg":    {"a.go:10", "20", []string{"readFile", "parseYAML", "applyDefaults"}},
		"loadConfig": {"b.go:10", "22", []string{"readFile", "parseYAML", "applyDefaults"}},
		// A wrapper calling loadCfg shares its callees and is not a copy of it.
		"wrapper": {"c.go:1", "11", []string{"loadCfg", "readFile", "parseYAML", "applyDefaults"}},
		// Tests with an identical shape are left out by design.
		"TestA": {"x_test.go:1", "10", []string{"readFile", "parseYAML", "applyDefaults"}},
		"TestB": {"y_test.go:1", "10", []string{"readFile", "parseYAML", "applyDefaults"}},
		// Unrelated, so it anchors the rarity weights.
		"other": {"d.go:1", "10", []string{"send", "encode", "flush"}},
	})

	groups := g.Duplicates(fixtureOpts)
	require.Len(t, groups, 1, "only the genuine copy groups: got %+v", groups)
	require.Len(t, groups[0].Members, 2)
	assert.Equal(t, "loadCfg", groups[0].Members[0].ID, "members sorted by id, whatever order the graph held them in")
	assert.Equal(t, "loadConfig", groups[0].Members[1].ID)
	assert.Equal(t, []string{"applyDefaults", "parseYAML", "readFile"}, groups[0].Shared)
}

// TestDuplicatesReportsAFamilyAsOneGroup pins the refinement the real run called for: five
// RPC methods sharing one shape were ten pairs for one decision.
func TestDuplicatesReportsAFamilyAsOneGroup(t *testing.T) {
	t.Parallel()

	shape := []string{"dial", "send", "decode", "closeConn"}
	g := duplicationFixture(map[string]fixtureFn{
		"Shutdown":       {"c.go:1", "12", shape},
		"ReloadConfig":   {"c.go:20", "31", shape},
		"StopAll":        {"c.go:40", "51", shape},
		"AcquireService": {"c.go:60", "72", shape},
		"unrelated":      {"d.go:1", "12", []string{"render", "flush", "encode", "wrap"}},
	})

	groups := g.Duplicates(types.DuplicationOptions{MinCallees: 3, MinShared: 4, MinScore: 0.7, MinSpanRatio: 0.5})
	require.Len(t, groups, 1, "a family is one finding, not six pairs")
	assert.Len(t, groups[0].Members, 4)
	assert.Equal(t, []string{"closeConn", "decode", "dial", "send"}, groups[0].Shared)
}

// TestDuplicatesRespectsMinShared pins the floor the default config sets: three shared
// helpers score a perfect overlap and are usually two callers of a small toolkit.
func TestDuplicatesRespectsMinShared(t *testing.T) {
	t.Parallel()

	g := duplicationFixture(map[string]fixtureFn{
		"a":     {"a.go:1", "10", []string{"x", "y", "z"}},
		"b":     {"b.go:1", "10", []string{"x", "y", "z"}},
		"other": {"c.go:1", "10", []string{"p", "q", "r"}},
	})
	assert.NotEmpty(t, g.Duplicates(fixtureOpts), "three shared clears a floor of three")
	strict := fixtureOpts
	strict.MinShared = 4
	assert.Empty(t, g.Duplicates(strict), "and not a floor of four")
}

// TestDuplicatesRefusesPairsOfVeryDifferentLength pins the span ratio: a short function and
// a long one sharing a few helpers are not the same logic.
func TestDuplicatesRefusesPairsOfVeryDifferentLength(t *testing.T) {
	t.Parallel()

	g := duplicationFixture(map[string]fixtureFn{
		"short": {"a.go:1", "5", []string{"readFile", "parseYAML", "applyDefaults"}},
		"long":  {"b.go:1", "80", []string{"readFile", "parseYAML", "applyDefaults"}},
		"other": {"c.go:1", "10", []string{"send", "encode", "flush"}},
	})
	assert.Empty(t, g.Duplicates(fixtureOpts))
}

// TestDuplicatesIsDeterministic pins the regression the first real run showed: pair
// orientation came from map iteration, so the same report listed A<->B one run and B<->A
// the next. A checklist that reorders between runs cannot be worked through.
func TestDuplicatesIsDeterministic(t *testing.T) {
	t.Parallel()

	build := func() []types.DuplicationGroup {
		return duplicationFixture(map[string]fixtureFn{
			"m1": {"a.go:1", "10", []string{"x", "y", "z"}},
			"m2": {"b.go:1", "10", []string{"x", "y", "z"}},
			"m3": {"c.go:1", "10", []string{"x", "y", "z"}},
			"n":  {"d.go:1", "10", []string{"p", "q", "r"}},
		}).Duplicates(fixtureOpts)
	}
	first := build()
	require.NotEmpty(t, first)
	for range 20 {
		assert.Equal(t, first, build())
	}
}

func TestIsTestSourceCoversTheIndexedLanguages(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"pkg/x_test.go:12", "web/a.test.ts", "web/a.spec.tsx", "py/test_thing.py", "py/thing_test.py"} {
		assert.True(t, isTestSource(path), path)
	}
	for _, path := range []string{"pkg/x.go:12", "web/testing.ts", "py/tester.py", "contest.go"} {
		assert.False(t, isTestSource(path), path)
	}
}

// placementFixture is two copies, one in package a and one in package b, both calling
// helpers declared in package h. imports is importer -> imported, written the way the
// index records them: a file defining the importer's namespace references the imported one.
func placementFixture(t *testing.T, imports map[string][]string) types.DuplicationGroup {
	t.Helper()
	g := NewGraph()
	ns := func(pkg string) string { return "ns:" + pkg }
	for _, pkg := range []string{"a", "b", "h", "leaf"} {
		g.AddNode(types.KnowledgeNode{ID: ns(pkg), Kind: types.KindSymbol, Label: pkg, Source: pkg + "/x.go:1",
			Attrs: map[string]string{attrNamespace: ns(pkg)}})
		g.AddNode(types.KnowledgeNode{ID: "file:" + pkg + "/x.go", Kind: types.KindFile, Source: pkg + "/x.go"})
		g.AddEdge(types.KnowledgeEdge{Source: "file:" + pkg + "/x.go", Target: ns(pkg), Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1})
	}
	for from, tos := range imports {
		for _, to := range tos {
			g.AddEdge(types.KnowledgeEdge{Source: "file:" + from + "/x.go", Target: ns(to), Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted, Score: 1})
		}
	}
	fn := func(id, pkg, line string, callees ...string) {
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: id, Source: pkg + "/x.go:" + line,
			Attrs: map[string]string{attrSymbolKind: "Function", attrDefEndLine: line + "0", attrNamespace: ns(pkg)}})
		for _, c := range callees {
			g.AddEdge(types.KnowledgeEdge{Source: id, Target: c, Relation: types.RelationCalls, Confidence: types.ConfidenceExtracted, Score: 1})
		}
	}
	for _, h := range []string{"h1", "h2", "h3"} {
		fn(h, "h", "1")
	}
	fn("copyA", "a", "2", "h1", "h2", "h3")
	fn("copyB", "b", "2", "h1", "h2", "h3")
	fn("other", "leaf", "2", "x1", "x2", "x3")

	groups := g.Duplicates(fixtureOpts)
	require.Len(t, groups, 1)
	assert.Equal(t, []string{ns("a"), ns("b")}, groups[0].Packages)
	return groups[0]
}

// TestDuplicatesPlacesTheFoldWithoutAnImportCycle pins the question a fold has to answer
// before it is worth starting: where can the shared helper live that every copy can import.
func TestDuplicatesPlacesTheFoldWithoutAnImportCycle(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		imports   map[string][]string
		placement types.DuplicationPlacement
		home      string
	}{
		// b already imports a, so a hosts the helper and b adds nothing.
		{"a member that the other already imports", map[string][]string{"a": {"h"}, "b": {"a", "h"}},
			types.PlacementMember, "ns:a"},
		// a and b import each other's absence; neither member works, and h, which both import
		// and which the callees live in, does.
		{"a package every member imports", map[string][]string{"a": {"h", "b"}, "b": {"h", "a"}},
			types.PlacementDependency, "ns:h"},
		// h imports a, so any home calling h's helpers is one a cannot import.
		{"a callee that imports a member", map[string][]string{"a": {"b"}, "b": {"a"}, "h": {"a"}},
			types.PlacementBlocked, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			group := placementFixture(t, tc.imports)
			assert.Equal(t, tc.placement, group.Placement)
			assert.Equal(t, tc.home, group.Home)
		})
	}
}

// TestDuplicatesSaysUnknownWithoutANamespace pins the honest answer for an index that
// names no namespace: no imports can be read, so no home is vouched for.
func TestDuplicatesSaysUnknownWithoutANamespace(t *testing.T) {
	t.Parallel()

	groups := duplicationFixture(map[string]fixtureFn{
		"loadCfg":    {"a.go:10", "20", []string{"readFile", "parseYAML", "applyDefaults"}},
		"loadConfig": {"b.go:10", "22", []string{"readFile", "parseYAML", "applyDefaults"}},
		"other":      {"d.go:1", "10", []string{"send", "encode", "flush"}},
	}).Duplicates(fixtureOpts)
	require.Len(t, groups, 1)
	assert.Equal(t, types.PlacementUnknown, groups[0].Placement)
	assert.Empty(t, groups[0].Packages)
}

// TestDuplicatesSaysWhatAFoldWouldCost pins the evidence a reader decides a fold on: what
// each member does that the others do not, who reaches it, and what folding would save.
// Similarity alone ranked four three-line wrappers above a sixty-line copy.
func TestDuplicatesSaysWhatAFoldWouldCost(t *testing.T) {
	t.Parallel()

	g := NewGraph()
	edge := func(src, dst string, rel types.RelationID) {
		g.AddEdge(types.KnowledgeEdge{Source: src, Target: dst, Relation: rel, Confidence: types.ConfidenceExtracted, Score: 1})
	}
	for _, pkg := range []string{"a", "b"} {
		g.AddNode(types.KnowledgeNode{ID: "ns:" + pkg, Kind: types.KindSymbol, Source: pkg + "/x.go:1", Attrs: map[string]string{attrNamespace: "ns:" + pkg}})
		g.AddNode(types.KnowledgeNode{ID: "file:" + pkg + "/x.go", Kind: types.KindFile, Source: pkg + "/x.go"})
		edge("file:"+pkg+"/x.go", "ns:"+pkg, types.RelationDefines)
	}
	fn := func(id, label, source, end string, attrs map[string]string, callees ...string) {
		a := map[string]string{attrSymbolKind: "Function", attrDefEndLine: end, attrNamespace: "ns:a"}
		maps.Copy(a, attrs)
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: label, Source: source, Attrs: a})
		for _, c := range callees {
			edge(id, c, types.RelationCalls)
		}
	}
	// A copy that differs only in values, named from package b and covered by a test.
	fn("load1", "load1", "a/x.go:10", "40", map[string]string{attrTestRefs: "1"}, "read", "parse", "apply")
	fn("load2", "load2", "a/x.go:50", "80", nil, "read", "parse", "apply")
	edge("file:b/x.go", "load1", types.RelationReferences)
	edge("caller", "load1", types.RelationCalls)
	// Same-named methods on two types, each with a call of its own.
	fn("T#Export", "Export", "a/x.go:100", "130", nil, "stage", "copy", "clean", "gitOnly")
	fn("U#Export", "Export", "a/x.go:140", "170", nil, "stage", "copy", "clean", "hgOnly")
	// Wrappers too short for a helper to shorten anything.
	fn("wrapA", "wrapA", "a/x.go:200", "202", nil, "ptr", "patch", "check")
	fn("wrapB", "wrapB", "a/x.go:210", "212", nil, "ptr", "patch", "check")
	fn("other", "other", "a/x.go:300", "310", nil, "p", "q", "r")

	opts := types.DuplicationOptions{MinCallees: 3, MinShared: 3, MinScore: 0.3, MinSpanRatio: 0.5}
	groups := g.Duplicates(opts)
	require.Len(t, groups, 3)

	byFirst := map[string]types.DuplicationGroup{}
	for _, group := range groups {
		byFirst[group.Members[0].ID] = group
	}
	copies := byFirst["load1"]
	assert.Equal(t, types.ShapeParameterize, copies.Shape)
	assert.Equal(t, 31+31-31-2*foldedMemberLines, copies.Removable)
	assert.Equal(t, types.DuplicationSite{
		ID: "load1", Label: "load1", Source: "a/x.go:10", EndLine: 40,
		Distinct: []string{}, Callers: 1, ForeignFiles: 1, Tested: true,
	}, copies.Members[0])

	siblings := byFirst["T#Export"]
	assert.Equal(t, types.ShapeSiblingHelper, siblings.Shape)
	assert.True(t, siblings.Siblings)
	assert.Equal(t, []string{"gitOnly"}, siblings.Members[0].Distinct)

	assert.Equal(t, types.ShapeLeave, byFirst["wrapA"].Shape)
	assert.Equal(t, "wrapA", groups[2].Members[0].ID, "a fold that saves nothing ranks last")
}
