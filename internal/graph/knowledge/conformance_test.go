package knowledge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// conformanceNS is the key of package pkg ("" for the root) in module example.com/m.
func conformanceNS(pkg string) string { return "gomod example.com/m `example.com/m" + pkg + "`/" }

// conformanceSym is one Go symbol record defined at file:start..end in package pkg.
func conformanceSym(pkg, desc, file string, start, end, lines int, shape string, tokens int, refs ...string) types.KnowledgeSymbol {
	s := types.KnowledgeSymbol{
		Key: conformanceNS(pkg) + desc, Language: "go", Namespace: conformanceNS(pkg),
		Source: fmt.Sprintf("%s:%d", file, start), DefEndLine: end, SourceLines: lines,
		BodyShape: shape, BodyTokens: tokens, Defs: []string{file},
	}
	for _, r := range refs {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: r, Count: 1})
	}
	return s
}

// conformancePkg is package pkg's namespace record: every file defines it, and each importer
// references it.
func conformancePkg(pkg string, files []string, importers ...string) types.KnowledgeSymbol {
	s := types.KnowledgeSymbol{Key: conformanceNS(pkg), Language: "go", Namespace: conformanceNS(pkg), Source: files[0] + ":1", Defs: files}
	for _, r := range importers {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: r, Count: 1})
	}
	return s
}

// lensFixture is a module with a library pair under internal/, a command, a test, a
// platform-twin pair, a generated package, a declared output and an excluded tree.
func lensFixture() *Graph {
	return assembleLens(lensSymbols())
}

// lensSymbols are lensFixture's records, for a test that edits one before assembling.
func lensSymbols() []types.KnowledgeSymbol {
	const (
		a, b, gen, tool, third      = "/internal/a", "/internal/b", "/internal/a/gen", "/cmd/tool", "/third"
		aGo, aTest, linux, darwin   = "internal/a/a.go", "internal/a/a_test.go", "internal/a/fs_linux.go", "internal/a/fs_darwin.go"
		bGo, bString, genGo, mainGo = "internal/b/b.go", "internal/b/b_string.go", "internal/a/gen/gen.go", "cmd/tool/main.go"
	)
	return []types.KnowledgeSymbol{
		conformancePkg("", []string{"root.go"}),
		conformancePkg(a, []string{aGo, aTest, linux, darwin}, mainGo),
		conformancePkg(b, []string{bGo, bString}, aGo, mainGo),
		conformancePkg(gen, []string{genGo}, bGo),
		conformancePkg(tool, []string{mainGo}),
		conformancePkg(third, []string{"third/t.go"}),

		conformanceSym("", "Root().", "root.go", 3, 4, 10, "", 0),
		conformanceSym(a, "F().", aGo, 3, 12, 40, "S1", 90, mainGo),
		conformanceSym(a, "T#", aGo, 14, 16, 40, "", 0, aTest),
		conformanceSym(a, "T#M1().", aGo, 18, 19, 40, "", 0),
		conformanceSym(a, "T#M2().", aGo, 20, 21, 40, "", 0),
		conformanceSym(a, "T#M3().", aGo, 22, 23, 40, "", 0),
		conformanceSym(a, "linuxStat().", linux, 3, 5, 10, "S2", 85),
		conformanceSym(a, "darwinStat().", darwin, 3, 5, 10, "S2", 85),
		conformanceSym(a, "TestF().", aTest, 3, 12, 30, "S1", 90),
		conformanceSym(b, "G().", bGo, 3, 12, 20, "S1", 90, aGo),
		conformanceSym(b, "Str().", bString, 1, 3, 5, "S3", 90),
		conformanceSym(gen, "Gen().", genGo, 3, 12, 50, "S1", 90),
		conformanceSym(tool, "main().", mainGo, 5, 30, 60, "S4", 200),
		conformanceSym(tool, "Run().", mainGo, 32, 34, 60, "", 0),
		conformanceSym(third, "Third().", "third/t.go", 3, 12, 15, "S1", 90),
	}
}

// assembleLens is a graph of syms, the records lensSymbols returns after a test's edit.
func assembleLens(syms []types.KnowledgeSymbol) *Graph {
	return mergeAll([]Shard{assembleSymbols(".", syms, nil)})
}

