package main

import (
	"encoding/binary"
	"github.com/egladman/magus/libs/proofread/postag/internal/feature"
	"maps"
	"slices"
)

// encode writes the format postag's decodeModel reads. Every collection is
// sorted, so the bytes depend only on the weights.
func (t *trainer) encode(q *quantized, testCounts [3]int) []byte {
	b := []byte("postag1\n")
	str := func(s string) {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}

	b = binary.AppendUvarint(b, uint64(len(t.classes)))
	for _, c := range t.classes {
		str(c.xpos)
		str(c.upos)
	}

	words := slices.Sorted(maps.Keys(t.dict))
	b = binary.AppendUvarint(b, uint64(len(words)))
	for _, w := range words {
		str(w)
		b = binary.AppendUvarint(b, uint64(t.dict[w]))
	}

	hashes := slices.Sorted(maps.Keys(q.weights))
	b = binary.AppendUvarint(b, uint64(len(hashes)))
	var last feature.Hash
	for _, h := range hashes {
		b = binary.AppendUvarint(b, uint64(h-last))
		last = h
		cws := q.weights[h]
		b = binary.AppendUvarint(b, uint64(len(cws)))
		for _, cw := range cws {
			b = binary.AppendUvarint(b, uint64(cw.class))
			b = binary.AppendVarint(b, int64(cw.weight))
		}
	}

	for _, n := range testCounts {
		b = binary.AppendUvarint(b, uint64(n))
	}
	return b
}
