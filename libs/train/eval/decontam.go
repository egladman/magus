package eval

import (
	"cmp"
	"hash/fnv"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/train/completion/fim"
)

// This is the 13-gram check of cmd/embed texts (libs/train/embed/cmd/embed/decontam.go),
// copied because that package is main. The constants and the hashes are the same, so a
// text one admits the other admits. Brown et al. 2020 (GPT-3, appendix C) set the window.
const (
	GramWords    = 13
	MinGramWords = 8
	// Common is cmd/embed texts' default: a gram more than this many eval texts hold is
	// boilerplate, such as an error-return idiom, and never drops a text.
	Common = 10
)

// Decontaminator indexes eval texts by whole-text hash and by 13-word grams.
type Decontaminator struct {
	exact   map[uint64]bool
	grams   []uint64
	texts   []int32
	lengths []int
}

// NewDecontaminator indexes every eval text.
func NewDecontaminator(evalTexts []string) *Decontaminator {
	d := &Decontaminator{exact: map[uint64]bool{}}
	type ref struct {
		hash uint64
		text int32
	}
	var refs []ref
	lengths := map[int]bool{}
	var n int32
	for _, text := range evalTexts {
		words := strings.Fields(text)
		if len(words) == 0 {
			continue
		}
		d.exact[wholeHash(words)] = true
		size := min(len(words), GramWords)
		if size < MinGramWords {
			continue
		}
		lengths[size] = true
		for _, h := range windowHashes(words, size) {
			refs = append(refs, ref{h, n})
		}
		n++
	}
	slices.SortFunc(refs, func(a, b ref) int {
		return cmp.Or(cmp.Compare(a.hash, b.hash), cmp.Compare(a.text, b.text))
	})
	for i, r := range refs {
		last := len(d.grams) - 1
		if last >= 0 && d.grams[last] == r.hash {
			if refs[i-1].text != r.text {
				d.texts[last]++
			}
			continue
		}
		d.grams = append(d.grams, r.hash)
		d.texts = append(d.texts, 1)
	}
	d.lengths = slices.Sorted(maps.Keys(lengths))
	return d
}

// Match is "exact" or "ngram" when text repeats an eval text, and "" when it repeats none.
func (d *Decontaminator) Match(text string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	if d.exact[wholeHash(words)] {
		return "exact"
	}
	for _, size := range d.lengths {
		for _, h := range windowHashes(words, size) {
			if i, ok := slices.BinarySearch(d.grams, h); ok && d.texts[i] <= Common {
				return "ngram"
			}
		}
	}
	return ""
}

const gramBase = 0x9e3779b97f4a7c15

func wordHash(w string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(w))
	return h.Sum64()
}

func wholeHash(words []string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(words, " ")))
	return h.Sum64()
}

func windowHashes(words []string, size int) []uint64 {
	if len(words) < size {
		return nil
	}
	var top uint64 = 1
	for range size - 1 {
		top *= gramBase
	}
	out := make([]uint64, 0, len(words)-size+1)
	var h uint64
	for i, w := range words {
		if i >= size {
			h -= wordHash(words[i-size]) * top
		}
		h = h*gramBase + wordHash(w)
		if i >= size-1 {
			out = append(out, h)
		}
	}
	return out
}

// Hits returns the byte ranges of text that repeat an eval text: each matching gram
// window, widened to the words it spans, or the whole text when it equals an eval text.
// The ranges are sorted and may overlap.
func (d *Decontaminator) Hits(text string) []fim.Range {
	type word struct{ start, end int }
	var spans []word
	start := -1
	for i, r := range text {
		blank := unicode.IsSpace(r)
		if blank && start >= 0 {
			spans = append(spans, word{start, i})
			start = -1
		} else if !blank && start < 0 {
			start = i
		}
	}
	if start >= 0 {
		spans = append(spans, word{start, len(text)})
	}
	if len(spans) == 0 {
		return nil
	}
	words := make([]string, len(spans))
	for i, w := range spans {
		words[i] = text[w.start:w.end]
	}
	if d.exact[wholeHash(words)] {
		return []fim.Range{{Start: 0, End: len(text)}}
	}
	var out []fim.Range
	for _, size := range d.lengths {
		for i, h := range windowHashes(words, size) {
			if j, ok := slices.BinarySearch(d.grams, h); ok && d.texts[j] <= Common {
				out = append(out, fim.Range{Start: spans[i].start, End: spans[i+size-1].end})
			}
		}
	}
	slices.SortFunc(out, func(a, b fim.Range) int { return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.End, b.End)) })
	return out
}
