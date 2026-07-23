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

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{}, nil)}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{}, nil)}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{}, nil)}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{Spells: []types.SpellEntry{{Name: "go"}}}, nil)}).Output()

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
	// The CURRENT (post-reorg) layout: codes under reference/codes, spells under
	// concepts/spells, modules under reference/buzz. The matchers anchor on filename +
	// path segment, not a fixed prefix, so these must produce documents edges - the exact
	// thing the reorg silently broke.
	writeFile(t, root, "docs/reference/codes/sandbox/MGS2010.md", "# MGS2010\nRelated to MGS2001. See [go spell](../../../concepts/spells/go.md).\n")
	writeFile(t, root, "docs/concepts/spells/go.md", "# go\nThe go spell.\n")
	writeFile(t, root, "docs/reference/buzz/fs.md", "# fs\nFilesystem module.\n")
	writeFile(t, root, "README.md", "Uses the `go` spell; see MGS2010 when it fails.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{Spells: []types.SpellEntry{{Name: "go"}}}, nil)}).Output()

	for _, id := range []string{"doc:docs/reference/codes/sandbox/MGS2010.md", "doc:docs/concepts/spells/go.md", "doc:docs/reference/buzz/fs.md", "doc:README.md"} {
		_, ok := nodeByID(out, id)
		assert.Truef(t, ok, "missing doc node %q", id)
	}

	// Path-convention documents edges survive the reorg layout.
	e, ok := findEdge(out, "doc:docs/reference/codes/sandbox/MGS2010.md", "diagnostic:MGS2010", types.RelationDocuments)
	require.True(t, ok, "code page documents its diagnostic under reference/codes")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	e, ok = findEdge(out, "doc:docs/concepts/spells/go.md", "spell:go", types.RelationDocuments)
	require.True(t, ok, "spell page documents its spell under concepts/spells")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	e, ok = findEdge(out, "doc:docs/reference/buzz/fs.md", "module:fs", types.RelationDocuments)
	require.True(t, ok, "module page documents its module under reference/buzz")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)

	// In-body MGS mention -> inferred; a markdown link -> references.
	e, ok = findEdge(out, "doc:docs/reference/codes/sandbox/MGS2010.md", "diagnostic:MGS2001", types.RelationDocuments)
	require.True(t, ok, "in-body MGS2001 mention")
	assert.Equal(t, types.ConfidenceInferred, e.Confidence)
	assert.True(t, hasEdge(out, "doc:docs/reference/codes/sandbox/MGS2010.md", "doc:docs/concepts/spells/go.md", types.RelationReferences), "resolved markdown link")

	// README mentions the go spell in backticks and MGS2010 in prose (both inferred).
	assert.True(t, hasEdge(out, "doc:README.md", "spell:go", types.RelationDocuments))
	assert.True(t, hasEdge(out, "doc:README.md", "diagnostic:MGS2010", types.RelationDocuments))
}

// TestDocsPathResilience guards against the reorg-style regression: the documents edges must
// key on entity identity + a path segment, NOT a fixed directory prefix, so moving the docs
// tree cannot silently sever them. Entities here sit under a directory no convention names.
func TestDocsPathResilience(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "handbook/errors/MGS2001.md", "# MGS2001\n")     // code: filename-keyed
	writeFile(t, root, "handbook/spells/go.md", "# go\n")               // spell: "spells" segment
	writeFile(t, root, "handbook/buzz/fs.md", "# fs\n")                 // module: "buzz" segment
	writeFile(t, root, "handbook/errors/MGS9998.md", "# MGS9998\n")     // unregistered code
	writeFile(t, root, "handbook/spells/notaspell.md", "# not\n")       // not a known spell

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{Spells: []types.SpellEntry{{Name: "go"}}}, nil)}).Output()

	assert.True(t, hasEdge(out, "doc:handbook/errors/MGS2001.md", "diagnostic:MGS2001", types.RelationDocuments), "code edge is directory-agnostic")
	assert.True(t, hasEdge(out, "doc:handbook/spells/go.md", "spell:go", types.RelationDocuments), "spell edge anchors on the spells segment")
	assert.True(t, hasEdge(out, "doc:handbook/buzz/fs.md", "module:fs", types.RelationDocuments), "module edge anchors on the buzz segment")

	// Guards: an unregistered code and a non-spell page under a spells dir link nothing.
	assert.False(t, hasEdge(out, "doc:handbook/errors/MGS9998.md", "diagnostic:MGS9998", types.RelationDocuments), "unregistered code has no documents edge")
	assert.False(t, hasEdge(out, "doc:handbook/spells/notaspell.md", "spell:notaspell", types.RelationDocuments), "non-spell page under spells/ links nothing")
}

// TestDocsSectionAttr checks the derived section attr: a page carries its docs/ top-level
// section so it is queryable by where it lives, with no section for top-level or non-docs.
func TestDocsSectionAttr(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guides/mcp.md", "# MCP\n")
	writeFile(t, root, "docs/glossary.md", "# Glossary\n")
	writeFile(t, root, "README.md", "# Readme\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{}, nil)}).Output()

	n, ok := nodeByID(out, "doc:docs/guides/mcp.md")
	require.True(t, ok)
	assert.Equal(t, "guides", n.Attrs[AttrSection], "section derived from path")

	n, ok = nodeByID(out, "doc:docs/glossary.md")
	require.True(t, ok)
	_, has := n.Attrs[AttrSection]
	assert.False(t, has, "top-level doc has no section")

	n, ok = nodeByID(out, "doc:README.md")
	require.True(t, ok)
	_, has = n.Attrs[AttrSection]
	assert.False(t, has, "doc outside docs/ has no section")
}

// TestDocsCommandReferences checks the doc<->command interconnection: a `magus <sub>` mention
// references the manpage doc that documents it, and a manpage never references itself.
func TestDocsCommandReferences(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/reference/manpage/magus-run.md", "# magus run\nRun a target.\n")
	writeFile(t, root, "docs/concepts/targets.md", "A target is what you run with `magus run`.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{}, nil)}).Output()

	e, ok := findEdge(out, "doc:docs/concepts/targets.md", "doc:docs/reference/manpage/magus-run.md", types.RelationReferences)
	require.True(t, ok, "a `magus run` mention references its manpage doc")
	assert.Equal(t, types.ConfidenceInferred, e.Confidence)
	assert.False(t, hasEdge(out, "doc:docs/reference/manpage/magus-run.md", "doc:docs/reference/manpage/magus-run.md", types.RelationReferences), "manpage does not reference itself")
}
