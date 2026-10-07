package knowledge

import (
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// layoutFile is one file a fixture places, defining one symbol and referencing others by name.
type layoutFile struct {
	path string
	refs []string
}

// layoutGraph assembles fixture files as a symbol index does: each defines a function named for
// its path, and each ref makes it reference the function another file defines.
func layoutGraph(t *testing.T, files ...layoutFile) *Graph {
	t.Helper()
	key := func(p string) string { return "local " + strings.NewReplacer("/", "_", ".", "_").Replace(p) }
	byKey := map[string]*types.KnowledgeSymbol{}
	var order []string
	for _, f := range files {
		k := key(f.path)
		byKey[k] = &types.KnowledgeSymbol{
			Key: k, Label: path.Base(f.path), Language: languageFromPath(f.path),
			Source: f.path + ":1", Defs: []string{f.path},
		}
		order = append(order, k)
	}
	for _, f := range files {
		for _, r := range f.refs {
			s := byKey[key(r)]
			require.NotNil(t, s, r)
			s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: f.path})
		}
	}
	syms := make([]types.KnowledgeSymbol, 0, len(order))
	for _, k := range order {
		syms = append(syms, *byKey[k])
	}
	return mergeAll([]Shard{assembleSymbols(".", syms, []types.TargetGraphProject{{Path: "."}})})
}

// goPackages lays out n Go directories under parent, each with two files and a test.
func goPackages(parent string, n int) []layoutFile {
	var out []layoutFile
	for i := range n {
		dir := fmt.Sprintf("%s/pkg%c", parent, 'a'+i)
		out = append(out, layoutFile{path: dir + "/a.go"}, layoutFile{path: dir + "/b.go"},
			layoutFile{path: dir + "/a_test.go"})
	}
	return out
}

func addedSet(files ...layoutFile) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		out[f.path] = true
	}
	return out
}

func TestLayoutFlagsANewOneFileUntestedDigitNamedPackage(t *testing.T) {
	siblings := goPackages("internal", 6)
	// pkgb3 sorts between pkgb and pkgc, so those two are its nearest siblings.
	merge := layoutFile{path: "internal/pkgb3/merge.go"}
	caller := layoutFile{path: "internal/pkga/c.go", refs: []string{merge.path}}
	g := layoutGraph(t, append(siblings, merge, caller)...)

	got := g.Layout(LayoutChange{Added: addedSet(merge, caller)})
	evidence := "1 Go file, 0 test files and 1 importing directory"
	assert.Equal(t, map[string][]types.Check{"internal/pkgb3": {
		{
			Name: types.CheckPackageFiles, Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
			Message: "`internal/pkgb3`: " + evidence + "; 6 of 6 Go directories beside it hold more than one, as `internal/pkgb` and `internal/pkgc` do",
		},
		{
			Name: types.CheckPackageTests, Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
			Message: "`internal/pkgb3`: " + evidence + "; 6 of 6 Go directories beside it hold a test file, as `internal/pkgb` and `internal/pkgc` do",
		},
		{
			Name: types.CheckPackageName, Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
			Message: "`internal/pkgb3`: " + evidence + "; 6 of 6 Go directories beside it carry no digit in their names, as `internal/pkgb` and `internal/pkgc` do",
		},
	}}, got)
}

func TestLayoutNeverFlagsADirectoryThatAlreadyExisted(t *testing.T) {
	siblings := goPackages("internal", 6)
	old := layoutFile{path: "internal/merge3/merge.go"}
	added := layoutFile{path: "internal/merge3/diff.go"}
	g := layoutGraph(t, append(siblings, old, added)...)

	assert.Nil(t, g.Layout(LayoutChange{Added: addedSet(added)}), "a sibling file makes the directory old")
}

func TestLayoutTreatsAnExistingSubdirectoryAsAnOldParent(t *testing.T) {
	siblings := goPackages("internal", 6)
	nested := layoutFile{path: "internal/graph/url/url.go"}
	added := layoutFile{path: "internal/graph/lens.go"}
	g := layoutGraph(t, append(siblings, nested, added)...)

	assert.Nil(t, g.Layout(LayoutChange{Added: addedSet(added)}), "a directory holding an older subdirectory is not new")
}

func TestLayoutSilentBelowTheGates(t *testing.T) {
	merge := layoutFile{path: "internal/merge3/merge.go"}
	t.Run("cohort", func(t *testing.T) {
		g := layoutGraph(t, append(goPackages("internal", 4), merge)...)
		assert.Nil(t, g.Layout(LayoutChange{Added: addedSet(merge)}), "four siblings are a coincidence")
	})
	t.Run("share", func(t *testing.T) {
		files := append(goPackages("internal", 4),
			layoutFile{path: "internal/singlea/x.go"}, layoutFile{path: "internal/singleb/x.go"})
		g := layoutGraph(t, append(files, merge)...)
		got := g.Layout(LayoutChange{Added: addedSet(merge)})
		// 4 of 6 hold two files and a test, which is a split; 6 of 6 carry no digit.
		require.Len(t, got["internal/merge3"], 1)
		assert.Equal(t, types.CheckPackageName, got["internal/merge3"][0].Name)
		assert.Nil(t, g.Layout(LayoutChange{Added: addedSet(merge), MinCohort: 7}), "a raised cohort gate holds it back")
	})
}

