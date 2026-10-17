package textembed

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"runtime"
	"slices"
	"sync"
)

// SearchOptions bounds a Search. Depth is how many hits it returns at most, and must be
// at least 1. Rescore is how many sign-code candidates it scores exactly: 0 means
// max(512, 4·Depth), and a value at or above Len() makes the search exact. Allow, when
// not nil, is a bitset over doc ordinals (bit i%64 of Allow[i/64]) of the docs Search
// may return, one word per 64 docs.
type SearchOptions struct {
	Depth, Rescore int
	Allow          []uint64
}

// Hit is one ranked document: its ordinal and int64(Σ q.Q[j]·Q[j])·InvNorm, its cosine
// with the query up to a factor constant within one query.
type Hit struct {
	Doc   int
	Score int64
}

// Search ranks the index's documents against q in two stages. The first scores every
// allowed doc's sign code against q's int8 direction, Σ q.Q[j] over the bits the code
// sets, and keeps the Rescore highest, ties taken in doc order. The second scores
// those exactly and returns the best Depth, by score descending, then Doc ascending.
// Docs with no Vector never come back. Every step is integer and its order fixed, so
// the hits are identical on every architecture. q.Code is not read. A Depth below 1,
// a negative Rescore, an Allow of the wrong length or with bits past Len(), or a q of
// other dims panics; so does a failed read of the rows, which for an index from
// ReadIndex means its ReaderAt failed.
func (ix *Index) Search(q Vector, opts SearchOptions) []Hit {
	ix.checkSearch(q, opts)
	r := opts.Rescore
	if r == 0 {
		r = max(512, 4*opts.Depth)
	}
	if r >= ix.n {
		return ix.exact(q.Q, opts.Allow, opts.Depth, ix.shards())
	}
	return ix.rescore(q.Q, ix.candidates(q.Q, opts.Allow, r), opts.Depth)
}

// minShardDocs is the fewest docs a shard of an exact search scans, so a small index
// is not split into goroutines that cost more than their scan.
const minShardDocs = 4096

func (ix *Index) shards() int {
	return max(1, min(runtime.GOMAXPROCS(0), ix.n/minShardDocs))
}

// scanBlock is how many rows a shard reads with one ReadAt.
const scanBlock = 256