func lensFixtureOptions() types.ConformanceOptions {
	limit := func(m types.ConformanceMetric, p types.ConformancePopulation, scope string, pct float64, anchor int) (types.ConformanceKey, types.ConformanceLimit) {
		return types.ConformanceKey{Metric: m, Population: p, Scope: scope}, types.ConformanceLimit{Percentile: pct, Anchor: anchor}
	}
	o := types.ConformanceOptions{
		Limits:    map[types.ConformanceKey]types.ConformanceLimit{},
		MinSample: 1,
		Step:      0.05,
		Exclude:   []string{"third/**"},
		Generated: map[string]bool{"internal/b/b_string.go": true},
		Ratchet: types.ConformanceRatchet{
			Schema: 1,
			Thresholds: []types.ConformanceThreshold{{
				ConformanceKey: types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopSource},
				Value:          30, N: 5, Percentile: 95, Why: "p95 at fixture", Line: 4,
			}},
			Known: []types.ConformanceKnown{
				{Metric: types.ConformanceCloneTokens, Items: []string{"internal/a/fs_linux.go#linuxStat", "internal/a/fs_darwin.go#darwinStat"}, Reason: "twins", Line: 12},
				{Metric: types.ConformanceFileLines, Items: []string{"internal/a/a.go", "internal/b/b.go"}, Reason: "split pending", Line: 10},
			},
		},
	}
	for _, l := range []struct {
		m      types.ConformanceMetric
		p      types.ConformancePopulation
		scope  string
		pct    float64
		anchor int
	}{
		{types.ConformanceFileLines, types.ConformancePopSource, "", 95, 100},
		{types.ConformanceFileLines, types.ConformancePopMain, "", 95, 100},
		{types.ConformanceFileLines, types.ConformancePopTest, "", 99, 100},
		{types.ConformanceFuncLines, types.ConformancePopSource, "", 95, 8},
		{types.ConformanceTypeMethods, types.ConformancePopSource, "", 95, 49},
		{types.ConformanceFileSymbols, types.ConformancePopSource, "", 95, 82},
		{types.ConformanceDepFanout, types.ConformancePopSource, "", 95, 62},
		{types.ConformanceDepFanout, types.ConformancePopMain, "cmd/tool", 0, 89},
		{types.ConformanceCloneTokens, types.ConformancePopSource, "", 50, 80},
		{types.ConformanceExportScope, types.ConformancePopSource, "", 0, 0},
		{types.ConformanceExportScope, types.ConformancePopMain, "", 0, 0},
	} {
		k, v := limit(l.m, l.p, l.scope, l.pct, l.anchor)
		o.Limits[k] = v
	}
	return o
}

func findConformanceRow(t *testing.T, out types.ConformanceOutput, m types.ConformanceMetric, p types.ConformancePopulation, scope string) types.ConformanceRow {
	t.Helper()
	for _, r := range out.Rows {
		if r.Key == (types.ConformanceKey{Metric: m, Population: p, Scope: scope}) {
			return r
		}
	}
	require.Failf(t, "no row", "%s %s %q", m, p, scope)
	return types.ConformanceRow{}
}

func TestConformanceMeasuresEveryMetricAndPopulation(t *testing.T) {
	out := lensFixture().Conformance(lensFixtureOptions())

	var keys []string
	for _, r := range out.Rows {
		keys = append(keys, fmt.Sprintf("%s %s %s", r.Key.Metric, r.Key.Population, r.Key.Scope))
	}
	assert.Equal(t, []string{
		"clone-tokens source ", "clone-tokens test ",
		"dep-fanout main cmd/tool", "dep-fanout source .", "dep-fanout source internal",
		"export-scope main ", "export-scope source ",
		"file-lines main ", "file-lines source ", "file-lines test ",
		"file-symbols main ", "file-symbols source ", "file-symbols test ",
		"func-lines main ", "func-lines source ", "func-lines test ", "main-logic main ",
		"mode-knobs source ", "shared-reach source ",
		"type-methods source ",
	}, keys, "generated, excluded and fixture files form no row and add no sample")
	assert.Equal(t, types.VerdictFound, out.Answer.Verdict)
	assert.Equal(t, 1, out.Excluded, "third/t.go")

	// The committed 30 is enforced, not the anchor 100 or the live p95 40; a.go is known.
	lines := findConformanceRow(t, out, types.ConformanceFileLines, types.ConformancePopSource, "")
	assert.Equal(t, types.ConformanceRow{
		Key: lines.Key, N: 5, P50: 10, P90: 40, P95: 40, P99: 40, Live: 40, Value: 30, Anchor: 100,
		Known: 1, Source: types.ConformanceFromRatchet,
	}, lines)
	assert.Equal(t, 60, findConformanceRow(t, out, types.ConformanceFileLines, types.ConformancePopMain, "").P99)
	assert.Equal(t, 30, findConformanceRow(t, out, types.ConformanceFileLines, types.ConformancePopTest, "").P99)

	funcs := findConformanceRow(t, out, types.ConformanceFuncLines, types.ConformancePopSource, "")
	wantFuncs := funcs
	wantFuncs.N = 8 // F, G, the twins, three methods and Root
	wantFuncs.Violations = []types.ConformanceItem{
		{ID: "internal/a/a.go#F", Source: "internal/a/a.go:3", Measure: 10},
		{ID: "internal/b/b.go#G", Source: "internal/b/b.go:3", Measure: 10},
	}
	assert.Equal(t, wantFuncs, funcs)
	assert.Equal(t, 26, findConformanceRow(t, out, types.ConformanceFuncLines, types.ConformancePopMain, "").P99)

	methods := findConformanceRow(t, out, types.ConformanceTypeMethods, types.ConformancePopSource, "")
	wantMethods := methods
	wantMethods.N = 1
	wantMethods.P50 = 3 // T declares M1, M2 and M3
	assert.Equal(t, wantMethods, methods)

	assert.Equal(t, 2, findConformanceRow(t, out, types.ConformanceFileSymbols, types.ConformancePopSource, "").P99, "a.go: F and T")
	assert.Equal(t, 2, findConformanceRow(t, out, types.ConformanceFileSymbols, types.ConformancePopMain, "").P50, "main and Run")

	// A package is keyed by its own directory when that directory has a value, else its layer.
	tool := findConformanceRow(t, out, types.ConformanceDepFanout, types.ConformancePopMain, "cmd/tool")
	assert.Equal(t, []int{1, 2, 89}, []int{tool.N, tool.P50, tool.Value})
	internal := findConformanceRow(t, out, types.ConformanceDepFanout, types.ConformancePopSource, "internal")
	assert.Equal(t, []int{2, 1, 62}, []int{internal.N, internal.P99, internal.Value},
		"b importing the generated package counts; what gen imports would not")

	clones := findConformanceRow(t, out, types.ConformanceCloneTokens, types.ConformancePopSource, "")
	wantClones := clones
	wantClones.N = 5 // main's body counts with source
	wantClones.Violations = []types.ConformanceItem{
		{ID: "internal/a/a.go#F, internal/b/b.go#G", Source: "internal/a/a.go:3", Measure: 90},
	}
	assert.Equal(t, wantClones, clones)
	testClones := findConformanceRow(t, out, types.ConformanceCloneTokens, types.ConformancePopTest, "")
	assert.Equal(t, 1, testClones.N)
	assert.Empty(t, testClones.Violations, "no value is configured for test clones")
	assert.Empty(t, testClones.Source)

	assert.Equal(t, []types.ConformanceItem{{ID: "internal/a/a.go#T", Source: "internal/a/a.go:14", Measure: 1}},
		findConformanceRow(t, out, types.ConformanceExportScope, types.ConformancePopSource, "").Violations,
		"F is used from cmd/tool; T only from its own test")
	assert.Equal(t, []types.ConformanceItem{{ID: "cmd/tool/main.go#Run", Source: "cmd/tool/main.go:32", Measure: 1}},
		findConformanceRow(t, out, types.ConformanceExportScope, types.ConformancePopMain, "").Violations)

	assert.Equal(t, []types.ConformanceKnown{
		{Metric: types.ConformanceFileLines, Items: []string{"internal/b/b.go"}, Reason: "split pending", Line: 10},
		{Metric: types.ConformanceCloneTokens, Items: []string{"internal/a/fs_linux.go#linuxStat", "internal/a/fs_darwin.go#darwinStat"}, Reason: "twins", Line: 12},
	}, out.Stale, "b.go is under 30 now, and twins never form a group")
}

