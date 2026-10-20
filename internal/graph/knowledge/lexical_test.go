package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/types"
)

const lexicalSymbolID = "symbol:gomod example.com/m `example.com/m/store`/Store#readLexicalIndex()."

func lexicalDocsShard(body string) Shard {
	return Shard{
		Name: docsShardName,
		Nodes: []types.KnowledgeNode{
			{ID: "doc:docs/guide.md", Kind: types.KindDoc, Label: "docs/guide.md", Source: "docs/guide.md"},
			{ID: "docsection:docs/guide.md#setup", Kind: types.KindDocSection, Label: "Setup", Source: "docs/guide.md#setup"},
		},
		Text: map[string]string{"docsection:docs/guide.md#setup": body},
	}
}

func lexicalSymbolShard(doc string) Shard {
	return Shard{
		Name: "pkg/a" + symbolsShardSuffix,
		Nodes: []types.KnowledgeNode{{
			ID: lexicalSymbolID, Kind: types.KindSymbol, Label: "readLexicalIndex", Source: "store/lexical.go:42",
			Attrs: map[string]string{AttrDoc: doc, AttrSignature: "func (s *Store) readLexicalIndex(man *manifest) *textrank.Index"},
		}},
	}
}

func lexicalWeights(t *testing.T) textrank.BM25F {
	t.Helper()
	w, err := textrank.LoadWeights()
	require.NoError(t, err)
	return w.BM25F
}

// hitIDs ranks query over ix and returns the matching ids in rank order.
func hitIDs(t *testing.T, ix *textrank.Index, query string) []string {
	t.Helper()
	var ids []string
	for _, h := range ix.Score(textrank.Tokens(query), lexicalWeights(t)) {
		ids = append(ids, h.ID)
	}
	return ids
}

// syncLexical syncs shards for classes (nil: every class) into store and returns the index
// the resulting manifest binds.
func syncLexical(t *testing.T, s *Store, classes []ShardClass, shards ...Shard) *textrank.Index {
	t.Helper()
	fps := map[string]string{}
	for _, sh := range shards {
		fps[sh.Name] = fingerprintShardContent(sh)
	}
	require.NoError(t, s.syncClasses(context.Background(), shards, fps, syncPlan{classes: classes}))
	return s.readLexicalIndex(s.readManifestOrNil())
}

func TestLexicalDocumentSplitsIdentifiers(t *testing.T) {
	doc := lexicalDocument(lexicalSymbolShard("returns the stored index").Nodes[0], "")
	assert.Equal(t, lexicalSymbolID, doc.ID)
	assert.Subset(t, doc.Fields[textrank.FieldName], []string{"store", "readlexicalindex", "read", "lexical", "index"},
		"the id's last segment and the label split into their words")
	assert.Equal(t, []string{"store", "lexical", "go"}, doc.Fields[textrank.FieldPath], "the path drops its :line")
	assert.Equal(t, []string{"returns", "the", "stored", "index"}, doc.Fields[textrank.FieldDoc], "a symbol's doc comment is searchable")
	assert.Contains(t, doc.Fields[textrank.FieldSignature], "manifest")
	assert.NotContains(t, doc.Fields, textrank.FieldBody, "an empty field is left out")

	section := lexicalDocument(lexicalDocsShard("").Nodes[1], "Install the binary")
	assert.Equal(t, []string{"docs", "guide", "md"}, section.Fields[textrank.FieldPath], "the path drops its #anchor")
	assert.Equal(t, []string{"install", "the", "binary"}, section.Fields[textrank.FieldBody])
	assert.Equal(t, []string{"guide", "md", "setup", "setup"}, section.Fields[textrank.FieldName])
}

func TestLexicalDocumentKeepsTheBuzzErrorOperator(t *testing.T) {
	n := types.KnowledgeNode{
		ID:    "function:libs/gopherbuzz/std/fs.buzz:readFile",
		Kind:  "function",
		Attrs: map[string]string{AttrSignature: "fun readFile(path: str) > str !> any"},
	}
	doc := lexicalDocument(n, "")
	assert.Equal(t, []string{"fun", "readfile", "read", "file", "path", "str", "str", "!>", "any"}, doc.Fields[textrank.FieldSignature])
}

func TestLexicalSourcePath(t *testing.T) {
	for in, want := range map[string]string{
		"a/b.go:12":    "a/b.go",
		"a/b.go:12:3":  "a/b.go",
		"docs/x.md#y":  "docs/x.md",
		"c:/path/x.go": "c:/path/x.go",
		"magus.yaml":   "magus.yaml",
		"":             "",
	} {
		assert.Equal(t, want, sourcePath(in), in)
	}
}

func TestLexicalIndexHoldsSymbolsAndBodies(t *testing.T) {
	docs, sym := lexicalDocsShard("Install frobnicator first."), lexicalSymbolShard("returns the persisted ranking")
	ix := buildLexicalIndex([]Shard{sym, docs, {Name: "@session", Nodes: []types.KnowledgeNode{{ID: "session:x", Label: "frobnicator"}}}},
		map[string]string{"docsection:docs/guide.md#setup": "Install frobnicator first."})
	assert.Equal(t, 3, ix.Len(), "domain and symbol nodes, and no overlay node")
	assert.Equal(t, []string{"docsection:docs/guide.md#setup"}, hitIDs(t, ix, "frobnicator"), "a section body is searchable")
	assert.Equal(t, []string{lexicalSymbolID}, hitIDs(t, ix, "persisted ranking"), "a symbol doc comment is searchable")
	assert.Equal(t, []string{lexicalSymbolID}, hitIDs(t, ix, "lexical"))
}

