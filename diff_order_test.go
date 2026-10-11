package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

const orderPatch = `diff --git a/iface.go b/iface.go
--- a/iface.go
+++ b/iface.go
@@ -1,1 +1,2 @@
 package p
+type Shape interface{ Area() int }
diff --git a/impl.go b/impl.go
--- a/impl.go
+++ b/impl.go
@@ -1,1 +1,2 @@
 package p
+type Square struct{}
`

var orderTree = map[string]string{
	"iface.go": "package p\ntype Shape interface{ Area() int }\n",
	"impl.go":  "package p\ntype Square struct{}\n",
}

// orderTreeAt writes files under a fresh root.
func orderTreeAt(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	return root
}

// orderDiffFiles reads the files of patch the way attachHunks would, attaching the symbols
// named per path to each file's first hunk.
func orderDiffFiles(patch string, symbols map[string][]string) []types.DiffFile {
	var out []types.DiffFile
	for _, pf := range changeset.Parse(patch) {
		f := types.DiffFile{Path: pf.Path}
		for _, id := range symbols[pf.Path] {
			f.Symbols = append(f.Symbols, types.DiffSymbol{ID: id, Label: id})
		}
		for i, h := range pf.Hunks {
			dh := types.DiffHunk{Index: h.Index, Digest: h.Digest, OldStart: h.OldStart, OldCount: h.OldCount, NewStart: h.NewStart, NewCount: h.NewCount}
			if i == 0 {
				dh.Symbols = symbols[pf.Path]
			}
			f.Hunks = append(f.Hunks, dh)
		}
		out = append(out, f)
	}
	return out
}

func orderStepPaths(o types.DiffOrder) []string {
	var out []string
	for _, g := range o.Groups {
		for _, s := range g.Steps {
			for _, h := range s.Hunks {
				out = append(out, string(g.Kind)+" "+h.Ref.Path+" "+string(h.Why.Relation))
			}
		}
	}
	return out
}

func TestDiffOrderPlacesAnInterfaceBeforeItsImplementation(t *testing.T) {
	t.Parallel()

	root := orderTreeAt(t, orderTree)
	files := orderDiffFiles(orderPatch, map[string][]string{"iface.go": {"Shape"}, "impl.go": {"Square"}})
	g := knowledge.NewGraph()
	g.Merge(nil, []types.KnowledgeEdge{{Source: "Square", Target: "Shape", Relation: types.RelationImplements}})

	order := buildOrder(root, orderPatch, files, g, nil)

	assert.Equal(t, []string{"connected iface.go starts", "connected impl.go implements"}, orderStepPaths(order))
	assert.Equal(t, "implements Shape, declared in step 1", order.Groups[0].Steps[1].Hunks[0].Why.Text)
	assert.True(t, order.Count.Complete)
	assert.Equal(t, 2, order.Count.Placed)
}

func TestDiffOrderPlacesAUseAfterItsDefinitionFromASite(t *testing.T) {
	t.Parallel()

	root := orderTreeAt(t, orderTree)
	files := orderDiffFiles(orderPatch, map[string][]string{"iface.go": {"Shape"}})

	order := buildOrder(root, orderPatch, files, nil, []changeset.OrderSite{{Symbol: "Shape", Path: "impl.go", Line: 2}})

	assert.Equal(t, []string{"connected iface.go starts", "connected impl.go uses"}, orderStepPaths(order))
}

func TestDiffOrderLeavesAFileTheWorkingTreeNoLongerMatchesUnranked(t *testing.T) {
	t.Parallel()

	// impl.go has moved on since the patch's head, so a use the index finds at line 2 of it
	// says nothing about the patch's hunk.
	root := orderTreeAt(t, map[string]string{
		"iface.go": orderTree["iface.go"],
		"impl.go":  "package p\n// edited after the range head\ntype Square struct{}\n",
	})
	files := orderDiffFiles(orderPatch, map[string][]string{"iface.go": {"Shape"}})

	order := buildOrder(root, orderPatch, files, nil, []changeset.OrderSite{{Symbol: "Shape", Path: "impl.go", Line: 2}})

	assert.Equal(t, []string{"connected iface.go starts", "unranked impl.go unranked"}, orderStepPaths(order))
	assert.Equal(t, "the working tree no longer matches the range head for this file", order.Groups[1].Steps[0].Hunks[0].Why.Text)
	assert.True(t, order.Count.Complete)
}

