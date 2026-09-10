package knowledge

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCitationAccepts(t *testing.T) {
	for _, tc := range []struct {
		name, in, want, wantFrag string
	}{
		{"https", "https://buzz-lang.dev/0.5.0/reference/std/fs.html", "https://buzz-lang.dev/0.5.0/reference/std/fs.html", ""},
		{"plain http", "http://www.apache.org/licenses/LICENSE-2.0", "http://www.apache.org/licenses/LICENSE-2.0", ""},
		{"bare host gains a path", "https://no-color.org", "https://no-color.org/", ""},
		{"host is lowercased", "https://Buzz-Lang.DEV/x/y", "https://buzz-lang.dev/x/y", ""},
		{"query is identity", "https://pkg.go.dev/net/url?tab=doc", "https://pkg.go.dev/net/url?tab=doc", ""},
		{"trailing prose punctuation is trimmed", "https://sapling-scm.com/.", "https://sapling-scm.com/", ""},
		{"fragment leaves the identity", "https://buzz-lang.dev/a/b#frag", "https://buzz-lang.dev/a/b", "frag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ok := parseCitation(tc.in)
			require.True(t, ok, "%q should be citable", tc.in)
			assert.Equal(t, tc.want, c.URL)
			assert.Equal(t, tc.wantFrag, c.Fragment)
		})
	}
}

// TestParseCitationRejects pins the safety rules. Every case except the two synthetic ones
// is a string this workspace's own comments contain: URL-shaped prose showing a format.
func TestParseCitationRejects(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"scheme outside the allowlist", "file:///etc/passwd"},
		{"javascript scheme", "javascript:alert(1)"},
		{"userinfo is credential-shaped", "http://127.0.0.1:7391@evil.com"},
		{"loopback literal", "http://127.0.0.1:9000/path"},
		{"ipv4 literal", "https://93.184.216.34/docs"},
		{"single-label host", "https://host/magus/"},
		{"placeholder host", "https://endpoint/bucket/key"},
		{"angle-bracket metavariable", "http://<host>/x"},
		{"empty labels", "https://.../magus/logs/"},
		{"trailing dot", "https://s3."},
		{"rfc 2606 example label", "https://github.example.com/api/v3"},
		{"reserved tld", "http://magus.localhost/docs"},
		{"over the length cap", "https://ok.example-host.dev/" + strings.Repeat("a", maxURLLen)},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseCitation(tc.in)
			assert.False(t, ok, "%q must not be indexed", tc.in)
		})
	}
}

// linksTree writes a workspace holding one page, one section, one Go source and one Buzz
// source, and returns the merged docs+links graph for it.
func linksTree(t *testing.T, files map[string]string) types.KnowledgeGraphOutput {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		writeFile(t, root, rel, content)
	}
	docShard, cites := assembleDocs(root, nil, nil, "")
	links := assembleLinks(root, nil, newDocIndex(docShard.Nodes), cites)
	return mergeAll([]Shard{docShard, links}).Output()
}

func TestLinksClassifiesCommentURLs(t *testing.T) {
	out := linksTree(t, map[string]string{
		"docs/concepts/cache.md": "# Cache\n\n## Eviction\n\nprose\n",
		"internal/cache/lru.go": `package cache

// evict drops the coldest entry.
// Algorithm: https://buzz-lang.dev/0.5.0/reference/std/fs.html
// Concept: https://magus.example-site.dev/base/concepts/cache/#eviction
// Source: https://github.com/egladman/magus/blob/main/internal/cache/lru.go
func evict() {}
`,
	})

	file := fileID("internal/cache/lru.go")

	// upstream: nothing in the workspace answers to it, so it gets a link node.
	link := linkID("https://buzz-lang.dev/0.5.0/reference/std/fs.html")
	n, ok := nodeByID(out, link)
	require.True(t, ok, "an upstream citation mints a link node")
	assert.Equal(t, types.KindLink, n.Kind)
	assert.Equal(t, "buzz-lang.dev", n.Attrs[attrHost])
	assert.True(t, hasEdge(out, file, link, types.RelationReferences))

	// docs: the URL's trailing path names a page the workspace holds, and its fragment
	// names a heading, so it resolves all the way to the section node.
	section := docSectionID("docs/concepts/cache.md", "eviction")
	assert.True(t, hasEdge(out, file, section, types.RelationReferences),
		"a site URL with an anchor resolves to the docsection, not a link node")
	_, minted := nodeByID(out, linkID("https://magus.example-site.dev/base/concepts/cache/"))
	assert.False(t, minted, "a citation that resolved internally must not also mint a link node")

	assert.False(t, hasEdge(out, file, file, types.RelationReferences),
		"a file citing its own path says nothing and emits no self-edge")
}

