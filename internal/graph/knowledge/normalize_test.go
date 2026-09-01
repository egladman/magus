package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const normalizeNodeID = "file:console/magusfile.buzz"

// pathShapeGraph holds the one file node the shapes below name, rooted at a real
// directory: the root-anchored and backslash readings are gated on the path existing.
func pathShapeGraph(t *testing.T) (*Graph, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "console"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "console", "magusfile.buzz"), []byte("x"), 0o644))

	g := NewGraph()
	g.SetRoot(root)
	g.AddNode(types.KnowledgeNode{ID: normalizeNodeID, Kind: types.KindFile, Label: "magusfile.buzz", Source: "console/magusfile.buzz"})
	g.AddNode(types.KnowledgeNode{ID: "project:console", Kind: types.KindProject, Label: "console"})
	return g, root
}

// Every spelling a human produces by copy-pasting - shell tab-completion, an editor's
// "Copy Path", a Windows-side agent - resolves the node a bare relative path resolves.
func TestResolvePathShapes(t *testing.T) {
	g, root := pathShapeGraph(t)
	abs := filepath.Join(root, "console", "magusfile.buzz")

	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"bare relative", "kind=file console/magusfile.buzz", normalizeNodeID},
		{"uppercase", "kind=file CONSOLE/MAGUSFILE.BUZZ", normalizeNodeID},
		{"dot relative", "kind=file ./console/magusfile.buzz", normalizeNodeID},
		{"root anchored", "kind=file /console/magusfile.buzz", normalizeNodeID},
		{"absolute", "kind=file " + abs, normalizeNodeID},
		{"backslash", `kind=file console\magusfile.buzz`, normalizeNodeID},
		{"id field dot relative", "id=./console/magusfile.buzz", normalizeNodeID},
		{"id field absolute", "id=" + abs, normalizeNodeID},
		{"project field dot relative", "kind=project project=./console", "project:console"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equalf(t, []string{tc.want}, matchIDs(g.Resolve(tc.query, 0)), "%q should resolve", tc.query)
		})
	}
}

// A =~ value is a regex, where a backslash escapes and path.Clean would rewrite the
// pattern into one that matches something else. reFields is never normalized.
func TestResolveLeavesRegexValuesIntact(t *testing.T) {
	g, _ := pathShapeGraph(t)

	for _, q := range []string{
		`id=~^file:console/.*\.buzz$`,
		`id=~console/magusfile\.buzz`,
		`id=~file:console/[a-z]+\.buzz`,
	} {
		assert.Equalf(t, []string{normalizeNodeID}, matchIDs(g.Resolve(q, 0)), "%q must match unmangled", q)
	}
	// A pattern whose literal spelling only survives if nothing cleaned it: "//" would
	// collapse to "/" and "./" would vanish, and either rewrite changes what it matches.
	assert.Empty(t, matchIDs(g.Resolve(`id=~file://console`, 0)))
	assert.Empty(t, matchIDs(g.Resolve(`id=~^\./console`, 0)))
}

// A bare term must keep behaving exactly as it did: no separator, no path reading.
func TestResolveBareTermIsUnchanged(t *testing.T) {
	g, _ := pathShapeGraph(t)
	assert.Equal(t, []string{normalizeNodeID}, matchIDs(g.Resolve("kind=file magusfile.buzz", 0)))
	assert.Empty(t, matchIDs(g.Resolve("kind=file nosuchterm", 0)))
}

// An absolute path under a DIFFERENT root is not this workspace's file. It stays the
// literal term it was, so the query answers "not here" rather than answering about
// whatever "Users/other/repo/console/magusfile.buzz" would have matched.
func TestResolveOutsideWorkspaceIsNotReRooted(t *testing.T) {
	g, _ := pathShapeGraph(t)
	assert.Empty(t, matchIDs(g.Resolve("kind=file /Users/other/repo/console/magusfile.buzz", 0)))
	assert.Empty(t, matchIDs(g.Resolve("kind=file ../sibling/console/magusfile.buzz", 0)))
}

// A graph with no root still canonicalises the shapes that need no workspace on disk,
// and refuses the ones that do rather than guessing.
func TestResolveWithoutRoot(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: normalizeNodeID, Kind: types.KindFile, Label: "magusfile.buzz"})

	assert.Equal(t, []string{normalizeNodeID}, matchIDs(g.Resolve("kind=file ./console/magusfile.buzz", 0)))
	assert.Empty(t, matchIDs(g.Resolve(`kind=file console\magusfile.buzz`, 0)))
}

// Negation reads the same shapes as the positive side, or `-./console/x` would exclude
// nothing while `-console/x` excluded the node.
func TestResolveNegatedTermNormalizes(t *testing.T) {
	g, _ := pathShapeGraph(t)
	assert.Empty(t, matchIDs(g.Resolve("kind=file magusfile.buzz -./console/magusfile.buzz", 0)))
}
