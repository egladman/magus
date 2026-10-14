package knowledge

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// conformNS is the key of package pkg ("" for the root) in module example.com/m.
func conformNS(pkg string) string { return "gomod example.com/m `example.com/m" + pkg + "`/" }

// conformSym is one Go symbol record defined at file:start..end in package pkg.
func conformSym(pkg, desc, file string, start, end, lines int, shape string, tokens int, refs ...string) types.KnowledgeSymbol {
	s := types.KnowledgeSymbol{
		Key: conformNS(pkg) + desc, Language: "go", Namespace: conformNS(pkg),
		Source: fmt.Sprintf("%s:%d", file, start), DefEndLine: end, SourceLines: lines,
		BodyShape: shape, BodyTokens: tokens, Defs: []string{file},
	}
	for _, r := range refs {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: r, Count: 1})
	}
	return s
}

// conformPkg is package pkg's namespace record: every file defines it, and each importer
// references it.
func conformPkg(pkg string, files []string, importers ...string) types.KnowledgeSymbol {
	s := types.KnowledgeSymbol{Key: conformNS(pkg), Language: "go", Namespace: conformNS(pkg), Source: files[0] + ":1", Defs: files}
	for _, r := range importers {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: r, Count: 1})
	}
	return s
}

// conformFixture is a module with a library pair under internal/, a command, a test, a
// platform-twin pair, a generated package, a declared output and an excluded tree.
func conformFixture() *Graph {
	const (
		a, b, gen, tool, third      = "/internal/a", "/internal/b", "/internal/a/gen", "/cmd/tool", "/third"
		aGo, aTest, linux, darwin   = "internal/a/a.go", "internal/a/a_test.go", "internal/a/fs_linux.go", "internal/a/fs_darwin.go"
		bGo, bString, genGo, mainGo = "internal/b/b.go", "internal/b/b_string.go", "internal/a/gen/gen.go", "cmd/tool/main.go"
	)
	syms := []types.KnowledgeSymbol{
		conformPkg("", []string{"root.go"}),
		conformPkg(a, []string{aGo, aTest, linux, darwin}, mainGo),
		conformPkg(b, []string{bGo, bString}, aGo, mainGo),
		conformPkg(gen, []string{genGo}, bGo),
		conformPkg(tool, []string{mainGo}),
		conformPkg(third, []string{"third/t.go"}),

		conformSym("", "Root().", "root.go", 3, 4, 10, "", 0),
		conformSym(a, "F().", aGo, 3, 12, 40, "S1", 90, mainGo),
		conformSym(a, "T#", aGo, 14, 16, 40, "", 0, aTest),
		conformSym(a, "T#M1().", aGo, 18, 19, 40, "", 0),
		conformSym(a, "T#M2().", aGo, 20, 21, 40, "", 0),
		conformSym(a, "T#M3().", aGo, 22, 23, 40, "", 0),
		conformSym(a, "linuxStat().", linux, 3, 5, 10, "S2", 85),
		conformSym(a, "darwinStat().", darwin, 3, 5, 10, "S2", 85),
		conformSym(a, "TestF().", aTest, 3, 12, 30, "S1", 90),
		conformSym(b, "G().", bGo, 3, 12, 20, "S1", 90, aGo),
		conformSym(b, "Str().", bString, 1, 3, 5, "S3", 90),
		conformSym(gen, "Gen().", genGo, 3, 12, 50, "S1", 90),
		conformSym(tool, "main().", mainGo, 5, 30, 60, "S4", 200),
		conformSym(tool, "Run().", mainGo, 32, 34, 60, "", 0),
		conformSym(third, "Third().", "third/t.go", 3, 12, 15, "S1", 90),
	}
	return mergeAll([]Shard{assembleSymbols(".", syms, nil)})
}