func TestConformanceKnownCloneGroup(t *testing.T) {
	g := lensFixture()
	o := lensFixtureOptions()
	o.Ratchet.Known = []types.ConformanceKnown{{Metric: types.ConformanceCloneTokens, Items: []string{"internal/a/a.go#F", "internal/b/b.go#G"}, Reason: "one home pending"}}

	clones := findConformanceRow(t, g.Conformance(o), types.ConformanceCloneTokens, types.ConformancePopSource, "")
	assert.Equal(t, 1, clones.Known)
	assert.Empty(t, clones.Violations)
	assert.Empty(t, g.Conformance(o).Stale)

	// A group's members must all be known: one entry naming only F leaves the group judged.
	o.Ratchet.Known[0].Items = []string{"internal/a/a.go#F"}
	clones = findConformanceRow(t, g.Conformance(o), types.ConformanceCloneTokens, types.ConformancePopSource, "")
	assert.Zero(t, clones.Known)
	assert.Len(t, clones.Violations, 1)
}

// A known item is matched by ID alone: its measure is never compared with anything, so
// growth past its value is not a violation.
func TestConformanceKnownItemIgnoresItsMeasure(t *testing.T) {
	key := types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopSource}
	o := types.ConformanceOptions{Ratchet: types.ConformanceRatchet{Schema: 1,
		Thresholds: []types.ConformanceThreshold{{ConformanceKey: key, Value: 30}},
		Known:      []types.ConformanceKnown{{Metric: types.ConformanceFileLines, Items: []string{"pkg/f00.go"}, Reason: "split pending"}},
	}}
	out := conformanceSizedFiles(300, 10).Conformance(o)
	row := findConformanceRow(t, out, types.ConformanceFileLines, types.ConformancePopSource, "")
	assert.Equal(t, 1, row.Known, "f00.go at 10x the value")
	assert.Empty(t, row.Violations)
	assert.Empty(t, out.Stale)
}

// A renamed or moved item with no recorded shape matches no sample: its old ID is stale,
// and its new ID, if still past the value, is a violation.
func TestConformanceKnownIDMatchingNoSampleIsStale(t *testing.T) {
	key := types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopSource}
	known := types.ConformanceKnown{Metric: types.ConformanceFileLines, Items: []string{"pkg/old.go"}, Reason: "split pending", Line: 3}
	o := types.ConformanceOptions{Ratchet: types.ConformanceRatchet{Schema: 1,
		Thresholds: []types.ConformanceThreshold{{ConformanceKey: key, Value: 30}},
		Known:      []types.ConformanceKnown{known},
	}}
	out := conformanceSizedFiles(300, 10).Conformance(o)
	assert.Equal(t, []types.ConformanceKnown{known}, out.Stale)
	assert.Equal(t, []types.ConformanceItem{{ID: "pkg/f00.go", Source: "pkg/f00.go:1", Measure: 300}},
		findConformanceRow(t, out, types.ConformanceFileLines, types.ConformancePopSource, "").Violations)
}

// carryOptions enforce 10 func lines and 30 file lines on source, with one known entry of
// metric m listing items.
func carryOptions(m types.ConformanceMetric, items ...string) types.ConformanceOptions {
	return types.ConformanceOptions{Ratchet: types.ConformanceRatchet{Schema: 1,
		Thresholds: []types.ConformanceThreshold{
			{ConformanceKey: types.ConformanceKey{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}, Value: 10},
			{ConformanceKey: types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopSource}, Value: 30},
		},
		Known: []types.ConformanceKnown{{Metric: m, Items: items, Reason: "split pending", Line: 3}},
	}}
}