func TestDiffOrderMovedFiles(t *testing.T) {
	t.Parallel()

	deletion := `diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package p
-var X = 1
`
	tests := []struct {
		name  string
		patch string
		tree  map[string]string
		want  map[string]bool
	}{
		{name: "lines in place", patch: orderPatch, tree: orderTree, want: map[string]bool{}},
		{
			name:  "a changed line",
			patch: orderPatch,
			tree:  map[string]string{"iface.go": orderTree["iface.go"], "impl.go": "package p\ntype Circle struct{}\n"},
			want:  map[string]bool{"impl.go": true},
		},
		{
			name:  "a file the tree no longer has",
			patch: orderPatch,
			tree:  map[string]string{"iface.go": orderTree["iface.go"]},
			want:  map[string]bool{"impl.go": true},
		},
		{
			name:  "trailing whitespace is not a move",
			patch: orderPatch,
			tree:  map[string]string{"iface.go": "package p  \ntype Shape interface{ Area() int }\r\n", "impl.go": orderTree["impl.go"]},
			want:  map[string]bool{},
		},
		{name: "a deleted file has no new line to compare", patch: deletion, tree: nil, want: map[string]bool{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, movedFiles(orderTreeAt(t, tc.tree), tc.patch))
		})
	}
}

func TestDiffOrderIsAbsentWhenTheIndexIsNotCurrent(t *testing.T) {
	t.Parallel()

	out := types.Diff{Files: orderDiffFiles(orderPatch, nil)}
	var m *Magus

	m.attachOrder(context.Background(), &out, nil, orderPatch, "the symbol index is not current for this tree")

	assert.Nil(t, out.Order)
	require.Len(t, out.Notes, 1)
	assert.Contains(t, out.Notes[0], "the symbol index is not current for this tree")
	assert.Contains(t, out.Notes[0], "graph build")
}

func TestDiffOrderIndexGap(t *testing.T) {
	t.Parallel()

	built := map[string]time.Time{"libs/a": {}}
	display := func(p string) string { return "project " + p }
	tests := []struct {
		name    string
		fresh   error
		indexed bool
		capable []string
		built   map[string]time.Time
		listed  bool
		want    string
	}{
		{name: "every touched project has an index", indexed: true, capable: []string{"libs/a"}, built: built, listed: true},
		{name: "no touched project is symbol-capable", capable: nil, listed: true},
		{name: "the freshen failed", fresh: assert.AnError, indexed: true, capable: []string{"libs/a"}, built: built, listed: true, want: "the symbol index is not current for this tree"},
		{name: "no index loaded at all", capable: []string{"libs/a"}, listed: true, want: "the symbol index is not current for this tree"},
		{name: "the indexes could not be listed", indexed: true, capable: []string{"libs/a"}, want: "the symbol indexes could not be listed"},
		{
			name: "one touched project has no index while another does", indexed: true, listed: true, built: built,
			capable: []string{"libs/a", "libs/z", "libs/b"}, want: "no symbol index is built for project libs/b, project libs/z",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, indexGap(tc.fresh, tc.indexed, tc.capable, tc.built, tc.listed, display))
		})
	}
}

func TestDiffOrderPartialNoteNamesOneCauseAndOneCommand(t *testing.T) {
	t.Parallel()

	assert.Empty(t, partialOrderNote(false, true, nil))
	assert.Equal(t,
		"reading order is partial: the symbol indexes could not be listed; rebuild with `magus graph build`",
		partialOrderNote(false, false, []string{"libs/a", "libs/b"}), "a failed listing outranks the indexes read without it")
	assert.Equal(t,
		"reading order is partial: the symbol index of libs/a, libs/b could not be read; rebuild with `magus graph build`",
		partialOrderNote(false, true, []string{"libs/a", "libs/b"}))
	assert.Equal(t,
		"reading order is partial: it was cancelled before every symbol index was read",
		partialOrderNote(true, false, []string{"libs/a"}), "a cancelled review is reported as cancelled, not as unreadable indexes")
}

