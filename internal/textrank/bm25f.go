package textrank

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// indexFormat is the version WriteTo stamps and ReadIndex requires. Bump it on any
// change to the encoding below.
const indexFormat = 1

var indexMagic = [4]byte{'M', 'T', 'R', 'X'}

// Document is one thing to rank: its id and, per field, its tokens as Tokens produced
// them.
type Document struct {
	ID     string
	Fields map[string][]string
}

// Index holds per-field term frequencies and lengths for BM25F scoring. It is immutable
// once built and safe for concurrent Score calls.
type Index struct {
	ids    []string
	fields []string  // sorted field names; a posting's tf is indexed by position here
	lens   [][]int32 // lens[field][doc]
	avg    []float64 // average length per field over every document
	post   map[string][]posting
}

// posting is one document holding a term, with the term's frequency in each field.
type posting struct {
	doc int32
	tf  []int32
}

// Hit is one scored document. Matched counts the distinct query terms it holds; Terms
// carries each term's share of Score per field, query order then field order.
type Hit struct {
	ID      string
	Score   float64
	Matched int
	Terms   []TermScore
}

// TermScore is one term's contribution to a Hit from one field.
type TermScore struct {
	Term, Field string
	Score       float64
}

// NewIndex indexes docs. The result does not depend on the order of docs or of their
// fields; documents sharing an ID are merged into one.
func NewIndex(docs []Document) *Index {
	byID := make(map[string]map[string][]string, len(docs))
	fieldSet := map[string]struct{}{}
	for _, d := range docs {
		fields := byID[d.ID]
		if fields == nil {
			fields = map[string][]string{}
			byID[d.ID] = fields
		}
		for f, toks := range d.Fields {
			fields[f] = append(fields[f], toks...)
			fieldSet[f] = struct{}{}
		}
	}

	ix := &Index{post: map[string][]posting{}}
	for id := range byID {
		ix.ids = append(ix.ids, id)
	}
	sort.Strings(ix.ids)
	for f := range fieldSet {
		ix.fields = append(ix.fields, f)
	}
	sort.Strings(ix.fields)
	ix.lens = make([][]int32, len(ix.fields))
	for fi := range ix.fields {
		ix.lens[fi] = make([]int32, len(ix.ids))
	}

	for doc, id := range ix.ids {
		tfs := map[string][]int32{}
		for fi, f := range ix.fields {
			toks := byID[id][f]
			ix.lens[fi][doc] = int32(len(toks))
			for _, t := range toks {
				tf := tfs[t]
				if tf == nil {
					tf = make([]int32, len(ix.fields))
					tfs[t] = tf
				}
				tf[fi]++
			}
		}
		// ids are visited in ascending order, so every posting list stays sorted by doc.
		for t, tf := range tfs {
			ix.post[t] = append(ix.post[t], posting{doc: int32(doc), tf: tf})
		}
	}
	ix.averageLengths()
	return ix
}

func (ix *Index) averageLengths() {
	ix.avg = make([]float64, len(ix.fields))
	if len(ix.ids) == 0 {
		return
	}
	for fi, lens := range ix.lens {
		var total int64
		for _, n := range lens {
			total += int64(n)
		}
		ix.avg[fi] = float64(total) / float64(len(ix.ids))
	}
}

// Len reports how many documents the index holds.
func (ix *Index) Len() int { return len(ix.ids) }

// Score ranks every document holding at least one query term by BM25F (Robertson and
// Zaragoza 2009, section 3.6): each field's term frequency is length-normalized and
// weighted, the fields are summed, and only then is the sum saturated with K1, so a
// term repeated across fields saturates once per document. IDF is over the whole index.
// A repeated query term counts once. A field w names no weight for is not scored.
//
// Hits are sorted by Matched descending, then Score descending, then ID ascending, so a
// document holding every term outranks one holding fewer however often it repeats them.
func (ix *Index) Score(query []string, w BM25F) []Hit {
	weight := make([]float64, len(ix.fields))
	norm := make([]float64, len(ix.fields))
	for fi, f := range ix.fields {
		fw, ok := w.Fields[f]
		if !ok || ix.avg[fi] == 0 {
			continue
		}
		weight[fi] = fw.Weight
		norm[fi] = fw.B
	}

	n := float64(len(ix.ids))
	hits := map[int32]*Hit{}
	seen := map[string]bool{}
	for _, term := range query {
		if seen[term] {
			continue
		}
		seen[term] = true
		postings := ix.post[term]
		if len(postings) == 0 {
			continue
		}
		df := float64(len(postings))
		// The Lucene form of the Robertson-Sparck Jones IDF: the +1 keeps a term in more
		// than half the documents from scoring negative.
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		parts := make([]float64, len(ix.fields))
		for _, p := range postings {
			var tfw float64
			for fi, tf := range p.tf {
				parts[fi] = 0
				if tf == 0 || weight[fi] == 0 {
					continue
				}
				b := norm[fi]
				lengthNorm := (1 - b) + b*float64(ix.lens[fi][p.doc])/ix.avg[fi]
				parts[fi] = weight[fi] * float64(tf) / lengthNorm
				tfw += parts[fi]
			}
			if tfw == 0 {
				continue
			}
			score := idf * tfw / (w.K1 + tfw)
			h := hits[p.doc]
			if h == nil {
				h = &Hit{ID: ix.ids[p.doc]}
				hits[p.doc] = h
			}
			h.Score += score
			h.Matched++
			// The saturated score is shared out in proportion to each field's part of
			// the weighted frequency, so a hit's TermScores sum to its Score.
			for fi, part := range parts {
				if part > 0 {
					h.Terms = append(h.Terms, TermScore{Term: term, Field: ix.fields[fi], Score: score * part / tfw})
				}
			}
		}
	}

	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Matched != b.Matched {
			return a.Matched > b.Matched
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.ID < b.ID
	})
	return out
}