// recordShapes fills o's known entries with the shapes out reports, as format does.
func recordShapes(o *types.ConformanceOptions, out types.ConformanceOutput) {
	for i, k := range o.Ratchet.Known {
		shapes := map[string]string{}
		for _, item := range k.Items {
			if s := out.Shapes[item]; s != "" {
				shapes[item] = s
			}
		}
		o.Ratchet.Known[i].Shapes = shapes
	}
}

func TestConformanceCarriesARenamedKnownFunction(t *testing.T) {
	long := func(name string) types.KnowledgeSymbol {
		return conformanceSym("/pkg", name+"().", "pkg/a.go", 1, 20, 20, "SA", 100)
	}
	other := conformanceSym("/pkg", "Other().", "pkg/b.go", 1, 20, 20, "SB", 100)
	o := carryOptions(types.ConformanceFuncLines, "pkg/a.go#Long", "pkg/b.go#Other")
	before := assembleLens([]types.KnowledgeSymbol{long("Long"), other}).Conformance(o)
	require.Equal(t, map[string]string{"pkg/a.go#Long": "SA", "pkg/b.go#Other": "SB"}, before.Shapes)
	recordShapes(&o, before)

	out := assembleLens([]types.KnowledgeSymbol{long("Renamed"), other}).Conformance(o)
	row := findConformanceRow(t, out, types.ConformanceFuncLines, types.ConformancePopSource, "")
	assert.Equal(t, 2, row.Known, "Renamed is Long")
	assert.Empty(t, row.Violations)
	out.Rows = nil
	assert.Equal(t, types.ConformanceOutput{
		Carried: []types.ConformanceCarry{{Metric: types.ConformanceFuncLines, From: "pkg/a.go#Long", To: []string{"pkg/a.go#Renamed"}}},
		Shapes:  map[string]string{"pkg/a.go#Renamed": "SA", "pkg/b.go#Other": "SB"},
		Answer:  types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}, out, "shapes under the carried ID")
	assert.Equal(t, []string{"pkg/a.go#Long", "pkg/b.go#Other"}, o.Ratchet.Known[0].Items, "the record is read, never edited")
}

func TestConformanceCarriesAnItemListedTwice(t *testing.T) {
	long := func(name string) types.KnowledgeSymbol {
		return conformanceSym("/pkg", name+"().", "pkg/a.go", 1, 20, 20, "SA", 100)
	}
	o := carryOptions(types.ConformanceFuncLines, "pkg/a.go#Long")
	o.Ratchet.Known[0].Shapes = map[string]string{"pkg/a.go#Long": "SA"}
	o.Ratchet.Known = append(o.Ratchet.Known, types.ConformanceKnown{
		Metric: types.ConformanceFuncLines, Items: []string{"pkg/a.go#Long"}, Reason: "listed again", Line: 4,
	})

	out := assembleLens([]types.KnowledgeSymbol{long("Renamed")}).Conformance(o)
	assert.Equal(t, []types.ConformanceCarry{{Metric: types.ConformanceFuncLines, From: "pkg/a.go#Long", To: []string{"pkg/a.go#Renamed"}}}, out.Carried)
	assert.Equal(t, 1, findConformanceRow(t, out, types.ConformanceFuncLines, types.ConformancePopSource, "").Known)
}

func TestConformanceCarriesAMovedKnownFile(t *testing.T) {
	file := func(path string) []types.KnowledgeSymbol {
		a := conformanceSym("/pkg", "A().", path, 1, 2, 300, "", 0)
		b := conformanceSym("/pkg", "B().", path, 4, 5, 300, "", 0)
		a.BodyDigest, b.BodyDigest = "d1", "d2"
		return []types.KnowledgeSymbol{a, b}
	}
	small := conformanceSym("/pkg", "C().", "pkg/small.go", 1, 2, 10, "", 0)
	small.BodyDigest = "d3"
	o := carryOptions(types.ConformanceFileLines, "pkg/old.go")
	before := assembleLens(append(file("pkg/old.go"), small)).Conformance(o)
	recordShapes(&o, before)
	require.NotEmpty(t, o.Ratchet.Known[0].Shapes["pkg/old.go"])

	out := assembleLens(append(file("pkg/new.go"), small)).Conformance(o)
	row := findConformanceRow(t, out, types.ConformanceFileLines, types.ConformancePopSource, "")
	assert.Equal(t, 1, row.Known)
	assert.Empty(t, row.Violations)
	assert.Empty(t, out.Stale)
	assert.Equal(t, []types.ConformanceCarry{{Metric: types.ConformanceFileLines, From: "pkg/old.go", To: []string{"pkg/new.go"}}}, out.Carried)

	// A file that changed a symbol on the way did not move whole.
	moved := file("pkg/new.go")
	moved[1].BodyDigest = "d2-edited"
	out = assembleLens(append(moved, small)).Conformance(o)
	assert.Empty(t, out.Carried)
	assert.Len(t, out.Stale, 1)
}