func TestLinksResolvesForgeURLToFileNode(t *testing.T) {
	out := linksTree(t, map[string]string{
		"docs/guides/x.md":    "# X\n\nsee [the code](https://github.com/egladman/magus/blob/main/internal/run/run.go).\n",
		"internal/run/run.go": "package run\n",
	})
	target := fileID("internal/run/run.go")
	n, ok := nodeByID(out, target)
	require.True(t, ok, "the cited source file gets a node so the edge has an endpoint")
	assert.Equal(t, types.KindFile, n.Kind)
	assert.Equal(t, "go", n.Attrs[attrLanguage])
	// A page citing a source path is describing it, so the relation is documents.
	assert.True(t, hasEdge(out, docID("docs/guides/x.md"), target, types.RelationDocuments))
}

func TestLinksLeavesAnAmbiguousSuffixUpstream(t *testing.T) {
	out := linksTree(t, map[string]string{
		"console/README.md":         "# Console\n",
		"docs/reference/console.md": "# Console reference\n",
		"docs/guides/y.md":          "# Y\n\n[console](https://magus.example-site.dev/base/reference/console/) and [both](https://elsewhere.dev/x/console/)\n",
	})
	// The longer suffix (reference/console) is unique, so that one resolves.
	assert.True(t, hasEdge(out, docID("docs/guides/y.md"), docID("docs/reference/console.md"), types.RelationReferences))
	// The bare one matches two pages, so it stays an external link rather than guessing.
	assert.True(t, hasEdge(out, docID("docs/guides/y.md"), linkID("https://elsewhere.dev/x/console/"), types.RelationReferences))
}

// TestLinksIgnoresStringLiterals is the whole reason this pass runs a lexer instead of a
// line regex: a URL that is data must not read as a citation.
func TestLinksIgnoresStringLiterals(t *testing.T) {
	out := linksTree(t, map[string]string{
		"internal/http/client.go": "package http\n\nconst endpoint = \"https://api.buzz-lang.dev/v1\"\n\nvar raw = `https://raw.buzz-lang.dev/v1`\n",
	})
	for _, u := range []string{"https://api.buzz-lang.dev/v1", "https://raw.buzz-lang.dev/v1"} {
		_, ok := nodeByID(out, linkID(u))
		assert.False(t, ok, "%s is a string literal, not a comment", u)
	}
}

func TestLinksIndexesBuzzComments(t *testing.T) {
	out := linksTree(t, map[string]string{
		"spells/go/spell.buzz": "// Upstream: https://buzz-lang.dev/0.5.0/reference/\nfun main() > void {\n}\n",
	})
	assert.True(t, hasEdge(out, fileID("spells/go/spell.buzz"),
		linkID("https://buzz-lang.dev/0.5.0/reference/"), types.RelationReferences))
}

func TestLinksIndexesGeneratedFrom(t *testing.T) {
	out := linksTree(t, map[string]string{
		"internal/cli/registry.go": "package cli\n",
		"docs/reference/cli.md":    "---\ntitle: CLI\ngenerated_from: internal/cli/registry.go\ntags: []\n---\n\n# CLI\n",
		"docs/reference/buzz.md":   "---\ntitle: Buzz\ngenerated_from: reference/buzz/\ntags: []\n---\n\n# Buzz\n",
		"std/fs.go":                "package std\n",
		"docs/reference/std.md":    "---\ntitle: Std\ngenerated_from: std/**/*.go\ntags: []\n---\n\n# Std\n",
	})
	assert.True(t, hasEdge(out, docID("docs/reference/cli.md"), fileID("internal/cli/registry.go"), types.RelationDocuments),
		"an exact generated_from path is a claim about one source file")
	// A section path names no file, and a glob names a set whose membership moves with the
	// tree; attaching one page to every match would drown the attribution.
	assert.False(t, hasEdge(out, docID("docs/reference/std.md"), fileID("std/fs.go"), types.RelationDocuments))
	for _, e := range out.Links {
		if e.Relation == types.RelationDocuments {
			assert.NotEqual(t, docID("docs/reference/buzz.md"), e.Source, "a section path names no file to document")
		}
	}
}

