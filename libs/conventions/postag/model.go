package postag

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/egladman/magus/libs/conventions/postag/internal/feature"
	"io"
	"sync"
)

// The format is internal/train's encodeModel: a magic line, the classes, the
// tag dictionary, the weights sorted by feature hash, and the test counts the
// trainer measured. Every integer is a varint.
//
//go:embed gen/model.gz
var modelGz []byte

const modelMagic = "postag1\n"

type class struct {
	upos UPOS
	xpos string
}

type classWeight struct {
	class  uint16
	weight int32
}

type span struct{ start, end uint32 }

type model struct {
	classes []class
	dict    map[string]uint16
	feats   map[feature.Hash]span
	weights []classWeight
	// The trainer's own test-split counts; TestTagAccuracy demands the same.
	testTokens, testUPOS, testXPOS int
}

var loadModel = sync.OnceValue(func() *model {
	m, err := decodeModel(modelGz)
	if err != nil {
		panic(fmt.Sprintf("postag: embedded model: %v", err))
	}
	return m
})

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

func decodeModel(gz []byte) (*model, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(raw, []byte(modelMagic)) {
		return nil, errors.New("not a postag model")
	}
	r := &modelReader{buf: raw[len(modelMagic):]}
	m := &model{}

	m.classes = make([]class, r.uvarint())
	for i := range m.classes {
		xpos, name := r.str(), r.str()
		u, ok := parseUPOS(name)
		if r.err == nil && !ok {
			return nil, fmt.Errorf("class %d: unknown UPOS %q", i, name)
		}
		m.classes[i] = class{upos: u, xpos: xpos}
	}

	n := r.uvarint()
	m.dict = make(map[string]uint16, n)
	for range n {
		word := r.str()
		m.dict[word] = r.class(len(m.classes))
	}

	n = r.uvarint()
	m.feats = make(map[feature.Hash]span, n)
	var h uint64
	for range n {
		h += r.uvarint()
		k := r.uvarint()
		s := span{start: uint32(len(m.weights))}
		for range k {
			c := r.class(len(m.classes))
			m.weights = append(m.weights, classWeight{class: c, weight: int32(r.varint())})
		}
		s.end = uint32(len(m.weights))
		m.feats[feature.Hash(h)] = s
		if r.err != nil {
			break
		}
	}

	m.testTokens, m.testUPOS, m.testXPOS = int(r.uvarint()), int(r.uvarint()), int(r.uvarint())
	if r.err != nil {
		return nil, r.err
	}
	if len(r.buf) != 0 {
		return nil, fmt.Errorf("%d trailing bytes", len(r.buf))
	}
	return m, nil
}

func (r *modelReader) class(n int) uint16 {
	c := r.uvarint()
	if r.err == nil && c >= uint64(n) {
		r.err = fmt.Errorf("class %d out of range", c)
	}
	return uint16(c)
}

// predict returns the highest-scoring class, the lowest index on a tie.
func (m *model) predict(feats []feature.Hash, scores []int32) uint16 {
	clear(scores)
	for _, f := range feats {
		s, ok := m.feats[f]
		if !ok {
			continue
		}
		for _, cw := range m.weights[s.start:s.end] {
			scores[cw.class] += cw.weight
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