func TestConformanceShapeTwinsCarryNothing(t *testing.T) {
	renamed := conformanceSym("/pkg", "Renamed().", "pkg/a.go", 1, 20, 20, "SA", 100)
	twin := conformanceSym("/pkg", "Twin().", "pkg/b.go", 1, 20, 20, "SA", 100)
	o := carryOptions(types.ConformanceFuncLines, "pkg/a.go#Long")
	o.Ratchet.Known[0].Shapes = map[string]string{"pkg/a.go#Long": "SA"}

	out := assembleLens([]types.KnowledgeSymbol{renamed, twin}).Conformance(o)
	assert.Len(t, findConformanceRow(t, out, types.ConformanceFuncLines, types.ConformancePopSource, "").Violations, 2)
	out.Rows = nil
	assert.Equal(t, types.ConformanceOutput{
		Stale: []types.ConformanceKnown{
			{Metric: types.ConformanceFuncLines, Items: []string{"pkg/a.go#Long"}, Reason: "split pending", Line: 3},
		},
		Ambiguous: []types.ConformanceCarry{
			{Metric: types.ConformanceFuncLines, From: "pkg/a.go#Long", To: []string{"pkg/a.go#Renamed", "pkg/b.go#Twin"}},
		},
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}, out)

	// One candidate that two known items claim is no evidence for either.
	o = carryOptions(types.ConformanceFuncLines, "pkg/a.go#Long", "pkg/c.go#Gone")
	o.Ratchet.Known[0].Shapes = map[string]string{"pkg/a.go#Long": "SA", "pkg/c.go#Gone": "SA"}
	out = assembleLens([]types.KnowledgeSymbol{renamed}).Conformance(o)
	assert.Empty(t, out.Carried)
	assert.Equal(t, []types.ConformanceCarry{
		{Metric: types.ConformanceFuncLines, From: "pkg/a.go#Long", To: []string{"pkg/a.go#Renamed"}},
		{Metric: types.ConformanceFuncLines, From: "pkg/c.go#Gone", To: []string{"pkg/a.go#Renamed"}},
	}, out.Ambiguous)
}

func TestConformanceRenamedCloneMemberKeepsItsGroupKnown(t *testing.T) {
	o := lensFixtureOptions()
	o.Ratchet.Known = []types.ConformanceKnown{{Metric: types.ConformanceCloneTokens, Items: []string{"internal/a/a.go#F", "internal/b/b.go#G"}, Reason: "one home pending", Line: 12}}
	recordShapes(&o, lensFixture().Conformance(o))

	syms := lensSymbols()
	for i := range syms {
		if syms[i].Key == conformanceNS("/internal/b")+"G()." {
			syms[i].Key = conformanceNS("/internal/b") + "G2()."
		}
	}
	out := assembleLens(syms).Conformance(o)
	clones := findConformanceRow(t, out, types.ConformanceCloneTokens, types.ConformancePopSource, "")
	assert.Equal(t, 1, clones.Known)
	assert.Empty(t, clones.Violations)
	assert.Empty(t, out.Stale)
	assert.Equal(t, []types.ConformanceCarry{
		{Metric: types.ConformanceCloneTokens, From: "internal/b/b.go#G", To: []string{"internal/b/b.go#G2"}},
	}, out.Carried, "F is listed, so G2 is the one unlisted member sharing the shape")
}

// A known export goes stale when a caller is added from another package, a change to a
// file the entry does not name.
func TestConformanceKnownExportGoesStaleOnAnOutsideCaller(t *testing.T) {
	known := types.ConformanceKnown{Metric: types.ConformanceExportScope, Items: []string{"internal/a/a.go#T"}, Reason: "stacked change", Line: 20}
	o := lensFixtureOptions()
	o.Ratchet.Known = []types.ConformanceKnown{known}

	before := lensFixture().Conformance(o)
	assert.Equal(t, 1, findConformanceRow(t, before, types.ConformanceExportScope, types.ConformancePopSource, "").Known)
	assert.Empty(t, before.Stale)

	syms := lensSymbols()
	for i := range syms {
		if syms[i].Key == conformanceNS("/internal/a")+"T#" {
			syms[i].Refs = append(syms[i].Refs, types.KnowledgeSymbolRef{Path: "cmd/tool/main.go", Count: 1})
		}
	}
	after := assembleLens(syms).Conformance(o)
	assert.Zero(t, findConformanceRow(t, after, types.ConformanceExportScope, types.ConformancePopSource, "").Known)
	assert.Equal(t, []types.ConformanceKnown{known}, after.Stale, "only cmd/tool/main.go changed")
}

// A new copy joining a known clone group makes the whole group one violation, located at
// its first member, a file the new copy did not touch.
func TestConformanceNewCopyFailsAKnownCloneGroupAtItsFirstMember(t *testing.T) {
	o := lensFixtureOptions()
	o.Ratchet.Known = []types.ConformanceKnown{{Metric: types.ConformanceCloneTokens, Items: []string{"internal/a/a.go#F", "internal/b/b.go#G"}, Reason: "one home pending"}}

	syms := append(lensSymbols(), conformanceSym("/internal/b", "H().", "internal/b/b.go", 14, 23, 20, "S1", 90))
	out := assembleLens(syms).Conformance(o)
	clones := findConformanceRow(t, out, types.ConformanceCloneTokens, types.ConformancePopSource, "")
	assert.Zero(t, clones.Known)
	assert.Equal(t, []types.ConformanceItem{
		{ID: "internal/a/a.go#F, internal/b/b.go#G, internal/b/b.go#H", Source: "internal/a/a.go:3", Measure: 90},
	}, clones.Violations, "H was added to b.go; the finding points at a.go")
	assert.Empty(t, out.Stale, "the entry still shares two members with the group")
}