func TestLexicalIndexKeepsTheFirstCopyOfASharedNode(t *testing.T) {
	a := Shard{Name: "pkg/a", Nodes: []types.KnowledgeNode{{ID: "spell:go", Label: "alpha"}}}
	b := Shard{Name: "pkg/b", Nodes: []types.KnowledgeNode{{ID: "spell:go", Label: "beta"}}}
	ix := buildLexicalIndex([]Shard{b, a}, nil)
	assert.Equal(t, 1, ix.Len())
	assert.Equal(t, []string{"spell:go"}, hitIDs(t, ix, "alpha"), "the first shard by name wins, as in the merged graph")
	assert.Empty(t, hitIDs(t, ix, "beta"))
}

func TestLexicalIndexSurvivesASymbolOnlyRebuild(t *testing.T) {
	s := NewStore(t.TempDir(), false, 0, nil, nil)
	ix := syncLexical(t, s, nil, lexicalDocsShard("Install frobnicator first."), lexicalSymbolShard("returns the persisted ranking"))
	require.NotNil(t, ix)
	assert.Equal(t, []string{"docsection:docs/guide.md#setup"}, hitIDs(t, ix, "frobnicator"))

	ix = syncLexical(t, s, []ShardClass{ClassSymbols}, lexicalSymbolShard("answers the quokka question"))
	require.NotNil(t, ix, "the rebuilt index binds the new manifest")
	assert.Equal(t, []string{"docsection:docs/guide.md#setup"}, hitIDs(t, ix, "frobnicator"),
		"the docs shard's bodies come from its sidecar")
	assert.Equal(t, []string{lexicalSymbolID}, hitIDs(t, ix, "quokka"))
	assert.Empty(t, hitIDs(t, ix, "persisted"), "the old symbol doc is gone")
}

func TestLexicalIndexBoundToShards(t *testing.T) {
	s := NewStore(t.TempDir(), false, 0, nil, nil)
	require.NotNil(t, syncLexical(t, s, nil, lexicalDocsShard("Install frobnicator first.")))

	ix := syncLexical(t, s, nil, lexicalDocsShard("Install wombat first."))
	require.NotNil(t, ix, "a body-only edit changes no fingerprint, yet rewrites the index")
	assert.Equal(t, []string{"docsection:docs/guide.md#setup"}, hitIDs(t, ix, "wombat"))
	assert.Empty(t, hitIDs(t, ix, "frobnicator"))

	man := s.readManifestOrNil()
	other := *man
	other.Shards = map[string]shardMeta{docsShardName: {Fingerprint: "elsewhere"}}
	assert.Nil(t, s.readLexicalIndex(&other), "an index built from other shards never binds")
	assert.Nil(t, s.readLexicalIndex(nil))

	require.NoError(t, os.Remove(s.lexicalIndexPath()))
	assert.Nil(t, s.readLexicalIndex(man), "an absent index reads as nil")
	assert.NotNil(t, syncLexical(t, s, nil, lexicalDocsShard("Install wombat first.")), "a sync with nothing moved restores a missing index")
}

func TestLexicalIndexRefusesAMissingSidecar(t *testing.T) {
	s := NewStore(t.TempDir(), false, 0, nil, nil)
	require.NotNil(t, syncLexical(t, s, nil, lexicalDocsShard("Install frobnicator first."), lexicalSymbolShard("x")))
	require.NoError(t, os.Remove(s.lexicalSidecarPath(s.shardPath(docsShardName))))

	assert.Nil(t, syncLexical(t, s, []ShardClass{ClassSymbols}, lexicalSymbolShard("y")),
		"without the docs sidecar the index would lose every body, so none is written")
	assert.NoFileExists(t, s.lexicalIndexPath())
}

func TestExportCarriesNoSectionBody(t *testing.T) {
	const body = "the zanzibar paragraph"
	cacheDir := t.TempDir()
	s := NewStore(cacheDir, false, 0, nil, nil)
	require.NotNil(t, syncLexical(t, s, nil, lexicalDocsShard(body)))

	export, err := ReadStoreExport(cacheDir)
	require.NoError(t, err)
	require.NotEmpty(t, export.Shards)
	for _, blob := range export.Shards {
		assert.NotContains(t, string(blob.Bytes), "zanzibar", "a published shard carries no body")
	}
	out, err := json.Marshal(mergeShards([]Shard{lexicalDocsShard(body)}).Output())
	require.NoError(t, err)
	assert.NotContains(t, string(out), "zanzibar", "the graph's output carries no body")

	sidecar, err := os.ReadFile(filepath.Join(StoreDir(cacheDir), lexicalSidecarDir, shardSlug(docsShardName)+".jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(sidecar), "zanzibar", "the body lives in the sidecar")
}