func TestLayoutNamesTheOutliers(t *testing.T) {
	files := append(goPackages("internal", 5), layoutFile{path: "internal/lone/x.go"}, layoutFile{path: "internal/lone/x_test.go"})
	added := layoutFile{path: "internal/zed/x.go"}
	g := layoutGraph(t, append(files, added, layoutFile{path: "internal/zed/x_test.go"})...)

	got := g.Layout(LayoutChange{Added: addedSet(added, layoutFile{path: "internal/zed/x_test.go"})})
	require.Len(t, got["internal/zed"], 1)
	// The message is pinned by substring below.
	want := types.Check{
		Name:     types.CheckPackageFiles,
		Status:   types.CheckAdvice,
		Evidence: types.EvidenceMeasured,
		Message:  got["internal/zed"][0].Message,
		Details:  []string{"not: internal/lone"},
	}
	assert.Equal(t, want, got["internal/zed"][0])
	assert.Contains(t, got["internal/zed"][0].Message, "5 of 6 Go directories beside it hold more than one, as `internal/pkge` and `internal/pkgd` do")
}

func TestLayoutComparesOnlyTheSameLanguage(t *testing.T) {
	var files []layoutFile
	for i := range 6 {
		dir := fmt.Sprintf("web/mod%c", 'a'+i)
		files = append(files, layoutFile{path: dir + "/index.ts"}, layoutFile{path: dir + "/util.ts"},
			layoutFile{path: dir + "/index.test.ts"})
	}
	goDir := layoutFile{path: "web/tool/main.go"}
	tsDir := layoutFile{path: "web/view/index.ts"}
	g := layoutGraph(t, append(files, goDir, tsDir)...)

	got := g.Layout(LayoutChange{Added: addedSet(goDir, tsDir)})
	assert.NotContains(t, got, "web/tool", "no Go directory beside it forms a norm")
	require.Contains(t, got, "web/view")
	assert.Contains(t, got["web/view"][0].Message, "1 TypeScript file, 0 test files")
}

func TestLayoutReadsTestFilesByTheirLanguagesConvention(t *testing.T) {
	var files []layoutFile
	for i := range 6 {
		dir := fmt.Sprintf("web/mod%c", 'a'+i)
		// A TypeScript file named like a Go test is not a test.
		files = append(files, layoutFile{path: dir + "/a.ts"}, layoutFile{path: dir + "/a.spec.ts"})
	}
	added := []layoutFile{{path: "web/new/a.ts"}, {path: "web/new/b.ts"}, {path: "web/new/store_test.ts"}}
	g := layoutGraph(t, append(files, added...)...)

	got := g.Layout(LayoutChange{Added: addedSet(added...)})
	require.Len(t, got["web/new"], 1)
	assert.Equal(t, types.CheckPackageTests, got["web/new"][0].Name)
	assert.Contains(t, got["web/new"][0].Message, "3 TypeScript files, 0 test files")
}

func TestLayoutIgnoresGeneratedAndFixtureDirectories(t *testing.T) {
	siblings := goPackages("internal", 6)
	gen := layoutFile{path: "internal/pkga/gen/x.go"}
	fixture := layoutFile{path: "internal/pkgz/testdata/src/x.go"}
	g := layoutGraph(t, append(siblings, gen, fixture)...)

	assert.Nil(t, g.Layout(LayoutChange{Added: addedSet(gen, fixture), Generated: map[string]bool{gen.path: true}}))
}

func TestLayoutIsDeterministicAndCapped(t *testing.T) {
	files := goPackages("internal", 6)
	var added []layoutFile
	for i := range 4 {
		added = append(added, layoutFile{path: fmt.Sprintf("internal/new%d/x.go", i)})
	}
	g := layoutGraph(t, append(files, added...)...)

	first := g.Layout(LayoutChange{Added: addedSet(added...)})
	total := 0
	for _, cs := range first {
		total += len(cs)
	}
	assert.Equal(t, conformanceMaxFindings, total, "four new directories break three checks each")
	assert.Equal(t, []string{"internal/new0", "internal/new1"}, slices.Sorted(maps.Keys(first)), "directory order, then check order")
	for range 5 {
		assert.Equal(t, first, g.Layout(LayoutChange{Added: addedSet(added...)}))
	}
}

// TestLayoutMeasure reports what the lens would say about every directory of an export if each
// were new, skipped unless MAGUS_CONFORMANCE_GRAPH names a `magus graph export --symbols -o json`:
//
//	./magus graph export --symbols -o json --tee /tmp/graph.json -s
//	MAGUS_CONFORMANCE_GRAPH=/tmp/graph.json ./magus run test . -- -run 'TestLayoutMeasure$' -v -count=1
//
// A directory is made new by treating its own files as the change's additions, one directory at
// a time, so the rest of the tree stays the norm it is measured against.
func TestLayoutMeasure(t *testing.T) {
	file := os.Getenv("MAGUS_CONFORMANCE_GRAPH")
	if file == "" {
		t.Skip("MAGUS_CONFORMANCE_GRAPH is not set")
	}
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var export types.KnowledgeGraphOutput
	require.NoError(t, json.Unmarshal(raw, &export), file)
	g := NewGraph()
	g.Merge(export.Nodes, export.Links)

	byDir := map[string][]string{}
	generated := map[string]bool{}
	for _, n := range g.nodes {
		if n.Kind != types.KindFile {
			continue
		}
		if slices.Contains(strings.Split(n.Source, "/"), "gen") {
			generated[n.Source] = true
		}
		byDir[path.Dir(n.Source)] = append(byDir[path.Dir(n.Source)], n.Source)
	}
	counts := map[string]int{}
	for _, dir := range slices.Sorted(maps.Keys(byDir)) {
		added := map[string]bool{}
		for _, f := range byDir[dir] {
			added[f] = true
		}
		for d, checks := range g.Layout(LayoutChange{Added: added, Generated: generated}) {
			for _, c := range checks {
				counts[c.Name]++
				t.Logf("%s %s: %s %v", c.Name, d, c.Message, c.Details)
			}
		}
	}
	t.Logf("findings by check: %v", counts)
}
