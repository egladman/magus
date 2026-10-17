package textembed

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/textrank"
)

// docSet is a synthetic retrieval set. Token frequencies follow Zipf(1) by rank, and
// a token's w16 falls with its frequency, as SIF weighting makes it in a real table.
// Each query mixes tokens of one target doc with Zipf draws. It uses only sha256 and
// PCG's raw output, so it is the same on every machine and Go release.
type docSet struct {
	table   *Table
	docs    [][]Field
	queries []Vector
}

// noVectorEvery is how often a doc holds only an out-of-vocabulary token.
const noVectorEvery = 97

func newDocSet(vocab, docs, queries, dims int) docSet {
	rng := rand.New(rand.NewPCG(1, 2))
	ranked := docSetWords(rng, vocab)
	weight := make(map[string]uint16, vocab)
	cum := make([]uint64, vocab)
	var total uint64
	for i, w := range ranked {
		weight[w] = uint16(65535 * (i + 1) / (i + 1 + 64))
		total += (1 << 40) / uint64(i+1)
		cum[i] = total
	}
	draw := func() string {
		i, _ := slices.BinarySearch(cum, rng.Uint64()%total+1)
		return ranked[i]
	}
	drawN := func(lo, hi int) []string {
		out := make([]string, lo+int(rng.Uint64()%uint64(hi-lo+1)))
		for i := range out {
			out[i] = draw()
		}
		return out
	}

	spec := testSpec(slices.Sorted(slices.Values(ranked)), dims)
	for i, tok := range spec.Tokens {
		spec.Weights[i] = weight[tok]
	}
	var buf bytes.Buffer
	if err := EncodeTable(&buf, spec); err != nil {
		panic(err)
	}
	tbl, err := ReadTable(buf.Bytes())
	if err != nil {
		panic(err)
	}

	c := docSet{table: tbl, docs: make([][]Field, docs)}
	for i := range c.docs {
		if i%noVectorEvery == noVectorEvery-1 {
			c.docs[i] = []Field{{Tokens: []string{"zzzzzzzzzzzz"}, Weight: 1}}
			continue
		}
		c.docs[i] = []Field{{Tokens: drawN(1, 3), Weight: 2}, {Tokens: drawN(8, 64), Weight: 1}}
	}
	for len(c.queries) < queries {
		fields := c.docs[rng.Uint64()%uint64(docs)]
		body := fields[len(fields)-1].Tokens
		toks := drawN(1, 4)
		for range 1 + rng.Uint64()%4 {
			toks = append(toks, body[rng.Uint64()%uint64(len(body))])
		}
		if v, ok := tbl.Embed([]Field{{Tokens: toks, Weight: 1}}); ok {
			c.queries = append(c.queries, v)
		}
	}
	return c
}

// docSetWords is n distinct lowercase words that are fixed points of textrank.Tokens,
// most frequent first.
func docSetWords(rng *rand.Rand, n int) []string {
	seen := make(map[string]bool, n)
	out := make([]string, 0, n)
	for len(out) < n {
		x := rng.Uint64()
		w := make([]byte, 3+x%8)
		x >>= 3
		for i := range w {
			w[i] = byte('a' + x%26)
			x /= 26
		}
		if got := textrank.Tokens(string(w)); !seen[string(w)] && len(got) > 0 && got[0] == string(w) {
			seen[string(w)] = true
			out = append(out, string(w))
		}
	}
	return out
}

// bigDocSet is the shape the design budgets for: about 96k docs over the 29k-token
// table, at 256 dims.
var bigDocSet = sync.OnceValues(func() (docSet, *Index) {
	c := newDocSet(29000, 96000, 100, 256)
	return c, NewIndex(c.table, c.docs)
})

func smallDocSet(t testing.TB) (docSet, *Index) {
	t.Helper()
	c := newDocSet(500, 300, 20, 128)
	return c, NewIndex(c.table, c.docs)
}

