package wordclass

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"
)

//go:embed gen/model.gz
var modelGz []byte

// magic opens the format. Changing it changes model.gz, so it waits for the
// next retrain.
const magic = "postag1\n"

// Label is one class a model predicts, a UPOS and XPOS pair as EWT spells
// them. A pair rather than an XPOS mapped to a UPOS afterwards: EWT splits IN
// into ADP and SCONJ and RB into ADV and PART by context, which a mapping
// from XPOS and word cannot recover.
type Label struct{ UPOS, XPOS string }

// Weight is one class's weight for one feature.
type Weight struct {
	Class uint16
	Value int32
}

// Accuracy is what the trainer measured over EWT's test split: the words, and
// how many got the right UPOS and the right XPOS. It is part of the format, so
// TestTagAccuracy can demand the same counts from the embedded model.
type Accuracy struct{ Words, UPOS, XPOS int }

type class struct {
	upos UPOS
	xpos XPOS
}

type span struct{ start, end uint32 }

// Model is a trained tagger: its classes, the words it tags by dictionary, and
// the weights of the features it scores every other word by. A Model is safe
// for concurrent use once built.
type Model struct {
	Accuracy Accuracy

	classes []class
	dict    map[string]uint16
	feats   map[Hash]span
	// weights holds every feature's weights back to back in hash order; a span
	// indexes one feature's run.
	weights []Weight
}

var embedded = sync.OnceValue(func() *Model {
	m, err := unzip(modelGz)
	if err != nil {
		panic(fmt.Sprintf("wordclass: embedded model: %v", err))
	}

	return m
})

func unzip(gz []byte) (*Model, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}

	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}

	m := &Model{}

	return m, m.UnmarshalBinary(raw)
}

// NewModel builds a Model from a trainer's output. dict maps a word to the
// index in labels it is always tagged with; weights holds each feature's
// weights in ascending class order. It fails on a label whose UPOS is not
// one of the constants and on a class index past labels.
func NewModel(labels []Label, dict map[string]uint16, weights map[Hash][]Weight) (*Model, error) {
	m := &Model{
		classes: make([]class, len(labels)),
		dict:    make(map[string]uint16, len(dict)),
		feats:   make(map[Hash]span, len(weights)),
	}

	for i, l := range labels {
		u, ok := parseUPOS(l.UPOS)
		if !ok {
			return nil, fmt.Errorf("class %d: unknown UPOS %q", i, l.UPOS)
		}

		m.classes[i] = class{upos: u, xpos: XPOS(l.XPOS)}
	}

	for w, c := range dict {
		if int(c) >= len(labels) {
			return nil, fmt.Errorf("word %q: class %d out of range", w, c)
		}

		m.dict[w] = c
	}

	for _, h := range slices.Sorted(maps.Keys(weights)) {
		s := span{start: uint32(len(m.weights))}

		for _, w := range weights[h] {
			if int(w.Class) >= len(labels) {
				return nil, fmt.Errorf("class %d out of range", w.Class)
			}

			m.weights = append(m.weights, w)
		}

		s.end = uint32(len(m.weights))
		m.feats[h] = s
	}

	return m, nil
}

// MarshalBinary writes the format: the magic line, the classes, the
// dictionary, the weights in ascending feature hash order, and the Accuracy.
// Every integer is a varint and every collection is sorted, so the bytes
// depend only on the model.
func (m *Model) MarshalBinary() ([]byte, error) {
	b := []byte(magic)
	str := func(s string) {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}

	b = binary.AppendUvarint(b, uint64(len(m.classes)))

	for _, c := range m.classes {
		str(string(c.xpos))
		str(c.upos.String())
	}

	words := slices.Sorted(maps.Keys(m.dict))
	b = binary.AppendUvarint(b, uint64(len(words)))

	for _, w := range words {
		str(w)
		b = binary.AppendUvarint(b, uint64(m.dict[w]))
	}

	hashes := slices.Sorted(maps.Keys(m.feats))
	b = binary.AppendUvarint(b, uint64(len(hashes)))

	var last Hash

	for _, h := range hashes {
		b = binary.AppendUvarint(b, uint64(h-last))
		last = h
		s := m.feats[h]
		b = binary.AppendUvarint(b, uint64(s.end-s.start))

		for _, w := range m.weights[s.start:s.end] {
			b = binary.AppendUvarint(b, uint64(w.Class))
			b = binary.AppendVarint(b, int64(w.Value))
		}
	}

	for _, n := range []int{m.Accuracy.Words, m.Accuracy.UPOS, m.Accuracy.XPOS} {
		b = binary.AppendUvarint(b, uint64(n))
	}

	return b, nil
}