func TestCloneGroupsSkipsPlatformTwins(t *testing.T) {
	groups := cloneGroups(lensFixture(), lensFixtureOptions())
	assert.Equal(t, [][]types.ConformanceItem{{
		{ID: "internal/a/a.go#F", Source: "internal/a/a.go:3", Measure: 90},
		{ID: "internal/b/b.go#G", Source: "internal/b/b.go:3", Measure: 90},
	}}, groups, "the S2 twins form none; test, generated and excluded copies of S1 join none")
}

func TestExportScope(t *testing.T) {
	assert.Equal(t, []types.ConformanceItem{
		{ID: "cmd/tool/main.go#Run", Source: "cmd/tool/main.go:32", Measure: 1},
		{ID: "internal/a/a.go#T", Source: "internal/a/a.go:14", Measure: 1},
	}, exportScope(lensFixture(), lensFixtureOptions()), "methods, the root package and test files are out of scope")
}

func TestConformanceSamplesDropGeneratedAndExcluded(t *testing.T) {
	samples := conformanceSamples(lensFixture(), lensFixtureOptions())
	for key, items := range samples {
		for _, it := range items {
			assert.NotContains(t, []string{"internal/b/b_string.go", "internal/a/gen/gen.go", "third/t.go"}, itemFile(it.ID), "%v", key)
		}
	}
}

// A file carrying the generated-code header is generated wherever it sits and whatever
// declares it.
func TestConformanceSamplesDropAHeaderMarkedFile(t *testing.T) {
	hand := conformanceSym("/pkg", "Hand().", "pkg/hand.go", 1, 1, 40, "", 0)
	made := conformanceSym("/pkg", "Made().", "pkg/made.go", 1, 1, 40, "", 0)
	made.SourceGenerated = true
	g := mergeAll([]Shard{assembleSymbols(".", []types.KnowledgeSymbol{hand, made}, nil)})

	key := types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopSource}
	var files []string
	for _, it := range conformanceSamples(g, types.ConformanceOptions{})[key] {
		files = append(files, itemFile(it.ID))
	}
	assert.Equal(t, []string{"pkg/hand.go"}, files)
}

// conformanceSizedFiles is a graph of one package whose files have the given line counts.
func conformanceSizedFiles(lines ...int) *Graph {
	var syms []types.KnowledgeSymbol
	for i, n := range lines {
		syms = append(syms, conformanceSym("/pkg", fmt.Sprintf("F%d().", i), fmt.Sprintf("pkg/f%02d.go", i), 1, 1, n, "", 0))
	}
	return mergeAll([]Shard{assembleSymbols(".", syms, nil)})
}

// A proposal is the live percentile, offered only when a large enough sample sits a full
// step under the enforced value. A live value looser than it yields violations instead.
func TestConformanceProposalRule(t *testing.T) {
	var lines []int
	for i := 1; i <= 30; i++ {
		lines = append(lines, i)
	}
	g := conformanceSizedFiles(lines...)
	key := types.ConformanceKey{Metric: types.ConformanceFileLines, Population: types.ConformancePopSource}
	for _, tc := range []struct {
		name               string
		committed          int
		minSample          int
		propose, violating int
		src                types.ConformanceSource
	}{
		{name: "anchor far above the live p95", minSample: 30, propose: 29, src: types.ConformanceFromAnchor},
		{name: "too few samples", minSample: 31, src: types.ConformanceFromAnchor},
		{name: "within one step", committed: 30, minSample: 30, src: types.ConformanceFromRatchet},
		{name: "live looser than the commit", committed: 25, minSample: 30, violating: 5, src: types.ConformanceFromRatchet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := types.ConformanceOptions{
				Limits:    map[types.ConformanceKey]types.ConformanceLimit{key: {Percentile: 95, Anchor: 100}},
				MinSample: tc.minSample, Step: 0.05,
			}
			if tc.committed > 0 {
				o.Ratchet = types.ConformanceRatchet{Schema: 1, Thresholds: []types.ConformanceThreshold{{ConformanceKey: key, Value: tc.committed}}}
			}
			row := findConformanceRow(t, g.Conformance(o), types.ConformanceFileLines, types.ConformancePopSource, "")
			assert.Len(t, row.Violations, tc.violating)
			wantRow := row
			wantRow.Live, wantRow.Propose, wantRow.Source = 29, tc.propose, tc.src
			assert.Equal(t, wantRow, row)
		})
	}
}

func TestConformanceIsDeterministic(t *testing.T) {
	g, o := lensFixture(), lensFixtureOptions()
	first := g.Conformance(o)
	for range 10 {
		assert.Equal(t, first, lensFixture().Conformance(o))
	}
}

func TestConformanceWithoutSymbolsIsUnknown(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: fileID("a.go"), Kind: types.KindFile, Source: "a.go"})
	out := g.Conformance(lensFixtureOptions())
	assert.Equal(t, types.VerdictUnknown, out.Answer.Verdict)
	assert.Empty(t, out.Rows)
}

// nearestRank's float form agrees with the integer form precedent.go's depFanout inlines.
func TestNearestRankMatchesTheIntegerForm(t *testing.T) {
	for n := 1; n <= 500; n++ {
		sorted := make([]int, n)
		for i := range sorted {
			sorted[i] = i
		}
		require.Equal(t, sorted[(95*n+99)/100-1], nearestRank(sorted, 95), "n=%d", n)
	}
	assert.Zero(t, nearestRank(nil, 95))
	assert.Equal(t, 1, nearestRank([]int{1, 2, 3}, 0))
	assert.Equal(t, 3, nearestRank([]int{1, 2, 3}, 100))
}