// referenceSearch is Search by sorting: every eligible doc by its sign-code score
// descending, then doc, the first r of those by exact score, the first depth.
func referenceSearch(c docSet, q Vector, allow []uint64, depth, r int) []Hit {
	type cand struct{ doc, near int }
	var cands []cand
	vecs := make([]Vector, len(c.docs))
	for doc, fields := range c.docs {
		v, ok := c.table.Embed(fields)
		if !ok || (allow != nil && allow[doc/64]>>(doc%64)&1 == 0) {
			continue
		}
		vecs[doc] = v
		var near int
		for j, x := range q.Q {
			if v.Code[j/64]>>(j%64)&1 == 1 {
				near += int(x)
			}
		}
		cands = append(cands, cand{doc, near})
	}
	slices.SortFunc(cands, func(a, b cand) int {
		return cmp.Or(cmp.Compare(b.near, a.near), cmp.Compare(a.doc, b.doc))
	})
	hits := make([]Hit, 0, len(cands))
	for _, cd := range cands[:min(r, len(cands))] {
		hits = append(hits, Hit{Doc: cd.doc, Score: score(q, vecs[cd.doc])})
	}
	slices.SortFunc(hits, compareHits)
	return hits[:min(depth, len(hits))]
}

func TestSearchMatchesReference(t *testing.T) {
	c, ix := smallDocSet(t)
	for _, tt := range []struct{ depth, rescore int }{
		{1, 1}, {10, 1}, {10, 7}, {10, 40}, {40, 10}, {25, 299}, {25, 300}, {25, 10000}, {300, 0},
	} {
		t.Run(fmt.Sprintf("depth%d/rescore%d", tt.depth, tt.rescore), func(t *testing.T) {
			r := tt.rescore
			if r == 0 {
				r = max(512, 4*tt.depth)
			}
			for i, q := range c.queries {
				got := ix.Search(q, SearchOptions{Depth: tt.depth, Rescore: tt.rescore})
				if want := referenceSearch(c, q, nil, tt.depth, r); !slices.Equal(got, want) {
					t.Fatalf("query %d: Search = %v\nwant %v", i, got, want)
				}
			}
		})
	}
}

func TestSearchAllow(t *testing.T) {
	c, ix := smallDocSet(t)
	rng := rand.New(rand.NewPCG(3, 4))
	allow := make([]uint64, (ix.Len()+63)/64)
	for doc := range ix.Len() {
		if rng.Uint64()%3 == 0 {
			allow[doc/64] |= 1 << (doc % 64)
		}
	}
	for i, q := range c.queries {
		for _, r := range []int{5, 50, 1000} {
			got := ix.Search(q, SearchOptions{Depth: 20, Rescore: r, Allow: allow})
			for _, h := range got {
				if allow[h.Doc/64]>>(h.Doc%64)&1 == 0 {
					t.Fatalf("query %d: hit %d is not allowed", i, h.Doc)
				}
			}
			if want := referenceSearch(c, q, allow, 20, r); !slices.Equal(got, want) {
				t.Fatalf("query %d, rescore %d: Search = %v\nwant %v", i, r, got, want)
			}
		}
	}
	if got := ix.Search(c.queries[0], SearchOptions{Depth: 5, Allow: make([]uint64, len(allow))}); len(got) != 0 {
		t.Errorf("Search with nothing allowed = %v, want no hits", got)
	}
}

func TestSearchNeverReturnsADocWithNoVector(t *testing.T) {
	c, ix := smallDocSet(t)
	for _, q := range c.queries {
		for _, h := range ix.Search(q, SearchOptions{Depth: ix.Len(), Rescore: ix.Len()}) {
			if h.Doc%noVectorEvery == noVectorEvery-1 {
				t.Fatalf("hit %d has no vector", h.Doc)
			}
		}
	}
}

func TestSearchTieBreak(t *testing.T) {
	c, _ := smallDocSet(t)
	doc := c.docs[0]
	ix := NewIndex(c.table, [][]Field{c.docs[1], doc, c.docs[2], doc, doc})
	q, _ := c.table.Embed(doc)
	s := score(q, q)
	if got, want := ix.Search(q, SearchOptions{Depth: 3}), []Hit{{1, s}, {3, s}, {4, s}}; !slices.Equal(got, want) {
		t.Errorf("Search = %v, want %v", got, want)
	}
	// The three copies top the sign-code stage together, so a rescore of two keeps the
	// first two.
	if got, want := ix.Search(q, SearchOptions{Depth: 3, Rescore: 2}), []Hit{{1, s}, {3, s}}; !slices.Equal(got, want) {
		t.Errorf("Search with rescore 2 = %v, want %v", got, want)
	}
}