// WriteTo encodes the index in a versioned binary form ReadIndex reads back. The bytes
// depend only on the index's contents.
func (ix *Index) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: bufio.NewWriter(w)}
	cw.write(indexMagic[:])
	cw.uvarint(indexFormat)
	cw.uvarint(uint64(len(ix.ids)))
	for _, id := range ix.ids {
		cw.string(id)
	}
	cw.uvarint(uint64(len(ix.fields)))
	for fi, f := range ix.fields {
		cw.string(f)
		for _, n := range ix.lens[fi] {
			cw.uvarint(uint64(n))
		}
	}
	terms := make([]string, 0, len(ix.post))
	for t := range ix.post {
		terms = append(terms, t)
	}
	sort.Strings(terms)
	cw.uvarint(uint64(len(terms)))
	for _, t := range terms {
		cw.string(t)
		postings := ix.post[t]
		cw.uvarint(uint64(len(postings)))
		prev := int32(0)
		for _, p := range postings {
			cw.uvarint(uint64(p.doc - prev))
			prev = p.doc
			for _, tf := range p.tf {
				cw.uvarint(uint64(tf))
			}
		}
	}
	if cw.err == nil {
		cw.err = cw.w.Flush()
	}
	return cw.n, cw.err
}

type countWriter struct {
	w   *bufio.Writer
	n   int64
	err error
	buf [binary.MaxVarintLen64]byte
}

func (c *countWriter) write(p []byte) {
	if c.err != nil {
		return
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.err = err
}

func (c *countWriter) uvarint(v uint64) {
	c.write(c.buf[:binary.PutUvarint(c.buf[:], v)])
}

func (c *countWriter) string(s string) {
	c.uvarint(uint64(len(s)))
	c.write([]byte(s))
}

// maxEncodedString bounds a length prefix, so a corrupt one fails instead of
// allocating whatever it claims.
const maxEncodedString = 1 << 20

// ReadIndex decodes an index WriteTo wrote. Input written in another format version is
// an error naming both versions; on any error no index is returned.
func ReadIndex(r io.Reader) (*Index, error) {
	rd := &indexReader{r: bufio.NewReader(r)}
	var magic [4]byte
	if _, err := io.ReadFull(rd.r, magic[:]); err != nil {
		return nil, fmt.Errorf("textrank: reading index header: %w", err)
	}
	if magic != indexMagic {
		return nil, fmt.Errorf("textrank: not a textrank index (header %q)", magic[:])
	}
	if v := rd.uvarint(); rd.err == nil && v != indexFormat {
		return nil, fmt.Errorf("textrank: index format version %d, this build reads version %d; rebuild the index", v, indexFormat)
	}

	ix := &Index{post: map[string][]posting{}}
	ndocs := rd.count()
	for i := 0; i < ndocs && rd.err == nil; i++ {
		ix.ids = append(ix.ids, rd.string())
	}
	nfields := rd.count()
	for i := 0; i < nfields && rd.err == nil; i++ {
		ix.fields = append(ix.fields, rd.string())
		lens := make([]int32, 0, min(ndocs, 1<<16))
		for d := 0; d < ndocs && rd.err == nil; d++ {
			lens = append(lens, rd.int32())
		}
		ix.lens = append(ix.lens, lens)
	}
	nterms := rd.count()
	for i := 0; i < nterms && rd.err == nil; i++ {
		term := rd.string()
		npost := rd.count()
		postings := make([]posting, 0, min(npost, 1<<16))
		doc := int64(0)
		for j := 0; j < npost && rd.err == nil; j++ {
			doc += int64(rd.uvarint())
			if doc >= int64(ndocs) {
				rd.fail(fmt.Errorf("term %q names document %d of %d", term, doc, ndocs))
				break
			}
			p := posting{doc: int32(doc), tf: make([]int32, nfields)}
			for fi := range p.tf {
				p.tf[fi] = rd.int32()
			}
			postings = append(postings, p)
		}
		ix.post[term] = postings
	}
	if rd.err != nil {
		return nil, fmt.Errorf("textrank: reading index: %w", rd.err)
	}
	ix.averageLengths()
	return ix, nil
}

type indexReader struct {
	r   *bufio.Reader
	err error
}

func (r *indexReader) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *indexReader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}
	v, err := binary.ReadUvarint(r.r)
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	r.fail(err)
	return v
}

func (r *indexReader) count() int {
	v := r.uvarint()
	if v > math.MaxInt32 {
		r.fail(fmt.Errorf("count %d out of range", v))
		return 0
	}
	return int(v)
}

func (r *indexReader) int32() int32 {
	v := r.uvarint()
	if v > math.MaxInt32 {
		r.fail(fmt.Errorf("value %d out of range", v))
		return 0
	}
	return int32(v)
}

func (r *indexReader) string() string {
	n := r.uvarint()
	if r.err != nil {
		return ""
	}
	if n > maxEncodedString {
		r.fail(fmt.Errorf("string length %d over the %d limit", n, maxEncodedString))
		return ""
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r.r, b); err != nil {
		r.fail(io.ErrUnexpectedEOF)
		return ""
	}
	return string(b)
}