func TestPlatformTwin(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"x/fs_linux.go", "x/fs_darwin.go", true},
		{"x/fs_linux_amd64.go", "x/fs_darwin_arm64.go", true},
		{"x/fs_unix.go", "x/fs_other.go", true},
		{"x/fs_linux_test.go", "x/fs_windows_test.go", true},
		{"x/fs.go", "x/fs_linux.go", false},
		{"x/fs_linux.go", "x/fs_linux.go", false},
		{"x/fs_linux.go", "y/fs_darwin.go", false},
		{"x/fs_linux.go", "x/fs_darwin_test.go", false},
		{"x/fs_linux.go", "x/io_darwin.go", false},
		{"x/linux.go", "x/darwin.go", false},
		{"x/fs_linux.go", "x/fs_darwin.ts", false},
	} {
		assert.Equal(t, tc.want, platformTwin(tc.a, tc.b), "%s %s", tc.a, tc.b)
	}
}

func TestModuleRoot(t *testing.T) {
	assert.True(t, moduleRoot(symbolID(conformanceNS(""))))
	assert.False(t, moduleRoot(symbolID(conformanceNS("/internal/a"))))
	assert.False(t, moduleRoot(symbolID(conformanceNS("")+"Root().")))
}

// callSeqSamples feeds one call-sequence row per population: each callable's longest run
// shared with another, judged against the anchor like any metric.
func TestConformanceCallSequenceRow(t *testing.T) {
	s, _, _ := pipelinePair(8, "internal/b/b.go")
	g := s.graph()
	o := callSeqOptions()
	o.MinSample, o.Step = 1, 0.05

	row := findConformanceRow(t, g.Conformance(o), types.ConformanceCallSequence, types.ConformancePopSource, "")
	assert.Equal(t, []int{3, 8, 7}, []int{row.N, row.Live, row.Value},
		"A and B share eight steps, C shares one; the steps call nothing and the wrapper is no step")
	assert.Equal(t, []types.ConformanceItem{
		{ID: "internal/a/a.go#A", Source: "internal/a/a.go:1", Measure: 8},
		{ID: "internal/b/b.go#B", Source: "internal/b/b.go:1", Measure: 8},
	}, row.Violations)

	o.Ratchet = types.ConformanceRatchet{Schema: 1, Known: []types.ConformanceKnown{
		{Metric: types.ConformanceCallSequence, Items: []string{"internal/b/b.go#B", "internal/c/c.go#C"}, Reason: "fold pending", Line: 3},
	}}
	out := g.Conformance(o)
	row = findConformanceRow(t, out, types.ConformanceCallSequence, types.ConformancePopSource, "")
	row.P50, row.P90, row.P95, row.P99 = 0, 0, 0, 0
	assert.Equal(t, types.ConformanceRow{
		Key: types.ConformanceKey{Metric: types.ConformanceCallSequence, Population: types.ConformancePopSource},
		N:   3, Live: 8, Value: 7, Anchor: 7, Known: 1, Source: types.ConformanceFromAnchor,
		Violations: []types.ConformanceItem{{ID: "internal/a/a.go#A", Source: "internal/a/a.go:1", Measure: 8}},
	}, row)
	assert.Equal(t, []types.ConformanceKnown{
		{Metric: types.ConformanceCallSequence, Items: []string{"internal/c/c.go#C"}, Reason: "fold pending", Line: 3},
	}, out.Stale, "C is under the value")
}

// boundarySymbols is a command beside a library, an interface, a store writing through
// internal/file, and a package that exports a path but keeps no state.
func boundarySymbols() []types.KnowledgeSymbol {
	const (
		lib, ifc, file, store, plain, tool = "/internal/lib", "/internal/ifc", "/internal/file", "/internal/store", "/internal/plain", "/cmd/tool"
		libGo, ifcGo, fileGo, storeGo      = "internal/lib/lib.go", "internal/ifc/ifc.go", "internal/file/file.go", "internal/store/store.go"
		storeTest, plainGo, mainGo         = "internal/store/store_test.go", "internal/plain/plain.go", "cmd/tool/main.go"
	)
	fn := func(pkg, name, file, kind, sig string, line, tokens int, refs ...string) types.KnowledgeSymbol {
		s := conformanceSym(pkg, name, file, line, line+5, 40, "S"+name, tokens, refs...)
		s.Label, s.SymbolKind, s.Signature = strings.TrimRight(name, "().#"), kind, sig
		return s
	}
	delegate := fn(tool, "delegate().", mainGo, "Function", "func delegate() error", 20, 150)
	delegate.Calls = []types.KnowledgeSymbolCall{{Key: conformanceNS(lib) + "Do().", Count: 1}}
	return []types.KnowledgeSymbol{
		conformancePkg(lib, []string{libGo}, mainGo),
		conformancePkg(ifc, []string{ifcGo}, mainGo),
		conformancePkg(file, []string{fileGo}, storeGo),
		conformancePkg(store, []string{storeGo, storeTest}, mainGo, libGo),
		conformancePkg(plain, []string{plainGo}, mainGo),
		conformancePkg(tool, []string{mainGo}),

		fn(lib, "Do().", libGo, "Function", "func Do() error", 3, 40, mainGo),
		fn(lib, "Name().", libGo, "Function", "func Name() string", 10, 40, mainGo),
		fn(ifc, "Store#", ifcGo, "Interface", "type Store interface", 3, 20, mainGo),
		fn(file, "WriteFileAtomic().", fileGo, "Function", "func WriteFileAtomic(path string) error", 3, 40, storeGo),
		fn(store, "LogPath.", storeGo, "Constant", "const LogPath untyped string = \"log.jsonl\"", 3, 0, mainGo, libGo, storeTest),
		fn(store, "AnchorFile.", storeGo, "Constant", "const AnchorFile Kind = \"file\"", 5, 0, libGo),
		fn(store, "IsDir().", storeGo, "Function", "func IsDir(p string) bool", 7, 40, libGo),
		fn(store, "StoreDir().", storeGo, "Function", "func StoreDir() string", 10, 40, storeTest),
		fn(store, "Open().", storeGo, "Function", "func Open() error", 20, 40, mainGo),
		fn(plain, "QueueDir.", plainGo, "Constant", "const QueueDir untyped string = \"queue\"", 3, 0, mainGo),

		fn(tool, "main().", mainGo, "Function", "func main()", 3, 300),
		fn(tool, "compute().", mainGo, "Function", "func compute(n int) int", 10, 150),
		delegate,
		fn(tool, "parse().", mainGo, "Function", "func parse(args []string) error", 30, 150),
		fn(tool, "print().", mainGo, "Function", "func print(w io.Writer, rows []string)", 40, 150),
		fn(tool, "viaStore().", mainGo, "Function", "func viaStore(s ifc.Store, n int) error", 50, 150),
		fn(tool, "small().", mainGo, "Function", "func small(a, b string) string", 60, 50),
	}
}