func TestSearchEmptyIndex(t *testing.T) {
	c, _ := smallDocSet(t)
	ix := NewIndex(c.table, nil)
	if got := ix.Search(c.queries[0], SearchOptions{Depth: 3}); len(got) != 0 {
		t.Errorf("Search over no docs = %v", got)
	}
	if got := ix.Search(c.queries[0], SearchOptions{Depth: 3, Allow: []uint64{}}); len(got) != 0 {
		t.Errorf("Search over no docs = %v", got)
	}
}

func TestSearchPanics(t *testing.T) {
	c, ix := smallDocSet(t)
	q := c.queries[0]
	words := (ix.Len() + 63) / 64
	tests := []struct {
		name string
		q    Vector
		opts SearchOptions
		want string
	}{
		{"zero depth", q, SearchOptions{}, "depth 0"},
		{"negative rescore", q, SearchOptions{Depth: 1, Rescore: -1}, "rescore -1"},
		{"short allow", q, SearchOptions{Depth: 1, Allow: make([]uint64, words-1)}, "allow has"},
		{"long allow", q, SearchOptions{Depth: 1, Allow: make([]uint64, words+1)}, "allow has"},
		{"allow past the last doc", q, SearchOptions{Depth: 1, Allow: append(make([]uint64, words-1), 1<<63)}, "past doc"},
		{"no vector", Vector{}, SearchOptions{Depth: 1}, "dims"},
		{"other dims", Vector{Q: make([]int8, 64), Code: make([]uint64, 1)}, SearchOptions{Depth: 1}, "dims"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := fmt.Sprint(recover()); !strings.Contains(r, tt.want) {
					t.Errorf("panic = %q, want one naming %q", r, tt.want)
				}
			}()
			ix.Search(tt.q, tt.opts)
		})
	}
}

// failingRows serves the head of an encoded index and fails every read of its rows.
type failingRows struct {
	data []byte
	rows int64
}

func (f failingRows) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.rows {
		return 0, errWrite
	}
	return copy(p, f.data[off:]), nil
}

func TestSearchPanicsOnAFailedRead(t *testing.T) {
	c, ix := smallDocSet(t)
	data := writeIndex(t, ix)
	back, err := ReadIndex(failingRows{data, int64(len(data) - ix.Len()*128)}, int64(len(data)), c.table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := fmt.Sprint(recover()); !strings.Contains(r, errWrite.Error()) {
			t.Errorf("panic = %q, want the read error", r)
		}
	}()
	back.Search(c.queries[0], SearchOptions{Depth: 1})
}

