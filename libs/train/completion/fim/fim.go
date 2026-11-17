// Package fim builds fill-in-the-middle training and evaluation samples from source
// files, deterministically. A file's randomness is a PCG seeded from the sha256 of its
// path, its blob sha and the version string, so the same file at the same blob always
// yields the same samples and a rerun on another machine changes nothing.
//
// The span mix, the order mix and the windows are stage S1's (plan 2.4). The context
// provider's fields stay empty here; stage S2 builds the provider.
package fim

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand/v2"
	"strings"
	"unicode/utf8"
)

// Version is hashed into every seed. Changing a mix below means a new version.
const Version = "fim/v1"

const (
	// PrefixWindow and SuffixWindow match what the editor sends: 6,000 B before the
	// cursor and 2,000 B after it.
	PrefixWindow = 6000
	SuffixWindow = 2000
	// AnchorAlign is the alignment of the prefix window's start, so typing that only
	// extends the prefix keeps the cache (plan 5.3).
	AnchorAlign = 1024

	// Rate is the share of samples that are fill-in-the-middle; the rest are plain
	// left-to-right text.
	Rate = 0.9

	// The span mix of a FIM sample, cumulative: AST, then single-line, then random
	// characters, then an empty middle.
	astShare    = 0.40
	lineShare   = 0.30
	randomShare = 0.20

	// maxRandom is the longest random-character middle in bytes.
	maxRandom = 256
	// lmWindow is the text a plain left-to-right sample carries.
	lmWindow = PrefixWindow + SuffixWindow
)

// Kind is how a sample's middle was chosen.
type Kind string

const (
	KindAST    Kind = "ast"
	KindLine   Kind = "line"
	KindRandom Kind = "random"
	KindEmpty  Kind = "empty"
	// KindLM is a plain left-to-right sample: no middle.
	KindLM Kind = "lm"
)

// Order is how a FIM sample is laid out for the model: prefix-suffix-middle or
// suffix-prefix-middle.
type Order string

const (
	PSM Order = "psm"
	SPM Order = "spm"
)

// File is one source file at one blob.
type File struct {
	Path     string
	Blob     string
	Language string
	Text     string
}

// Chunk is one piece of retrieved context. Stage S1 never fills it.
type Chunk struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// Sample is one training or evaluation example. Start and End are the middle's byte
// range in the file. Fallback names the share a sample was drawn from when its own had
// no candidate, so a file without an AST span says so instead of drawing silently from
// another share.
type Sample struct {
	Path     string  `json:"path"`
	Blob     string  `json:"blob"`
	Language string  `json:"language"`
	Kind     Kind    `json:"kind"`
	Fallback Kind    `json:"fallback,omitempty"`
	Order    Order   `json:"order,omitempty"`
	Start    int     `json:"start"`
	End      int     `json:"end"`
	Prefix   string  `json:"prefix"`
	Middle   string  `json:"middle"`
	Suffix   string  `json:"suffix"`
	Context  []Chunk `json:"context"`
}

// Seed is the PCG state a file's samples draw from.
func Seed(path, blob string) (uint64, uint64) {
	sum := sha256.Sum256([]byte(path + "\x00" + blob + "\x00" + Version))
	return binary.LittleEndian.Uint64(sum[0:8]), binary.LittleEndian.Uint64(sum[8:16])
}

// Limit bounds where a file's samples may put their middle: [From, To) in bytes. The
// zero value means the whole file. A file whose tail is reserved for a dev set trains
// on a Limit that ends before it.
type Limit struct{ From, To int }

// Build returns n samples of f, deterministic in (f.Path, f.Blob, n, lim). Prefix and
// suffix still read the whole file, so a limit reserves middles, not context.
func Build(f File, n int, lim Limit) []Sample {
	if lim.To == 0 {
		lim.To = len(f.Text)
	}
	lim.To = min(lim.To, len(f.Text))
	a, b := Seed(f.Path, f.Blob)
	rng := rand.New(rand.NewPCG(a, b))
	var cands []Range
	for _, c := range Constructs(f.Language, f.Text) {
		if c.Start >= lim.From && c.End <= lim.To {
			cands = append(cands, c)
		}
	}
	var out []Sample
	for range n {
		out = append(out, one(f, rng, cands, lim))
	}
	return out
}

