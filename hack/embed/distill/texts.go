package distill

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"iter"
	"math"
)

// Text is one training text.
type Text struct {
	ID, Body string
}

// TextPair is one mined pair: a query and the document it should retrieve.
type TextPair struct {
	Source, Query, Doc string
}

// TextSource yields the training texts in a fixed order. A non-nil error ends the
// sequence and fails whatever is reading it.
type TextSource interface {
	Texts() iter.Seq2[Text, error]
}

// Pairs yields the training pairs in a fixed order, with TextSource's error contract.
type Pairs interface {
	Pairs() iter.Seq2[TextPair, error]
}

// Bag is a text as distinct vocabulary rows and their counts, rows ascending. Tokens
// outside the vocabulary are dropped.
type Bag struct {
	Rows   []int32
	Counts []float64
}

func (b Bag) total() float64 {
	var n float64
	for _, c := range b.Counts {
		n += c
	}
	return n
}

// key identifies a bag's contents, so two texts that embed identically count as one
// in a contrastive batch.
func (b Bag) key() uint64 {
	h := fnv.New64a()
	var buf [12]byte
	for i, r := range b.Rows {
		binary.LittleEndian.PutUint32(buf[:4], uint32(r))
		binary.LittleEndian.PutUint64(buf[4:], math.Float64bits(b.Counts[i]))
		h.Write(buf[:])
	}
	return h.Sum64()
}

// Example is one MSE example: a text's bag and its teacher target in the PCA basis.
type Example struct {
	ID     string
	Bag    Bag
	Target []float64
}

// Pair is one contrastive example.
type Pair struct {
	Query, Doc Bag
}

// Examples bags every text of c and pairs it with target(id), which must have the
// model's dims. A text with no vocabulary token cannot be embedded and is skipped; the
// count of those is returned. A text whose target is missing is an error.
func Examples(v *Vocab, c TextSource, target func(id string) ([]float64, bool)) ([]Example, int, error) {
	var out []Example
	var empty int
	for t, err := range c.Texts() {
		if err != nil {
			return nil, 0, err
		}
		y, ok := target(t.ID)
		if !ok {
			return nil, 0, fmt.Errorf("text %q has no teacher target", t.ID)
		}
		b := v.Bag(t.Body)
		if len(b.Rows) == 0 {
			empty++
			continue
		}
		out = append(out, Example{ID: t.ID, Bag: b, Target: y})
	}
	if len(out) == 0 {
		return nil, empty, errors.New("no text holds a vocabulary token")
	}
	return out, empty, nil
}

// BagPairs bags every pair. A pair whose query or doc has no vocabulary token, or
// whose query bags the same as its doc, teaches nothing and is skipped; the count of
// those is returned.
func BagPairs(v *Vocab, p Pairs) ([]Pair, int, error) {
	var out []Pair
	var empty int
	for tp, err := range p.Pairs() {
		if err != nil {
			return nil, 0, err
		}
		q, d := v.Bag(tp.Query), v.Bag(tp.Doc)
		if len(q.Rows) == 0 || len(d.Rows) == 0 || q.key() == d.key() {
			empty++
			continue
		}
		out = append(out, Pair{Query: q, Doc: d})
	}
	return out, empty, nil
}

// IDHash is the uint64 behind every by-hash choice in the pipeline: the first eight
// bytes of sha256(salt || id), big-endian. It does not depend on input order.
func IDHash(salt, id string) uint64 {
	sum := sha256.Sum256([]byte(salt + id))
	return binary.BigEndian.Uint64(sum[:8])
}
