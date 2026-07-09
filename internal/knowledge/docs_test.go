package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocsDanglingCodeReferenceMGS7002(t *testing.T) {
	root := t.TempDir()
	// MGS2001 is registered (PathReadDenied); MGS9998 is not.
	writeFile(t, root, "docs/x.md", "See MGS2001, but MGS9998 does not exist.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{})}).Output()

	// A registered code still gets its inferred documents edge.
	assert.True(t, hasEdge(out, "doc:docs/x.md", "diagnostic:MGS2001", types.RelationDocuments))
	// An unregistered code produces NO dangling edge; instead the doc is tagged.
	_, ok := findEdge(out, "doc:docs/x.md", "diagnostic:MGS9998", types.RelationDocuments)
	assert.False(t, ok, "no dangling edge to an unregistered code")
	d, ok := nodeByID(out, "doc:docs/x.md")
	require.True(t, ok)
	assert.Equal(t, string(types.DanglingDocReference), d.Attrs[AttrDiagnostic])
	assert.Contains(t, d.Attrs["unknown_codes"], "MGS9998")
}

func TestDocsFrontmatterAttrs(t *testing.T) {
	root := t.TempDir()
	// A page with frontmatter title/tags, and one without: the second must carry
	// neither attr (best-effort, not a hard requirement).
	writeFile(t, root, "docs/charms.md", "---\ntitle: Charms\ntags: [reference, argv]\n---\n\nCharms modify argv.\n")
	writeFile(t, root, "docs/plain.md", "# Plain\nNo frontmatter here.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{})}).Output()

	charms, ok := nodeByID(out, "doc:docs/charms.md")
	require.True(t, ok)
	assert.Equal(t, "Charms", charms.Attrs[AttrTitle])
	assert.Equal(t, "reference,argv", charms.Attrs[AttrTags])

	plain, ok := nodeByID(out, "doc:docs/plain.md")
	require.True(t, ok)
	assert.Empty(t, plain.Attrs[AttrTitle], "no frontmatter, no title attr")
	assert.Empty(t, plain.Attrs[AttrTags])
}

// TestDocsFrontmatterCoexistsWithDiagnostic guards that a page carrying BOTH
// frontmatter and a dangling-code reference keeps both sets of attrs (the
// diagnostic branch merges rather than clobbering the frontmatter map).
func TestDocsFrontmatterCoexistsWithDiagnostic(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/x.md", "---\ntitle: X\n---\n\nMentions MGS9998 which does not exist.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{})}).Output()

	d, ok := nodeByID(out, "doc:docs/x.md")
	require.True(t, ok)
	assert.Equal(t, "X", d.Attrs[AttrTitle], "frontmatter survives the diagnostic branch")
	assert.Equal(t, string(types.DanglingDocReference), d.Attrs[AttrDiagnostic])
}

// TestMagusMdNotIngested guards the fixpoint fix: MAGUS.md is a generated catalog,
// so it must NOT become a doc node even when present on disk. Ingesting it would make
// it both an input and an output (its body carries live counts that feed edges that
// change the counts), which is what produced the "settle gen fixpoint" churn.
func TestMagusMdNotIngested(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "MAGUS.md", "# magus\nUses the `go` spell; see MGS2001.\n")
	writeFile(t, root, "README.md", "The `go` spell.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{Spells: []types.SpellEntry{{Name: "go"}}})}).Output()

	_, ok := nodeByID(out, "doc:MAGUS.md")
	assert.False(t, ok, "generated MAGUS.md must not be ingested as a doc node")
	for _, e := range out.Links {
		assert.NotEqualf(t, "doc:MAGUS.md", e.Source, "no edges should originate from the excluded MAGUS.md (found -> %s)", e.Target)
	}
	// README.md is still ingested (control: the exclusion is MAGUS.md-specific).
	_, ok = nodeByID(out, "doc:README.md")
	assert.True(t, ok, "README.md is still ingested")
}

func TestAssembleDocs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/codes/sandbox/MGS2010.md", "# MGS2010\nRelated to MGS2001. See [go spell](../../spells/go.md).\n")
	writeFile(t, root, "docs/spells/go.md", "# go\nThe go spell.\n")
	writeFile(t, root, "README.md", "Uses the `go` spell; see MGS2010 when it fails.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{Spells: []types.SpellEntry{{Name: "go"}}})}).Output()

	for _, id := range []string{"doc:docs/codes/sandbox/MGS2010.md", "doc:docs/spells/go.md", "doc:README.md"} {
		_, ok := nodeByID(out, id)
		assert.Truef(t, ok, "missing doc node %q", id)
	}

	// Path-convention edges are extracted.
	e, ok := findEdge(out, "doc:docs/codes/sandbox/MGS2010.md", "diagnostic:MGS2010", types.RelationDocuments)
	require.True(t, ok, "path documents edge to MGS2010")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	e, ok = findEdge(out, "doc:docs/spells/go.md", "spell:go", types.RelationDocuments)
	require.True(t, ok, "path documents edge to spell go")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)

	// In-body MGS mention -> inferred; a markdown link -> references.
	e, ok = findEdge(out, "doc:docs/codes/sandbox/MGS2010.md", "diagnostic:MGS2001", types.RelationDocuments)
	require.True(t, ok, "in-body MGS2001 mention")
	assert.Equal(t, types.ConfidenceInferred, e.Confidence)
	assert.True(t, hasEdge(out, "doc:docs/codes/sandbox/MGS2010.md", "doc:docs/spells/go.md", types.RelationReferences), "resolved markdown link")

	// README mentions the go spell in backticks and MGS2010 in prose (both inferred).
	assert.True(t, hasEdge(out, "doc:README.md", "spell:go", types.RelationDocuments))
	assert.True(t, hasEdge(out, "doc:README.md", "diagnostic:MGS2010", types.RelationDocuments))
}