func one(f File, rng *rand.Rand, cands []Range, lim Limit) Sample {
	s := Sample{Path: f.Path, Blob: f.Blob, Language: f.Language, Context: []Chunk{}}
	if rng.Float64() >= Rate {
		s.Kind = KindLM
		s.Start, s.End = lim.From, lim.From
		s.Prefix = clip(f.Text, lineStartAtOrAfter(f.Text, lim.From), min(lim.To, lineStartAtOrAfter(f.Text, lim.From)+lmWindow))
		return s
	}
	s.Order = PSM
	if rng.IntN(2) == 1 {
		s.Order = SPM
	}
	share := rng.Float64()
	var kind Kind
	switch {
	case share < astShare:
		kind = KindAST
	case share < astShare+lineShare:
		kind = KindLine
	case share < astShare+lineShare+randomShare:
		kind = KindRandom
	default:
		kind = KindEmpty
	}
	s.Kind = kind
	var r Range
	ok := true
	switch kind {
	case KindAST:
		if len(cands) == 0 {
			ok = false
		} else {
			r = cands[rng.IntN(len(cands))]
		}
	case KindLine:
		r, ok = lineSpan(f.Text, rng, lim)
	case KindRandom:
		r, ok = randomSpan(f.Text, rng, lim)
	case KindEmpty:
		p, found := cursor(f.Text, rng, lim)
		r, ok = Range{p, p}, found
	}
	if !ok {
		// The file has no candidate of this share: say so and take an empty middle at the
		// limit's start, the one span every non-empty range has.
		s.Fallback, s.Kind = kind, KindEmpty
		p := min(max(lim.From, 0), len(f.Text))
		r = Range{p, p}
	}
	s.Start, s.End = r.Start, r.End
	s.Prefix, s.Middle, s.Suffix = Windows(f.Text, r)
	return s
}

// Windows cuts the prefix and suffix windows around the middle r. The prefix starts at
// the first line start at or after the AnchorAlign-aligned offset that keeps it within
// PrefixWindow bytes; the suffix is the SuffixWindow bytes after the middle, cut at a
// rune boundary.
func Windows(text string, r Range) (prefix, middle, suffix string) {
	start := 0
	if r.Start > PrefixWindow {
		aligned := ((r.Start-PrefixWindow)/AnchorAlign + 1) * AnchorAlign
		start = lineStartAtOrAfter(text, aligned)
		start = min(start, r.Start)
	}
	endCut := min(len(text), r.End+SuffixWindow)
	for endCut > r.End && endCut < len(text) && !utf8.RuneStart(text[endCut]) {
		endCut--
	}
	return text[start:r.Start], text[r.Start:r.End], text[r.End:endCut]
}

// lineStartAtOrAfter is the smallest line start at or after off: off itself when it
// begins a line.
func lineStartAtOrAfter(text string, off int) int {
	if off <= 0 {
		return 0
	}
	if off >= len(text) {
		return len(text)
	}
	if text[off-1] == '\n' {
		return off
	}
	i := strings.IndexByte(text[off:], '\n')
	if i < 0 {
		return len(text)
	}
	return off + i + 1
}

func clip(text string, from, to int) string {
	from, to = min(from, len(text)), min(to, len(text))
	for to > from && to < len(text) && !utf8.RuneStart(text[to]) {
		to--
	}
	return text[from:to]
}

// cursor draws a rune boundary inside the limit.
func cursor(text string, rng *rand.Rand, lim Limit) (int, bool) {
	if lim.To <= lim.From {
		return 0, false
	}
	p := lim.From + rng.IntN(lim.To-lim.From+1)
	for p < lim.To && p < len(text) && !utf8.RuneStart(text[p]) {
		p++
	}
	return min(p, lim.To), true
}

// lineSpan is a cursor to the end of its line, where the cursor falls on a line with
// text after it.
func lineSpan(text string, rng *rand.Rand, lim Limit) (Range, bool) {
	for range 16 {
		p, ok := cursor(text, rng, lim)
		if !ok {
			return Range{}, false
		}
		end := strings.IndexByte(text[p:], '\n')
		if end < 0 {
			end = len(text) - p
		}
		end += p
		if end > lim.To {
			end = lim.To
		}
		if strings.TrimSpace(text[p:end]) != "" {
			return Range{p, end}, true
		}
	}
	return Range{}, false
}

// randomSpan is a span of up to maxRandom bytes from a random cursor, both ends on rune
// boundaries, inside the limit.
func randomSpan(text string, rng *rand.Rand, lim Limit) (Range, bool) {
	p, ok := cursor(text, rng, lim)
	if !ok || p >= lim.To {
		return Range{}, false
	}
	end := min(lim.To, p+1+rng.IntN(maxRandom))
	for end < lim.To && end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	return Range{p, end}, strings.TrimSpace(text[p:end]) != ""
}