func hitsDigest(hits []Hit) string {
	h := sha256.New()
	for _, hit := range hits {
		h.Write(binary.LittleEndian.AppendUint32(nil, uint32(hit.Doc)))
		h.Write(binary.LittleEndian.AppendUint64(nil, uint64(hit.Score)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestSearchGolden pins hits over the big doc set. Search uses no float and a fixed
// order, so they hold byte for byte on amd64 and arm64; a mismatch on one
// architecture only is a determinism bug, never a reason to update want.
func TestSearchGolden(t *testing.T) {
	c, ix := bigDocSet()
	tests := []struct {
		name string
		opts SearchOptions
		want string
	}{
		{"default", SearchOptions{Depth: 10}, "8a123d00ba49e3b3473fa154ab9856d17262a1284f2e8e88e0982c022ab8dd5c"},
		{"rescore 256", SearchOptions{Depth: 100, Rescore: 256}, "8a17e8c485ce59609ab305b275fdae553ea32a66d5e5146605cb0af812cd2345"},
		{"exact", SearchOptions{Depth: 10, Rescore: ix.Len()}, "cde51efc7be9c8d373b82c081396512d2d405bd9ec01b89417ae2c0857c2ddf0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var all []Hit
			for _, q := range c.queries[:10] {
				all = append(all, ix.Search(q, tt.opts)...)
			}
			if got := hitsDigest(all); got != tt.want {
				t.Errorf("hits digest = %q, want %q", got, tt.want)
			}
		})
	}
}

// overlap is |a ∩ b| / len(a).
func overlap(a, b []Hit) float64 {
	in := make(map[int]bool, len(b))
	for _, h := range b {
		in[h.Doc] = true
	}
	var n int
	for _, h := range a {
		if in[h.Doc] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

// TestSearchRecall measures how much of exact search's top 100 and top 10 the
// sign-code stage keeps over the big doc set, per rescore depth. The floors sit just
// under the measured means, so a change that loses recall fails here.
func TestSearchRecall(t *testing.T) {
	c, ix := bigDocSet()
	exact := make([][]Hit, len(c.queries))
	for i, q := range c.queries {
		exact[i] = ix.Search(q, SearchOptions{Depth: 100, Rescore: ix.Len()})
	}
	for _, tt := range []struct {
		rescore       int
		top100, top10 float64
	}{
		{256, 0.640, 0.962},
		{512, 0.767, 0.980},
		{1024, 0.867, 0.991},
		{2048, 0.939, 0.998},
	} {
		var top100, top10, worst100 float64
		worst100 = 1
		for i, q := range c.queries {
			got := ix.Search(q, SearchOptions{Depth: 100, Rescore: tt.rescore})
			o := overlap(exact[i], got)
			top100 += o
			worst100 = min(worst100, o)
			top10 += overlap(exact[i][:10], got[:10])
		}
		top100 /= float64(len(c.queries))
		top10 /= float64(len(c.queries))
		t.Logf("rescore %4d: top-100 overlap %.4f (worst query %.2f), top-10 %.4f", tt.rescore, top100, worst100, top10)
		if top100 < tt.top100 || top10 < tt.top10 {
			t.Errorf("rescore %d: top-100 %.4f, top-10 %.4f; want at least %.4f, %.4f", tt.rescore, top100, top10, tt.top100, tt.top10)
		}
	}
}

// fileIndex writes ix to a file and reads it back, so Search reads rows with pread.
func fileIndex(b *testing.B, c docSet, ix *Index) *Index {
	b.Helper()
	path := filepath.Join(b.TempDir(), "vectors.idx")
	if err := os.WriteFile(path, writeIndex(b, ix), 0o600); err != nil {
		b.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { f.Close() })
	info, err := f.Stat()
	if err != nil {
		b.Fatal(err)
	}
	back, err := ReadIndex(f, info.Size(), c.table)
	if err != nil {
		b.Fatal(err)
	}
	return back
}

// BenchmarkSearch runs the big doc set's queries in turn: about 96k docs of 256 dims,
// rows in memory and in a file in the page cache.
func BenchmarkSearch(b *testing.B) {
	c, mem := bigDocSet()
	file := fileIndex(b, c, mem)
	for _, src := range []struct {
		name string
		ix   *Index
	}{{"mem", mem}, {"file", file}} {
		for _, tt := range []struct {
			name string
			opts SearchOptions
		}{
			{"depth10", SearchOptions{Depth: 10}},
			{"depth100/rescore2048", SearchOptions{Depth: 100, Rescore: 2048}},
			{"exact", SearchOptions{Depth: 10, Rescore: mem.Len()}},
		} {
			b.Run(src.name+"/"+tt.name, func(b *testing.B) {
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					src.ix.Search(c.queries[i%len(c.queries)], tt.opts)
					i++
				}
			})
		}
	}
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			mem.candidates(c.queries[i%len(c.queries)].Q, nil, 512)
			i++
		}
	})
}

func BenchmarkReadIndex(b *testing.B) {
	c, mem := bigDocSet()
	path := filepath.Join(b.TempDir(), "vectors.idx")
	if err := os.WriteFile(path, writeIndex(b, mem), 0o600); err != nil {
		b.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ReadIndex(f, info.Size(), c.table); err != nil {
			b.Fatal(err)
		}
	}
}
