package knowledge

import (
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDocsDanglingCodeReferenceMGS7002(t *testing.T) {
	root := t.TempDir()
	// MGS2001 is registered (PathReadDenied); MGS9998 is not.
	writeFile(t, root, "docs/x.md", "See MGS2001, but MGS9998 does not exist.\n")

	out := mergeAll([]Shard{assembleDocs(root, nil, nil, "")}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, nil, nil, "")}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, nil, nil, "")}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, []types.Spell{{Name: "go"}}, nil, "")}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, []types.Spell{{Name: "go"}}, nil, "")}).Output()

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
	writeFile(t, root, "handbook/errors/MGS2001.md", "# MGS2001\n") // code: filename-keyed
	writeFile(t, root, "handbook/spells/go.md", "# go\n")           // spell: "spells" segment
	writeFile(t, root, "handbook/buzz/fs.md", "# fs\n")             // module: "buzz" segment
	writeFile(t, root, "handbook/errors/MGS9998.md", "# MGS9998\n") // unregistered code
	writeFile(t, root, "handbook/spells/notaspell.md", "# not\n")   // not a known spell

	out := mergeAll([]Shard{assembleDocs(root, []types.Spell{{Name: "go"}}, nil, "")}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, nil, nil, "")}).Output()

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

	out := mergeAll([]Shard{assembleDocs(root, nil, nil, "")}).Output()

	e, ok := findEdge(out, "doc:docs/concepts/targets.md", "doc:docs/reference/manpage/magus-run.md", types.RelationReferences)
	require.True(t, ok, "a `magus run` mention references its manpage doc")
	assert.Equal(t, types.ConfidenceInferred, e.Confidence)
	assert.False(t, hasEdge(out, "doc:docs/reference/manpage/magus-run.md", "doc:docs/reference/manpage/magus-run.md", types.RelationReferences), "manpage does not reference itself")
}

// A doc under a VCS-ignored path must not become a graph node. The concrete case
// this exists for: `magus agent install` writes rendered copies of cmd/magus/skills/
// into .agents/, .opencode/, and .claude/, all three declared generated in
// .gitignore. Indexing them made the committed graph depend on which provider trees
// the person regenerating it happened to have installed, so the drift gate failed
// for everyone else and CI could never reproduce it.
func TestFindDocFiles_SkipsVCSIgnored(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}

	git("init", "-q")
	write(".gitignore", ".agents/skills/\n.claude/skills/*\n!.claude/skills/hand-authored/\n")
	write("README.md", "# readme")
	write("docs/guide.md", "# guide")
	write(".agents/skills/installed/SKILL.md", "# installed copy")
	write(".claude/skills/installed/SKILL.md", "# installed copy")
	write(".claude/skills/hand-authored/SKILL.md", "# hand authored")

	got := findDocFiles(root, "")

	assert.Contains(t, got, "README.md")
	assert.Contains(t, got, "docs/guide.md")
	// The negation in .gitignore is the whole reason this filters on IGNORED rather
	// than on untracked: hand-authored is uncommitted here too, and must survive.
	assert.Contains(t, got, ".claude/skills/hand-authored/SKILL.md",
		"a re-included path is not ignored and must still be indexed")
	assert.NotContains(t, got, ".agents/skills/installed/SKILL.md")
	assert.NotContains(t, got, ".claude/skills/installed/SKILL.md")
}

// No VCS at all is a normal way to run magus, and it must not silently empty the
// doc set. No answer means index what was found.
func TestFindDocFiles_NoVCSIndexesEverything(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme"), 0o644))

	assert.Equal(t, []string{"README.md"}, findDocFiles(root, ""))
}

// TestFindDocFiles_ExcludesDeclaredNotes is the guard against the quietest failure in the
// notes design: this walk takes EVERY .md in the workspace, not just docs/, so a notes
// store would silently arrive in @docs as kind:doc on the next build - conflating
// human-authored notes with documentation, which is the one distinction the store exists
// to draw.
//
// Both directions matter. The exclusion follows the DECLARED path, so a workspace that
// declares nothing keeps indexing a directory that merely happens to be called notes.
func TestFindDocFiles_ExcludesDeclaredNotes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "a real doc\n")
	writeFile(t, root, "notes/cache-pairing.md", "---\nname: cache-pairing\n---\n\nprose\n")
	writeFile(t, root, "team/notes/deep.md", "---\nname: deep\n---\n\nprose\n")

	assert.Equal(t,
		[]string{"docs/guide.md", "notes/cache-pairing.md", "team/notes/deep.md"},
		findDocFiles(root, ""),
		"with nothing declared, a directory named notes is just documentation")

	assert.Equal(t,
		[]string{"docs/guide.md", "team/notes/deep.md"},
		findDocFiles(root, "notes"),
		"the declared store is excluded; an unrelated notes dir elsewhere is not")

	assert.Equal(t,
		[]string{"docs/guide.md", "notes/cache-pairing.md"},
		findDocFiles(root, "team/notes"),
		"the exclusion follows the declaration, not the name")
}

// TestAssembleDocs_DeclaredNotesProduceNoDocNode closes the same gap one level up, at the
// shard, which is what a query actually reads.
func TestAssembleDocs_DeclaredNotesProduceNoDocNode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "a real doc\n")
	writeFile(t, root, "notes/cache-pairing.md", "---\nname: cache-pairing\n---\n\nprose\n")

	out := mergeAll([]Shard{assembleDocs(root, nil, nil, "notes")}).Output()

	_, ok := nodeByID(out, "doc:docs/guide.md")
	assert.True(t, ok, "documentation is still indexed")
	_, ok = nodeByID(out, "doc:notes/cache-pairing.md")
	assert.False(t, ok, "a note must never become a doc node")
}