func conformFixtureOptions() types.ConformOptions {
	limit := func(m types.ConformMetric, p types.ConformPopulation, scope string, pct float64, anchor int) (types.ConformKey, types.ConformLimit) {
		return types.ConformKey{Metric: m, Population: p, Scope: scope}, types.ConformLimit{Percentile: pct, Anchor: anchor}
	}
	o := types.ConformOptions{
		Limits:    map[types.ConformKey]types.ConformLimit{},
		MinSample: 1,
		Step:      0.05,
		Exclude:   []string{"third/**"},
		Generated: map[string]bool{"internal/b/b_string.go": true},
		Ratchet: types.ConformRatchet{
			Schema: 1,
			Thresholds: []types.ConformThreshold{{
				ConformKey: types.ConformKey{Metric: types.ConformFileLines, Population: types.ConformPopSource},
				Value:      30, N: 5, Percentile: 95, Why: "p95 at fixture", Line: 4,
			}},
			Known: []types.ConformKnown{
				{Metric: types.ConformCloneTokens, Items: []string{"internal/a/fs_linux.go#linuxStat", "internal/a/fs_darwin.go#darwinStat"}, Reason: "twins", Line: 12},
				{Metric: types.ConformFileLines, Items: []string{"internal/a/a.go", "internal/b/b.go"}, Reason: "split pending", Line: 10},
			},
		},
	}
	for _, l := range []struct {
		m      types.ConformMetric
		p      types.ConformPopulation
		scope  string
		pct    float64
		anchor int
	}{
		{types.ConformFileLines, types.ConformPopSource, "", 95, 100},
		{types.ConformFileLines, types.ConformPopMain, "", 95, 100},
		{types.ConformFileLines, types.ConformPopTest, "", 99, 100},
		{types.ConformFuncLines, types.ConformPopSource, "", 95, 8},
		{types.ConformTypeMethods, types.ConformPopSource, "", 95, 49},
		{types.ConformFileSymbols, types.ConformPopSource, "", 95, 82},
		{types.ConformDepFanout, types.ConformPopSource, "", 95, 62},
		{types.ConformDepFanout, types.ConformPopMain, "cmd/tool", 0, 89},
		{types.ConformCloneTokens, types.ConformPopSource, "", 50, 80},
		{types.ConformExportScope, types.ConformPopSource, "", 0, 0},
		{types.ConformExportScope, types.ConformPopMain, "", 0, 0},
	} {
		k, v := limit(l.m, l.p, l.scope, l.pct, l.anchor)
		o.Limits[k] = v
	}
	return o
}

func conformRowFor(t *testing.T, out types.ConformOutput, m types.ConformMetric, p types.ConformPopulation, scope string) types.ConformRow {
	t.Helper()
	for _, r := range out.Rows {
		if r.Key == (types.ConformKey{Metric: m, Population: p, Scope: scope}) {
			return r
		}
	}
	require.Failf(t, "no row", "%s %s %q", m, p, scope)
	return types.ConformRow{}
}

