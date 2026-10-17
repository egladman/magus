package textembed

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
)

// SearchOptions bounds a Search. Depth is how many hits it returns at most, and must be
// at least 1. Allow, when not nil, is a bitset over doc ordinals (bit i%64 of
// Allow[i/64]) of the docs Search may return, one word per 64 docs.
type SearchOptions struct {
	Depth int
	Allow []uint64
}

// Hit is one ranked document: its ordinal and int64(Σ q.Q[j]·Q[j])·InvNorm, its cosine
// with the query up to a factor constant within one query.
type Hit struct {
	Doc   int
	Score int64
}

// Search scores every allowed doc against q exactly and returns the best Depth, by
// score descending, then Doc ascending. It reads every row, split across up to
// GOMAXPROCS goroutines. Docs with no Vector never come back. Every step is integer
// and the order total, so the hits are identical on every architecture and for any
// GOMAXPROCS. A Depth below 1, an Allow of the wrong length or with bits past Len(),
// or a q of other dims panics; so does a failed read of the rows, which for an index
// from ReadIndex means its ReaderAt failed.
func (ix *Index) Search(q Vector, opts SearchOptions) []Hit {
	ix.checkSearch(q, opts)
	return ix.search(q.Q, opts.Allow, opts.Depth, ix.shards())
}

func (ix *Index) checkSearch(q Vector, opts SearchOptions) {
	if opts.Depth < 1 {
		panic(fmt.Sprintf("textembed: Search: depth %d is below 1", opts.Depth))
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

// minShardDocs is the fewest docs a shard scans, so a small index is not split into
// goroutines that cost more than their scan.
const minShardDocs = 4096

func (ix *Index) shards() int {
	return max(1, min(runtime.GOMAXPROCS(0), ix.n/minShardDocs))
}

// search splits the docs into shards contiguous ranges scanned at once. Each shard
// keeps its best depth by compareHits, a total order, so the best depth of their union
// are the best depth overall: the hits do not depend on shards.
func (ix *Index) search(q []int8, allow []uint64, depth, shards int) []Hit {
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

// scanBlock is how many rows a shard reads with one ReadAt.
const scanBlock = 256

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

func (ix *Index) eligible(doc int, allow []uint64) bool {
	return ix.invNorm[doc] != 0 && (len(allow) == 0 || allow[doc/64]>>(doc%64)&1 == 1)
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