func TestDiffOrderSymbolsOccurrencesAnswersEachKeyAsALoneKeyWould(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg/a/pkg/a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg/a/pkg/a/a.go"), []byte("Foo\n"), 0o644))
	writeSCIP(t, goIndexPath(cacheDir, filepath.Join(root, "pkg/a")))
	projects, spells := goWorkspace("pkg/a")
	in := ingest(config.Config{}, root, cacheDir, projects, spells)
	foo, absent := "gomod example.com/a Foo#", "gomod example.com/a Nothing#"

	reads := symbolsOccurrences(t.Context(), in, []string{foo, absent})

	assert.Equal(t, symbolsOccurrences(t.Context(), in, []string{foo})[foo], reads[foo], "another key in the batch does not change a key's read")
	assert.Empty(t, reads[absent].Files, "a key no index names has an entry with no sites")
	assert.Empty(t, reads[absent].Unreadable)
	require.Len(t, reads[foo].Files, 1)
}

func TestDiffOrderSymbolsOccurrencesRecordsACorruptIndexOnEveryKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cacheDir := filepath.Join(root, ".magus")
	path := goIndexPath(cacheDir, filepath.Join(root, "pkg/a"))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("not a protobuf"), 0o644))
	projects, spells := goWorkspace("pkg/a")

	reads := symbolsOccurrences(t.Context(), ingest(config.Config{}, root, cacheDir, projects, spells), []string{"gomod example.com/a Foo#", "gomod example.com/a Bar#"})

	require.Len(t, reads, 2)
	for key, read := range reads {
		require.Len(t, read.Unreadable, 1, key)
		assert.Equal(t, "pkg/a", read.Unreadable[0].Project.Path, key)
	}
}

func TestDiffOrderRefusesAPatchPathOutsideTheWorkspace(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.go"), []byte("package p\nvar X = 1\n"), 0o644))
	root := filepath.Join(t.TempDir(), "ws")
	require.NoError(t, os.MkdirAll(root, 0o755))
	rel, err := filepath.Rel(root, filepath.Join(outside, "x.go"))
	require.NoError(t, err)
	patch := "diff --git a/" + filepath.ToSlash(rel) + " b/" + filepath.ToSlash(rel) + "\n--- a/" + filepath.ToSlash(rel) + "\n+++ b/" + filepath.ToSlash(rel) + "\n@@ -1,1 +1,2 @@\n package p\n+var X = 1\n"

	moved := movedFiles(root, patch)

	assert.Equal(t, map[string]bool{filepath.ToSlash(rel): true}, moved, "the file the path names exists and matches, and is still not read")
}

func TestDiffOrderChangedDefinitionsAreUniqueAndSorted(t *testing.T) {
	t.Parallel()

	files := []types.DiffFile{
		{Hunks: []types.DiffHunk{{Symbols: []string{"b", "a"}}, {Symbols: []string{"a"}}}},
		{Hunks: []types.DiffHunk{{Symbols: []string{"c"}}, {}}},
	}

	assert.Equal(t, []string{"a", "b", "c"}, changedDefinitions(files))
}

func TestDiffOrderLinksKeepOnlyPairsOfChangedSymbols(t *testing.T) {
	t.Parallel()

	g := knowledge.NewGraph()
	g.Merge(nil, []types.KnowledgeEdge{
		{Source: "impl", Target: "iface", Relation: types.RelationImplements},
		{Source: "impl", Target: "unchanged", Relation: types.RelationImplements},
		{Source: "impl", Target: "iface", Relation: types.RelationCalls},
	})
	files := []types.DiffFile{{Hunks: []types.DiffHunk{{Symbols: []string{"impl", "iface"}}}}}

	assert.Equal(t, []changeset.OrderLink{{Implementer: "impl", Interface: "iface"}}, orderLinks(g, files))
	assert.Nil(t, orderLinks(nil, files))
}
