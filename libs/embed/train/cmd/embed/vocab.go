package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/embed/train/distill"
)

// vocabEntry is one line of gen/vocab.jsonl.
type vocabEntry struct {
	Token string `json:"token"`
	Count int64  `json:"count"`
}

// vocabJSONL is v as JSON lines sorted by token, so the committed file diffs by token
// and does not depend on rank ties.
func vocabJSONL(v *distill.Vocab) ([]byte, error) {
	entries := make([]vocabEntry, v.Len())
	for i, tok := range v.Tokens {
		entries[i] = vocabEntry{Token: tok, Count: v.Counts[i]}
	}
	slices.SortFunc(entries, func(a, b vocabEntry) int { return strings.Compare(a.Token, b.Token) })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// readVocab reads a vocabulary vocabJSONL wrote and ranks it for distill.NewVocab. The
// file keeps no total, so Total is the sum of the counts.
func readVocab(path string) (*distill.Vocab, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []vocabEntry
	dec := json.NewDecoder(f)
	for {
		var e vocabEntry
		if err := dec.Decode(&e); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%s: entry %d: %w", path, len(entries)+1, err)
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b vocabEntry) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Token, b.Token)
	})
	tokens := make([]string, len(entries))
	counts := make([]int64, len(entries))
	var total int64
	for i, e := range entries {
		tokens[i], counts[i] = e.Token, e.Count
		total += e.Count
	}
	v, err := distill.NewVocab(tokens, counts, total)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
