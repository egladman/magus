package probe

import (
	"cmp"
	"slices"
	"unicode/utf8"
)

// TypedBytes is how much of a span's middle the replayed user has typed when the
// measured request goes out: about 64 new prompt tokens, the figure plan 4.1's warm
// single-line row assumes.
const TypedBytes = 240

// Cursor is one FIM dev span as the replay reads it.
type Cursor struct {
	Group  string
	Path   string
	Start  int
	Prefix string
	Middle string
	Suffix string
}

// Pair is one replayed keystroke burst. Primer is the request at the span's start, which
// leaves the editor's cache warm; Request is the request after TypedBytes more of the
// middle are typed, with the file text after the cursor unchanged. Under SPM order the
// suffix leads the prompt, so the primer's whole prompt is a prefix of the request's and
// only the typed bytes need evaluating.
type Pair struct {
	Group  string
	Primer string
	Prefix string
	Suffix string
	Typed  int
}

// Typing builds up to max pairs from cursors in typing order: by file, then by position
// in it. A span with an empty middle has nothing to type and is left out. The typed part
// ends on a character boundary.
func Typing(cs []Cursor, max int) []Pair {
	sorted := slices.Clone(cs)
	slices.SortStableFunc(sorted, func(a, b Cursor) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Start, b.Start))
	})
	var out []Pair
	for _, c := range sorted {
		if c.Middle == "" {
			continue
		}
		n := min(TypedBytes, len(c.Middle))
		for n > 0 && n < len(c.Middle) && !utf8.RuneStart(c.Middle[n]) {
			n--
		}
		out = append(out, Pair{Group: c.Group, Primer: c.Prefix, Prefix: c.Prefix + c.Middle[:n], Suffix: c.Suffix, Typed: n})
		if max > 0 && len(out) == max {
			break
		}
	}
	return out
}
