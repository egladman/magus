package rankeval

import (
	"bufio"
	"cmp"
	"container/heap"
	"fmt"
	"hash/fnv"
	"io"
	"iter"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Hit is one ranked document of a TREC run.
type Hit struct {
	Doc   string
	Score float64
}

// TRECRun is one system's ranking per query id, in the order Rank gives. Tag is the
// run's name, trec_eval's last column.
type TRECRun struct {
	Tag     string
	Queries map[string][]Hit
}

// ReadRun parses trec_eval's run format, one "qid Q0 docno rank score tag" line per
// hit. Like trec_eval it ignores the rank column and orders each query by score; ties
// go by Rank's hash, not trec_eval's docno descending, so every arm in a comparison
// breaks ties one way. A line without six fields, an unparsable rank or score, a
// second tag, or a document ranked twice for one query is an error naming the line.
func ReadRun(r io.Reader) (TRECRun, error) {
	run := TRECRun{Queries: map[string][]Hit{}}
	seen := map[[2]string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 6 {
			return TRECRun{}, fmt.Errorf("run line %d: %d fields, want qid Q0 docno rank score tag", n, len(f))
		}
		if rank, err := strconv.Atoi(f[3]); err != nil || rank < 0 {
			return TRECRun{}, fmt.Errorf("run line %d: rank %q is not a non-negative integer", n, f[3])
		}
		score, err := strconv.ParseFloat(f[4], 64)
		if err != nil || math.IsNaN(score) {
			return TRECRun{}, fmt.Errorf("run line %d: score %q is not a number", n, f[4])
		}
		switch {
		case run.Tag == "":
			run.Tag = f[5]
		case run.Tag != f[5]:
			return TRECRun{}, fmt.Errorf("run line %d: tag %q in a run tagged %q", n, f[5], run.Tag)
		}
		key := [2]string{f[0], f[2]}
		if seen[key] {
			return TRECRun{}, fmt.Errorf("run line %d: query %s ranks %s twice", n, f[0], f[2])
		}
		seen[key] = true
		run.Queries[f[0]] = append(run.Queries[f[0]], Hit{Doc: f[2], Score: score})
	}
	if err := sc.Err(); err != nil {
		return TRECRun{}, err
	}
	for q, hits := range run.Queries {
		run.Queries[q] = Rank(hits, len(hits))
	}
	return run, nil
}

// WriteRun writes run in trec_eval's run format, queries in id order and each query's
// hits in Rank order with ranks from 1. Scores print in the shortest form that parses
// back to the same float64, so ReadRun returns run unchanged. An empty tag, or a tag,
// query id or document id holding white space, is an error.
func WriteRun(w io.Writer, run TRECRun) error {
	if run.Tag == "" || strings.ContainsFunc(run.Tag, unicode.IsSpace) {
		return fmt.Errorf("run tag %q is empty or holds white space", run.Tag)
	}
	bw := bufio.NewWriter(w)
	for _, q := range slices.Sorted(maps.Keys(run.Queries)) {
		if q == "" || strings.ContainsFunc(q, unicode.IsSpace) {
			return fmt.Errorf("query id %q is empty or holds white space", q)
		}
		for i, h := range Rank(slices.Clone(run.Queries[q]), len(run.Queries[q])) {
			if h.Doc == "" || strings.ContainsFunc(h.Doc, unicode.IsSpace) {
				return fmt.Errorf("query %s: document id %q is empty or holds white space", q, h.Doc)
			}
			fmt.Fprintf(bw, "%s Q0 %s %d %s %s\n", q, h.Doc, i+1, strconv.FormatFloat(h.Score, 'g', -1, 64), run.Tag)
		}
	}
	return bw.Flush()
}

// CompareHits orders a before b when its score is higher; equal scores go by the
// FNV-1a hash of the document id, then the id itself. The hash depends on the id
// alone, so no arm and no id prefix wins ties systematically (Cabanac et al., CLEF
// 2010; Lin and Yang, SIGIR 2019).
func CompareHits(a, b Hit) int {
	if c := cmp.Compare(b.Score, a.Score); c != 0 {
		return c
	}
	if c := cmp.Compare(tieHash(a.Doc), tieHash(b.Doc)); c != 0 {
		return c
	}
	return strings.Compare(a.Doc, b.Doc)
}

// Rank sorts hits in place by CompareHits and returns the first k.
func Rank(hits []Hit, k int) []Hit {
	slices.SortFunc(hits, CompareHits)
	return hits[:min(k, len(hits))]
}

// Top returns the first k of hits by CompareHits, in that order, holding at most k
// hits at once, so a ranker can score a whole corpus without sorting it.
func Top(hits iter.Seq[Hit], k int) []Hit {
	if k < 1 {
		return nil
	}
	worst := &worstFirst{}
	for h := range hits {
		switch {
		case len(*worst) < k:
			heap.Push(worst, h)
		case CompareHits(h, (*worst)[0]) < 0:
			(*worst)[0] = h
			heap.Fix(worst, 0)
		}
	}
	return Rank(*worst, k)
}

// worstFirst is a heap whose root is the hit CompareHits ranks last.
type worstFirst []Hit

func (w worstFirst) Len() int           { return len(w) }
func (w worstFirst) Less(i, j int) bool { return CompareHits(w[i], w[j]) > 0 }
func (w worstFirst) Swap(i, j int)      { w[i], w[j] = w[j], w[i] }
func (w *worstFirst) Push(x any) {
	hit, ok := x.(Hit)
	if !ok {
		panic("rankeval: heap requires a Hit")
	}
	*w = append(*w, hit)
}
func (w *worstFirst) Pop() any {
	old := *w
	h := old[len(old)-1]
	*w = old[:len(old)-1]
	return h
}

func tieHash(doc string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(doc))
	return h.Sum64()
}

// MeanInterval is the mean of values and its 95% Student t interval. It panics on
// fewer than 2 values.
func MeanInterval(values []float64) (mean, lo, hi float64) {
	return pairedTInterval(values, make([]float64, len(values)), 0.95)
}

// WilsonInterval is the 95% Wilson score interval of a binomial proportion
// successes/n (Wilson 1927), which stays inside [0, 1] where the normal
// approximation does not. It panics unless 0 <= successes <= n and n > 0.
func WilsonInterval(successes, n int) (lo, hi float64) {
	if n <= 0 || successes < 0 || successes > n {
		panic(fmt.Sprintf("wilson interval of %d successes in %d trials", successes, n))
	}
	z := normalQuantile(0.975)
	p, nf := float64(successes)/float64(n), float64(n)
	center := (p + z*z/(2*nf)) / (1 + z*z/nf)
	half := z / (1 + z*z/nf) * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf))
	return max(0, center-half), min(1, center+half)
}