func TestConformMeasuresEveryMetricAndPopulation(t *testing.T) {
	out := conformFixture().Conform(conformFixtureOptions())

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
		"func-lines main ", "func-lines source ", "func-lines test ",
		"type-methods source ",
	}, keys, "generated, excluded and fixture files form no row and add no sample")
	assert.Equal(t, types.VerdictFound, out.Answer.Verdict)
	assert.Equal(t, 1, out.Excluded, "third/t.go")

	// The committed 30 is enforced, not the anchor 100 or the live p95 40; a.go is known.
	lines := conformRowFor(t, out, types.ConformFileLines, types.ConformPopSource, "")
	assert.Equal(t, types.ConformRow{
		Key: lines.Key, N: 5, P50: 10, P90: 40, P95: 40, P99: 40, Live: 40, Value: 30, Anchor: 100,
		Known: 1, Source: types.ConformFromRatchet,
	}, lines)
	assert.Equal(t, 60, conformRowFor(t, out, types.ConformFileLines, types.ConformPopMain, "").P99)
	assert.Equal(t, 30, conformRowFor(t, out, types.ConformFileLines, types.ConformPopTest, "").P99)

	funcs := conformRowFor(t, out, types.ConformFuncLines, types.ConformPopSource, "")
	assert.Equal(t, 8, funcs.N, "F, G, the twins, three methods and Root")
	assert.Equal(t, []types.ConformItem{
		{ID: "internal/a/a.go#F", Source: "internal/a/a.go:3", Measure: 10},
		{ID: "internal/b/b.go#G", Source: "internal/b/b.go:3", Measure: 10},
	}, funcs.Violations)
	assert.Equal(t, 26, conformRowFor(t, out, types.ConformFuncLines, types.ConformPopMain, "").P99)

	methods := conformRowFor(t, out, types.ConformTypeMethods, types.ConformPopSource, "")
	assert.Equal(t, 1, methods.N)
	assert.Equal(t, 3, methods.P50, "T declares M1, M2 and M3")

	assert.Equal(t, 2, conformRowFor(t, out, types.ConformFileSymbols, types.ConformPopSource, "").P99, "a.go: F and T")
	assert.Equal(t, 2, conformRowFor(t, out, types.ConformFileSymbols, types.ConformPopMain, "").P50, "main and Run")

	// A package is keyed by its own directory when that directory has a value, else its layer.
	tool := conformRowFor(t, out, types.ConformDepFanout, types.ConformPopMain, "cmd/tool")
	assert.Equal(t, []int{1, 2, 89}, []int{tool.N, tool.P50, tool.Value})
	internal := conformRowFor(t, out, types.ConformDepFanout, types.ConformPopSource, "internal")
	assert.Equal(t, []int{2, 1, 62}, []int{internal.N, internal.P99, internal.Value},
		"b importing the generated package counts; what gen imports would not")

	clones := conformRowFor(t, out, types.ConformCloneTokens, types.ConformPopSource, "")
	assert.Equal(t, 5, clones.N, "main's body counts with source")
	assert.Equal(t, []types.ConformItem{
		{ID: "internal/a/a.go#F, internal/b/b.go#G", Source: "internal/a/a.go:3", Measure: 90},
	}, clones.Violations)
	testClones := conformRowFor(t, out, types.ConformCloneTokens, types.ConformPopTest, "")
	assert.Equal(t, 1, testClones.N)
	assert.Empty(t, testClones.Violations, "no value is configured for test clones")
	assert.Empty(t, testClones.Source)

	assert.Equal(t, []types.ConformItem{{ID: "internal/a/a.go#T", Source: "internal/a/a.go:14", Measure: 1}},
		conformRowFor(t, out, types.ConformExportScope, types.ConformPopSource, "").Violations,
		"F is used from cmd/tool; T only from its own test")
	assert.Equal(t, []types.ConformItem{{ID: "cmd/tool/main.go#Run", Source: "cmd/tool/main.go:32", Measure: 1}},
		conformRowFor(t, out, types.ConformExportScope, types.ConformPopMain, "").Violations)

	assert.Equal(t, []types.ConformKnown{
		{Metric: types.ConformFileLines, Items: []string{"internal/b/b.go"}, Reason: "split pending", Line: 10},
		{Metric: types.ConformCloneTokens, Items: []string{"internal/a/fs_linux.go#linuxStat", "internal/a/fs_darwin.go#darwinStat"}, Reason: "twins", Line: 12},
	}, out.Stale, "b.go is under 30 now, and twins never form a group")
}

func TestConformKnownCloneGroup(t *testing.T) {
	g := conformFixture()
	o := conformFixtureOptions()
	o.Ratchet.Known = []types.ConformKnown{{Metric: types.ConformCloneTokens, Items: []string{"internal/a/a.go#F", "internal/b/b.go#G"}, Reason: "one home pending"}}

	clones := conformRowFor(t, g.Conform(o), types.ConformCloneTokens, types.ConformPopSource, "")
	assert.Equal(t, 1, clones.Known)
	assert.Empty(t, clones.Violations)
	assert.Empty(t, g.Conform(o).Stale)

	// A group's members must all be known: one entry naming only F leaves the group judged.
	o.Ratchet.Known[0].Items = []string{"internal/a/a.go#F"}
	clones = conformRowFor(t, g.Conform(o), types.ConformCloneTokens, types.ConformPopSource, "")
	assert.Zero(t, clones.Known)
	assert.Len(t, clones.Violations, 1)
}

