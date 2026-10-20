package distill

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/embed"
)

// Vocab is the token set a table is trained over, in rank order: count descending,
// then token ascending. Every token is a fixed point of embed.Tokens, so the runtime
// tokenizer reaches it.
type Vocab struct {
	Tokens []string
	Counts []int64 // occurrences of each token in the training texts
	Total  int64   // every token occurrence in the training texts, in the vocabulary or not
	index  map[string]int32
}

// VocabOptions bounds a vocabulary. Every field is required; there are no defaults.
type VocabOptions struct {
	MinCount int      // a counted token below this is left out
	MaxBytes int      // a counted token longer than this is left out
	Size     int      // at most this many tokens, forced ones included
	Forced   []string // run through embed.Tokens; every token they yield is kept
}

func (o VocabOptions) validate() error {
	if o.MinCount < 1 || o.MaxBytes < 1 || o.Size < 1 {
		return fmt.Errorf("min count %d, max bytes %d and size %d must all be at least 1", o.MinCount, o.MaxBytes, o.Size)
	}
	return nil
}

// BuildVocab counts embed.Tokens over c and keeps the tokens counted at least
// MinCount times that are at most MaxBytes long and not all digits, plus every forced
// token whatever its count. When more qualify than Size allows, the forced tokens stay
// and the rest are cut by rank. Forced tokens that alone exceed Size are an error.
func BuildVocab(c TextSource, opts VocabOptions) (*Vocab, error) {
	if err := opts.validate(); err != nil {
		return nil, fmt.Errorf("vocab: %w", err)
	}
	counts := map[string]int64{}
	var total int64
	for t, err := range c.Texts() {
		if err != nil {
			return nil, fmt.Errorf("vocab: %w", err)
		}
		for _, tok := range embed.Tokens(t.Body) {
			counts[tok]++
			total++
		}
	}

	forced, err := ForcedTokens(opts.Forced)
	if err != nil {
		return nil, err
	}
	if len(forced) > opts.Size {
		return nil, fmt.Errorf("vocab: %d forced tokens exceed the size %d", len(forced), opts.Size)
	}

	var kept []string
	for tok, n := range counts {
		if !forced[tok] && n >= int64(opts.MinCount) && len(tok) <= opts.MaxBytes && !allDigits(tok) && fixedPoint(tok) {
			kept = append(kept, tok)
		}
	}
	byRank := func(a, b string) int {
		if c := cmp.Compare(counts[b], counts[a]); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	}
	slices.SortFunc(kept, byRank)
	kept = kept[:min(len(kept), opts.Size-len(forced))]
	for tok := range forced {
		kept = append(kept, tok)
	}
	slices.SortFunc(kept, byRank)

	n := make([]int64, len(kept))
	for i, tok := range kept {
		n[i] = counts[tok]
	}
	return NewVocab(kept, n, total)
}

// ForcedTokens is the set of tokens embed.Tokens makes of forced, each of which it
// must keep whole. A forced entry that yields no token, or a token embed.Tokens would
// not keep whole, is an error.
func ForcedTokens(forced []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, f := range forced {
		toks := embed.Tokens(f)
		if len(toks) == 0 {
			return nil, fmt.Errorf("vocab: forced token %q yields no token", f)
		}
		for _, tok := range toks {
			if !fixedPoint(tok) {
				return nil, fmt.Errorf("vocab: forced token %q yields %q, which embed.Tokens does not keep whole", f, tok)
			}
			out[tok] = true
		}
	}
	return out, nil
}

// NewVocab checks a vocabulary read back from disk: tokens in rank order, unique, each
// a fixed point of embed.Tokens, with counts that sum to at most total.
func NewVocab(tokens []string, counts []int64, total int64) (*Vocab, error) {
	if len(tokens) == 0 {
		return nil, errors.New("vocab: no tokens")
	}
	if len(counts) != len(tokens) {
		return nil, fmt.Errorf("vocab: %d tokens but %d counts", len(tokens), len(counts))
	}
	if len(tokens) > math.MaxInt32 {
		return nil, fmt.Errorf("vocab: %d tokens overflow an int32 row", len(tokens))
	}
	v := &Vocab{Tokens: tokens, Counts: counts, Total: total, index: make(map[string]int32, len(tokens))}
	var sum int64
	for i, tok := range tokens {
		if !fixedPoint(tok) {
			return nil, fmt.Errorf("vocab: token %q is not what embed.Tokens makes of it", tok)
		}
		if _, dup := v.index[tok]; dup {
			return nil, fmt.Errorf("vocab: token %q repeats", tok)
		}
		if counts[i] < 0 {
			return nil, fmt.Errorf("vocab: token %q has count %d", tok, counts[i])
		}
		if i > 0 && (counts[i] > counts[i-1] || counts[i] == counts[i-1] && tok < tokens[i-1]) {
			return nil, fmt.Errorf("vocab: tokens %q and %q are not in rank order", tokens[i-1], tok)
		}
		v.index[tok] = int32(i)
		sum += counts[i]
	}
	if sum > total {
		return nil, fmt.Errorf("vocab: counts sum to %d, more than the total %d", sum, total)
	}
	return v, nil
}

// Len is the number of tokens.
func (v *Vocab) Len() int { return len(v.Tokens) }

// Bag tokenizes text with embed.Tokens, as the runtime does, and counts the
// vocabulary tokens.
func (v *Vocab) Bag(text string) Bag {
	var rows []int32
	for _, tok := range embed.Tokens(text) {
		if i, ok := v.index[tok]; ok {
			rows = append(rows, i)
		}
	}
	slices.Sort(rows)
	var b Bag
	for _, r := range rows {
		if n := len(b.Rows); n > 0 && b.Rows[n-1] == r {
			b.Counts[n-1]++
			continue
		}
		b.Rows = append(b.Rows, r)
		b.Counts = append(b.Counts, 1)
	}
	return b
}

// fixedPoint is the table codec's rule: embed.Tokens of the token yields it first.
// Lowercasing can make a token that is not, such as "i̇" from "İ".
func fixedPoint(tok string) bool {
	got := embed.Tokens(tok)
	return len(got) > 0 && got[0] == tok
}

func allDigits(tok string) bool {
	for _, r := range tok {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