// UnmarshalBinary reads what MarshalBinary writes, replacing m. It fails on
// any byte out of place: a wrong magic, a truncated or out-of-range value, or
// bytes left over.
func (m *Model) UnmarshalBinary(data []byte) error {
	raw, ok := bytes.CutPrefix(data, []byte(magic))
	if !ok {
		return errors.New("not a wordclass model")
	}

	r := &modelReader{buf: raw}
	out := Model{}

	nClasses := r.count()
	if r.err != nil {
		return r.err
	}

	out.classes = make([]class, nClasses)
	for i := range out.classes {
		xpos, name := r.str(), r.str()

		u, ok := parseUPOS(name)
		if r.err == nil && !ok {
			return fmt.Errorf("class %d: unknown UPOS %q", i, name)
		}

		out.classes[i] = class{upos: u, xpos: XPOS(xpos)}
	}

	nWords := r.count()
	if r.err != nil {
		return r.err
	}

	out.dict = make(map[string]uint16, nWords)
	for range nWords {
		word := r.str()
		out.dict[word] = r.class(len(out.classes))
	}

	nFeats := r.count()
	if r.err != nil {
		return r.err
	}

	out.feats = make(map[Hash]span, nFeats)

	var h uint64
	for range nFeats {
		h += r.uvarint()
		k := r.count()
		s := span{start: uint32(len(out.weights))}

		for range k {
			c := r.class(len(out.classes))
			out.weights = append(out.weights, Weight{Class: c, Value: int32(r.varint())})
		}

		s.end = uint32(len(out.weights))
		out.feats[Hash(h)] = s

		if r.err != nil {
			break
		}
	}

	out.Accuracy = Accuracy{Words: int(r.uvarint()), UPOS: int(r.uvarint()), XPOS: int(r.uvarint())}
	if r.err != nil {
		return r.err
	}

	if len(r.buf) != 0 {
		return fmt.Errorf("%d trailing bytes", len(r.buf))
	}

	*m = out

	return nil
}

type modelReader struct {
	buf []byte
	err error
}

func (r *modelReader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}

	v, n := binary.Uvarint(r.buf)
	if n <= 0 {
		r.err = errors.New("truncated varint")
		return 0
	}

	r.buf = r.buf[n:]

	return v
}

func (r *modelReader) varint() int64 {
	if r.err != nil {
		return 0
	}

	v, n := binary.Varint(r.buf)
	if n <= 0 {
		r.err = errors.New("truncated varint")
		return 0
	}

	r.buf = r.buf[n:]

	return v
}

// count reads the length of a collection whose entries each take at least one
// byte, so a count past the bytes left is corrupt and must not size an
// allocation.
func (r *modelReader) count() int {
	n := r.uvarint()
	if r.err != nil {
		return 0
	}

	if n > uint64(len(r.buf)) {
		r.err = fmt.Errorf("count %d past the %d bytes left", n, len(r.buf))
		return 0
	}

	return int(n)
}

func (r *modelReader) str() string {
	n := r.uvarint()
	if r.err != nil {
		return ""
	}

	if n > uint64(len(r.buf)) {
		r.err = errors.New("truncated string")
		return ""
	}

	s := string(r.buf[:n])
	r.buf = r.buf[n:]

	return s
}

func (r *modelReader) class(n int) uint16 {
	c := r.uvarint()
	if r.err == nil && c >= uint64(n) {
		r.err = fmt.Errorf("class %d out of range", c)
	}

	return uint16(c)
}

// predict returns the highest-scoring class, the lowest index on a tie.
func (m *Model) predict(feats []Hash, scores []int32) uint16 {
	clear(scores)

	for _, f := range feats {
		s, ok := m.feats[f]
		if !ok {
			continue
		}

		for _, w := range m.weights[s.start:s.end] {
			scores[w.Class] += w.Value
		}
	}

	best := 0

	for c, s := range scores {
		if s > scores[best] {
			best = c
		}
	}

	return uint16(best)
}