// exact scores every eligible doc, split into shards contiguous ranges of docs that
// run at once. Each shard keeps its best depth by compareHits, a total order, so the
// best depth of their union are the best depth overall: the hits do not depend on
// shards.
func (ix *Index) exact(q []int8, allow []uint64, depth, shards int) []Hit {
	tops := make([][]Hit, shards)
	errs := make([]error, shards)
	var wg sync.WaitGroup
	for s := range shards {
		wg.Go(func() {
			tops[s], errs[s] = ix.scan(q, allow, s*ix.n/shards, (s+1)*ix.n/shards, depth)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		panic(fmt.Sprintf("textembed: Search: %v", err))
	}
	all := slices.Concat(tops...)
	slices.SortFunc(all, compareHits)
	return all[:min(depth, len(all))]
}

// scan returns the best depth eligible docs in [lo, hi), unordered.
func (ix *Index) scan(q []int8, allow []uint64, lo, hi, depth int) ([]Hit, error) {
	top := make([]Hit, 0, min(depth, hi-lo))
	buf := make([]byte, min(scanBlock, hi-lo)*ix.dims)
	for start := lo; start < hi; start += scanBlock {
		end := min(start+scanBlock, hi)
		block := buf[:(end-start)*ix.dims]
		if err := readAt(ix.rows, block, int64(start)*int64(ix.dims)); err != nil {
			return nil, fmt.Errorf("read rows of docs %d-%d: %w", start, end-1, err)
		}
		for doc := start; doc < end; doc++ {
			if !ix.eligible(doc, allow) {
				continue
			}
			row := block[(doc-start)*ix.dims : (doc-start+1)*ix.dims]
			top = keepBest(top, Hit{Doc: doc, Score: int64(dot(q, row)) * int64(ix.invNorm[doc])}, depth)
		}
	}
	return top, nil
}

// dot is Σ q[j]·int8(row[j]) over len(q) dims, a multiple of 64. |q[j]|, |int8(row[j])|
// ≤ 127 and len(q) ≤ maxDims keep it inside int32.
//
// optimization: one 8-byte load per 8 row bytes, and two sums, cut the loads and break
// the add chain one sum makes.
//
//	measured (one goroutine, a whole scan of 100k rows of 256 dims, Apple M5, n=10,
//	median ± 95% CI): one sum 7.99 ms ± 0%, this 4.70 ms ± 1%; four sums of byte
//	loads 5.00 ms ± 5%, eight 5.11 ms ± 8%.
func dot(q []int8, row []byte) int32 {
	row = row[:len(q)]
	var s0, s1 int32
	for j := 0; j+8 <= len(q); j += 8 {
		qq := q[j : j+8 : j+8]
		w := binary.LittleEndian.Uint64(row[j:])
		s0 += int32(qq[0])*int32(int8(w)) + int32(qq[1])*int32(int8(w>>8)) +
			int32(qq[2])*int32(int8(w>>16)) + int32(qq[3])*int32(int8(w>>24))
		s1 += int32(qq[4])*int32(int8(w>>32)) + int32(qq[5])*int32(int8(w>>40)) +
			int32(qq[6])*int32(int8(w>>48)) + int32(qq[7])*int32(int8(w>>56))
	}
	return s0 + s1
}

// keepBest adds h to top, a heap of at most depth hits whose root is the worst kept,
// when there is room or h beats the root.
func keepBest(top []Hit, h Hit, depth int) []Hit {
	switch {
	case len(top) < depth:
		top = append(top, h)
		siftUp(top, len(top)-1)
	case compareHits(h, top[0]) < 0:
		top[0] = h
		siftDown(top, 0)
	}
	return top
}

func (ix *Index) checkSearch(q Vector, opts SearchOptions) {
	if opts.Depth < 1 {
		panic(fmt.Sprintf("textembed: Search: depth %d is below 1", opts.Depth))
	}
	if opts.Rescore < 0 {
		panic(fmt.Sprintf("textembed: Search: rescore %d is negative", opts.Rescore))
	}
	if len(q.Q) != ix.dims {
		panic(fmt.Sprintf("textembed: Search: q has %d dims, the index %d", len(q.Q), ix.dims))
	}
	if opts.Allow == nil {
		return
	}
	if want := (ix.n + 63) / 64; len(opts.Allow) != want {
		panic(fmt.Sprintf("textembed: Search: allow has %d words, %d docs need %d", len(opts.Allow), ix.n, want))
	}
	if tail := ix.n % 64; tail != 0 && opts.Allow[len(opts.Allow)-1]>>tail != 0 {
		panic(fmt.Sprintf("textembed: Search: allow sets bits past doc %d", ix.n-1))
	}
}

func (ix *Index) eligible(doc int, allow []uint64) bool {
	return ix.invNorm[doc] != 0 && (len(allow) == 0 || allow[doc/64]>>(doc%64)&1 == 1)
}

// candidates returns, in doc order, the r eligible docs whose sign codes score highest
// against q: every doc above a threshold T, then those at exactly T in doc order until
// r are taken. It finds T from a histogram of the scores, so it needs no sort.
//
// Scoring q's int8 values against a doc's bits, rather than q's own sign bits by
// Hamming distance, keeps the magnitudes the query carries.
//
//	measured (96k docs of 256 dims, the Zipf doc set of search_test.go, 100 queries):
//	top-10 overlap with exact search at rescore 512 rises 0.834 -> 0.981, and top-100
//	at rescore 2048 0.741 -> 0.940.
func (ix *Index) candidates(q []int8, allow []uint64, r int) []int32 {
	lut := byteSums(q)
	var lo, hi int32
	for _, x := range q {
		if x < 0 {
			lo += int32(x)
		} else {
			hi += int32(x)
		}
	}
	// score[doc] is the doc's sign-code score less lo, so 0 or more, or -1 when the doc
	// is not eligible.
	score := make([]int32, ix.n)
	hist := make([]int32, hi-lo+1)
	live := 0
	for doc := range ix.n {
		if !ix.eligible(doc, allow) {
			score[doc] = -1
			continue
		}
		s := -lo
		for k, c := range ix.code(doc) {
			t := lut[8*k : 8*k+8 : 8*k+8]
			s += int32(t[0][byte(c)]) + int32(t[1][byte(c>>8)]) +
				int32(t[2][byte(c>>16)]) + int32(t[3][byte(c>>24)]) +
				int32(t[4][byte(c>>32)]) + int32(t[5][byte(c>>40)]) +
				int32(t[6][byte(c>>48)]) + int32(t[7][byte(c>>56)])
		}
		score[doc] = s
		hist[s]++
		live++
	}
	threshold, room := topThreshold(hist, r, live)
	out := make([]int32, 0, min(r, live))
	for doc, s := range score {
		switch {
		case s > threshold:
			out = append(out, int32(doc))
		case s == threshold && room > 0:
			out = append(out, int32(doc))
			room--
		}
	}
	return out
}

// topThreshold reads, from hist of the live scores, the score T the r highest reach:
// every score above T is taken, and room more at exactly T. When r covers every live
// doc, T is -1 and every eligible doc is above it.
func topThreshold(hist []int32, r, live int) (threshold int32, room int) {
	if r >= live {
		return -1, 0
	}
	above := 0
	threshold = int32(len(hist) - 1)
	for above+int(hist[threshold]) < r {
		above += int(hist[threshold])
		threshold--
	}
	return threshold, r - above
}

// byteSums tabulates, for each run of 8 dims, Σ q[j] over every subset of the run:
// entry [m][b] sums q[8m+t] over the bits t that b sets, so one lookup per code byte
// scores 8 dims.
func byteSums(q []int8) [][256]int16 {
	lut := make([][256]int16, len(q)/8)
	for m := range lut {
		for b := 1; b < 256; b++ {
			lut[m][b] = lut[m][b&(b-1)] + int16(q[8*m+bits.TrailingZeros8(uint8(b))])
		}
	}
	return lut
}

// rescore scores each candidate exactly and keeps the best depth in a heap whose root
// is the worst kept.
func (ix *Index) rescore(q []int8, cands []int32, depth int) []Hit {
	top := make([]Hit, 0, min(depth, len(cands)))
	row := make([]byte, ix.dims)
	for _, doc := range cands {
		if err := readAt(ix.rows, row, int64(doc)*int64(ix.dims)); err != nil {
			panic(fmt.Sprintf("textembed: Search: read row of doc %d: %v", doc, err))
		}
		var dot int32
		for j, b := range row {
			dot += int32(int8(b)) * int32(q[j])
		}
		h := Hit{Doc: int(doc), Score: int64(dot) * int64(ix.invNorm[doc])}
		switch {
		case len(top) < depth:
			top = append(top, h)
			siftUp(top, len(top)-1)
		case compareHits(h, top[0]) < 0:
			top[0] = h
			siftDown(top, 0)
		}
	}
	slices.SortFunc(top, compareHits)
	return top
}

// compareHits orders hits best first: score descending, then doc ascending.
func compareHits(a, b Hit) int {
	if c := cmp.Compare(b.Score, a.Score); c != 0 {
		return c
	}
	return cmp.Compare(a.Doc, b.Doc)
}

func siftUp(h []Hit, i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if compareHits(h[i], h[parent]) <= 0 {
			return
		}
		h[i], h[parent] = h[parent], h[i]
		i = parent
	}
}

func siftDown(h []Hit, i int) {
	for {
		worst := i
		for _, c := range [2]int{2*i + 1, 2*i + 2} {
			if c < len(h) && compareHits(h[c], h[worst]) > 0 {
				worst = c
			}
		}
		if worst == i {
			return
		}
		h[i], h[worst] = h[worst], h[i]
		i = worst
	}
}