// TestLinksEdgesAreDeclared is the shape-table half of the schema bump: every citation
// edge must sit inside types.KnowledgeRelationDefinitions, or the closed vocabulary is a
// claim nobody checked.
func TestLinksEdgesAreDeclared(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/concepts/cache.md", "# Cache\n\n## Eviction\n\nprose\n")
	writeFile(t, root, "docs/reference/cli.md", "---\ntitle: CLI\ngenerated_from: internal/cache/lru.go\ntags: []\n---\n\n# CLI\n\n"+
		"[code](https://github.com/o/r/blob/main/internal/cache/lru.go) "+
		"[dir](https://github.com/o/r/tree/main/internal) "+
		"[page](https://site.example-host.dev/b/concepts/cache/) "+
		"[anchor](https://site.example-host.dev/b/concepts/cache/#eviction) "+
		"[away](https://buzz-lang.dev/x/y)\n")
	writeFile(t, root, "internal/cache/policy.go", "package cache\n")
	writeFile(t, root, "internal/cache/lru.go", "package cache\n\n"+
		"// Upstream: https://buzz-lang.dev/x/y\n"+
		"// Concept: https://site.example-host.dev/b/concepts/cache/\n"+
		"// Anchor: https://site.example-host.dev/b/concepts/cache/#eviction\n"+
		"// Sibling: https://github.com/o/r/blob/main/internal/cache/policy.go\n"+
		"// Tree: https://github.com/o/r/tree/main/internal\n")

	docShard, cites := assembleDocs(root, nil, nil, "")
	links := assembleLinks(root, nil, newDocIndex(docShard.Nodes), cites)
	g := mergeAll([]Shard{docShard, links})

	// Every endpoint kind the classifier can reach has to be present, or an empty
	// UndeclaredEdges result would only mean the fixture missed a shape.
	kinds := map[string]bool{}
	for _, e := range links.Edges {
		if n, ok := nodeByID(g.Output(), e.Target); ok {
			kinds[n.Kind] = true
		}
	}
	for _, want := range []string{types.KindLink, types.KindDoc, types.KindDocSection, types.KindFile, types.KindDir} {
		assert.True(t, kinds[want], "the fixture must reach a %s endpoint", want)
	}
	assert.Empty(t, g.UndeclaredEdges(), "every citation edge must be a declared endpoint shape")
}

// TestCitedRankLiftsOnlyCitedProse pins the boost's narrowness: a page code points at
// ranks above one it does not, and neither displaces a domain entity.
func TestCitedRankLiftsOnlyCitedProse(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: docID("docs/a.md"), Kind: types.KindDoc, Label: "docs/a.md"})
	g.AddNode(types.KnowledgeNode{ID: docID("docs/b.md"), Kind: types.KindDoc, Label: "docs/b.md"})
	g.AddNode(types.KnowledgeNode{ID: fileID("x.go"), Kind: types.KindFile, Label: "x.go"})
	g.AddEdge(extractedEdge(fileID("x.go"), docID("docs/a.md"), types.RelationReferences, "x.go:1"))

	assert.Equal(t, citedDocRank, g.citedRank(docID("docs/a.md"), types.KindDoc))
	assert.Equal(t, 0, g.citedRank(docID("docs/b.md"), types.KindDoc))
	assert.Equal(t, 0, g.citedRank(fileID("x.go"), types.KindFile), "the boost is for prose, not for the citing code")
	assert.Less(t, citedDocRank, kindRank(types.KindTarget), "a cited page must still lose to the entity a reader named")
}