func boundaryOptions() types.ConformanceOptions {
	return types.ConformanceOptions{MinSample: 1, Step: 0.05, Limits: map[types.ConformanceKey]types.ConformanceLimit{
		{Metric: types.ConformanceMainLogic, Population: types.ConformancePopMain}:         {Anchor: 119},
		{Metric: types.ConformanceStorePathExport, Population: types.ConformancePopSource}: {Anchor: 0},
	}}
}

func TestConformanceMainLogic(t *testing.T) {
	out := assembleLens(boundarySymbols()).Conformance(boundaryOptions())
	row := findConformanceRow(t, out, types.ConformanceMainLogic, types.ConformancePopMain, "")
	row.P50, row.P90, row.P95, row.P99, row.Live = 0, 0, 0, 0, 0
	assert.Equal(t, types.ConformanceRow{
		Key: row.Key, N: 7, Value: 119, Anchor: 119, Source: types.ConformanceFromAnchor,
		Violations: []types.ConformanceItem{
			{ID: "cmd/tool/main.go#compute", Source: "cmd/tool/main.go:10", Measure: 150, Home: "internal/lib"},
		},
	}, row, "every callable of the command is sampled; main, a delegate, an args parser, a writer, an interface user and a small helper pass")
}

func TestConformanceStorePathExport(t *testing.T) {
	out := assembleLens(boundarySymbols()).Conformance(boundaryOptions())
	row := findConformanceRow(t, out, types.ConformanceStorePathExport, types.ConformancePopSource, "")
	row.P50, row.P90, row.P95, row.P99, row.Live = 0, 0, 0, 0, 0
	assert.Equal(t, types.ConformanceRow{
		Key: row.Key, N: 2, Source: types.ConformanceFromAnchor,
		Violations: []types.ConformanceItem{
			{ID: "internal/store/store.go#LogPath", Source: "internal/store/store.go:3", Measure: 2, Home: "internal/store"},
		},
	}, row, "LogPath and StoreDir: Open, a typed constant and a predicate are no paths, and QueueDir's package keeps no state"+
		"; the test file and the store's own files do not count")
}

func TestConformanceFileSplit(t *testing.T) {
	t.Parallel()

	sh := assembleSymbols(".", splitSymbols(), nil)
	g := assembleLens(splitSymbols())
	g.Merge(nil, splitEdges(sh.Nodes, sh.Edges))
	options := func(value int) types.ConformanceOptions {
		key := types.ConformanceKey{Metric: types.ConformanceFileSplit, Population: types.ConformancePopSource}
		return types.ConformanceOptions{MinSample: 1, Step: 0.05,
			Limits: map[types.ConformanceKey]types.ConformanceLimit{key: {Anchor: value}}}
	}

	row := findConformanceRow(t, g.Conformance(options(1500)), types.ConformanceFileSplit, types.ConformancePopSource, "")
	row.P50, row.P90, row.P95, row.P99, row.Live = 0, 0, 0, 0, 0
	assert.Equal(t, types.ConformanceRow{
		Key: row.Key, N: 2, Value: 1500, Anchor: 1500, Source: types.ConformanceFromAnchor,
		Violations: []types.ConformanceItem{
			{ID: "internal/r/r_tasks.go", Source: "internal/r/r_tasks.go:1", Measure: 1100, Home: "internal/r/r.go", Unshared: true},
		{ID: "internal/r/r_cache.go", Source: "internal/r/r_cache.go:1", Measure: 900, Home: "internal/r/r.go"},
		},
	}, row, "only the concept siblings, both of which would fit in r.go; a floor proposes nothing")

	row = findConformanceRow(t, g.Conformance(options(1000)), types.ConformanceFileSplit, types.ConformancePopSource, "")
	require.Len(t, row.Violations, 1)
	assert.Equal(t, "internal/r/r_cache.go", row.Violations[0].ID, "r.go with r_tasks.go would pass 1000 lines, so that split is owed")
}
