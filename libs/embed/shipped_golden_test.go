package embed

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// updateGolden names the variable that rewrites the golden files from this build.
const updateGolden = "UPDATE_EMBED_GOLDEN"

var (
	goldenTablePath   = filepath.Join("testdata", "golden-table.mset")
	goldenRankingPath = filepath.Join("testdata", "golden.json")
)

var goldenDocs = []struct{ id, text string }{
	{"bm25f", "func (ix *Index) Score(terms []string) []Hit scores BM25F over every field"},
	{"fuse", "Fuse merges ranked lists by weighted reciprocal rank fusion"},
	{"pagerank", "PersonalizedPageRank walks the graph from seeds with restart"},
	{"pull", "Pull fetches the embedding table layer into the cache by rename"},
	{"load", "Load reads the cached table and checks its sha256 against the pin"},
	{"tokens", "Tokens splits text into lowercase words and identifier parts"},
	{"search", "Search scores every allowed doc against the query vector exactly"},
	{"spell", "a spell declares ops that a target runs in the sandbox"},
	{"cache", "the build cache keys each target by the digest of its inputs"},
	{"lease", "a lease binds a job to its write paths in one worktree"},
	{"guard", "the guard denies a command that pipes magus output"},
	{"weights", "weights.json holds the BM25F field weights and the fusion lists"},
}

var goldenQueries = []string{
	"rank fusion",
	"embedding table cache",
	"BM25F field weights",
	"PersonalizedPageRank seeds",
	"sha256 pin",
	"identifier parts",
	"query vector",
	"target sandbox",
	"write paths",
	"pipes output",
}

type goldenQuery struct {
	Query   string   `json:"query"`
	InvNorm uint32   `json:"inv_norm"`
	Q       []int8   `json:"q"`
	Ranked  []string `json:"ranked"`
}

// goldenSpec is a 64-dim table over every token of the docs and queries, derived as
// testSpec derives rows, so it is the same on every machine.
func goldenSpec() TableSpec {
	var vocab []string
	for _, text := range slices.Concat(goldenQueries, docTexts()) {
		for _, tok := range Tokens(text) {
			if Tokens(tok)[0] == tok {
				vocab = append(vocab, tok)
			}
		}
	}
	slices.Sort(vocab)
	return testSpec(slices.Compact(vocab), 64)
}

func docTexts() []string {
	out := make([]string, len(goldenDocs))
	for i, d := range goldenDocs {
		out[i] = d.text
	}
	return out
}

func goldenRanking(t *testing.T, tbl *Table) []goldenQuery {
	t.Helper()
	docs := make([][]Field, len(goldenDocs))
	for i, d := range goldenDocs {
		docs[i] = []Field{{Tokens: Tokens(d.text), Weight: 1}}
	}
	ix := NewIndex(tbl, docs)
	out := make([]goldenQuery, len(goldenQueries))
	for i, q := range goldenQueries {
		v, ok := tbl.Embed([]Field{{Tokens: Tokens(q), Weight: 1}})
		if !ok {
			t.Fatalf("query %q embeds to no vector", q)
		}
		out[i] = goldenQuery{Query: q, InvNorm: v.InvNorm, Q: v.Q}
		for _, h := range ix.Search(v, SearchOptions{Depth: len(goldenDocs)}) {
			out[i].Ranked = append(out[i].Ranked, goldenDocs[h.Doc].id)
		}
	}
	return out
}

// TestShippedGoldenTable pins the bytes EncodeTable writes for goldenSpec.
func TestShippedGoldenTable(t *testing.T) {
	data := encode(t, goldenSpec())
	if os.Getenv(updateGolden) != "" {
		if err := os.WriteFile(goldenTablePath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenTablePath)
	if err != nil {
		t.Fatalf("%v: regenerate with %s=1", err, updateGolden)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("EncodeTable wrote %d bytes that differ from %s", len(data), goldenTablePath)
	}
}

// TestShippedGoldenRanking pins the query vectors and rankings over the committed table.
// Embed and Search use no float, so these hold byte for byte on amd64 and arm64; a
// mismatch on one architecture only is a determinism bug, never a reason to update.
func TestShippedGoldenRanking(t *testing.T) {
	data, err := os.ReadFile(goldenTablePath)
	if err != nil {
		t.Fatal(err)
	}
	got := goldenRanking(t, read(t, data))
	if os.Getenv(updateGolden) != "" {
		out := []byte("[\n")
		for i, q := range got {
			line, err := json.Marshal(q)
			if err != nil {
				t.Fatal(err)
			}
			if i > 0 {
				out = append(out, ",\n"...)
			}
			out = append(out, line...)
		}
		if err := os.WriteFile(goldenRankingPath, append(out, "\n]\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(goldenRankingPath)
	if err != nil {
		t.Fatalf("%v: regenerate with %s=1", err, updateGolden)
	}
	var want []goldenQuery
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(goldenQueries) {
		t.Fatalf("%s holds %d queries, want %d", goldenRankingPath, len(want), len(goldenQueries))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("query %q:\n got %+v\nwant %+v", got[i].Query, got[i], want[i])
		}
	}
}