func TestCloneGroupsSkipsPlatformTwins(t *testing.T) {
	groups := cloneGroups(conformFixture(), conformFixtureOptions())
	assert.Equal(t, [][]types.ConformItem{{
		{ID: "internal/a/a.go#F", Source: "internal/a/a.go:3", Measure: 90},
		{ID: "internal/b/b.go#G", Source: "internal/b/b.go:3", Measure: 90},
	}}, groups, "the S2 twins form none; test, generated and excluded copies of S1 join none")
}

func TestExportScope(t *testing.T) {
	assert.Equal(t, []types.ConformItem{
		{ID: "cmd/tool/main.go#Run", Source: "cmd/tool/main.go:32", Measure: 1},
		{ID: "internal/a/a.go#T", Source: "internal/a/a.go:14", Measure: 1},
	}, exportScope(conformFixture(), conformFixtureOptions()), "methods, the root package and test files are out of scope")
}

func TestConformSamplesDropGeneratedAndExcluded(t *testing.T) {
	samples := conformSamples(conformFixture(), conformFixtureOptions())
	for key, items := range samples {
		for _, it := range items {
			assert.NotContains(t, []string{"internal/b/b_string.go", "internal/a/gen/gen.go", "third/t.go"}, itemFile(it.ID), "%v", key)
		}
	}
}

// conformSizedFiles is a graph of one package whose files have the given line counts.
func conformSizedFiles(lines ...int) *Graph {
	var syms []types.KnowledgeSymbol
	for i, n := range lines {
		syms = append(syms, conformSym("/pkg", fmt.Sprintf("F%d().", i), fmt.Sprintf("pkg/f%02d.go", i), 1, 1, n, "", 0))
	}
	return mergeAll([]Shard{assembleSymbols(".", syms, nil)})
}

// A proposal is the live percentile, offered only when a large enough sample sits a full
// step under the enforced value. A live value looser than it yields violations instead.
func TestConformProposalRule(t *testing.T) {
	var lines []int
	for i := 1; i <= 30; i++ {
		lines = append(lines, i)
	}
	g := conformSizedFiles(lines...)
	key := types.ConformKey{Metric: types.ConformFileLines, Population: types.ConformPopSource}
	for _, tc := range []struct {
		name               string
		committed          int
		minSample          int
		propose, violating int
		src                types.ConformSource
	}{
		{name: "anchor far above the live p95", minSample: 30, propose: 29, src: types.ConformFromAnchor},
		{name: "too few samples", minSample: 31, src: types.ConformFromAnchor},
		{name: "within one step", committed: 30, minSample: 30, src: types.ConformFromRatchet},
		{name: "live looser than the commit", committed: 25, minSample: 30, violating: 5, src: types.ConformFromRatchet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := types.ConformOptions{
				Limits:    map[types.ConformKey]types.ConformLimit{key: {Percentile: 95, Anchor: 100}},
				MinSample: tc.minSample, Step: 0.05,
			}
			if tc.committed > 0 {
				o.Ratchet = types.ConformRatchet{Schema: 1, Thresholds: []types.ConformThreshold{{ConformKey: key, Value: tc.committed}}}
			}
			row := conformRowFor(t, g.Conform(o), types.ConformFileLines, types.ConformPopSource, "")
			assert.Equal(t, 29, row.Live)
			assert.Equal(t, tc.propose, row.Propose)
			assert.Len(t, row.Violations, tc.violating)
			assert.Equal(t, tc.src, row.Source)
		})
	}
}

func TestConformIsDeterministic(t *testing.T) {
	g, o := conformFixture(), conformFixtureOptions()
	first := g.Conform(o)
	for range 10 {
		assert.Equal(t, first, conformFixture().Conform(o))
	}
}

func TestConformWithoutSymbolsIsUnknown(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: fileID("a.go"), Kind: types.KindFile, Source: "a.go"})
	out := g.Conform(conformFixtureOptions())
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
	assert.True(t, moduleRoot(symbolID(conformNS(""))))
	assert.False(t, moduleRoot(symbolID(conformNS("/internal/a"))))
	assert.False(t, moduleRoot(symbolID(conformNS("")+"Root().")))
}
