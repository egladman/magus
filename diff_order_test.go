package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
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
				out = append(out, string(g.Kind)+" "+h.Hunk.Path+" "+string(h.Why.Relation))
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

	m.attachOrder(context.Background(), &out, nil, orderPatch, false)

	assert.Nil(t, out.Order)
	require.Len(t, out.Notes, 1)
	assert.Contains(t, out.Notes[0], "graph build")
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
