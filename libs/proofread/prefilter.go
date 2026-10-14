package proofread

import (
	"iter"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// prefilter is a necessary condition for a regexp to match: every match,
// folded, starts with one of its literals. Folding lowercases ASCII letters
// and turns each run of the bytes \s matches into one space, so "In  Order"
// folds to "in order".
type prefilter struct {
	// single holds the first byte of each one-byte literal.
	single [4]uint64
	// pairs holds the first two bytes of every longer literal, and byPair the
	// literals under them.
	pairs  [1024]uint64
	byPair map[uint16][]string
	// atStart matches the pattern at the start of a text, and afterRune one
	// rune into it, so a match at an offset still sees the rune before it.
	atStart, afterRune *regexp.Regexp
}

// maxLiterals caps the strings one pattern's analysis carries.
const maxLiterals = 8192

// enoughLiteral is the length past which a literal stops growing: eight bytes
// already rule out nearly every position.
const enoughLiteral = 8

var prefilters sync.Map // *regexp.Regexp -> *prefilter

func loadPrefilter(re *regexp.Regexp) *prefilter {
	f, ok := prefilters.Load(re)
	if !ok {
		f, _ = prefilters.LoadOrStore(re, newPrefilter(re))
	}

	return f.(*prefilter)
}

// mayMatch reports whether re can match s. A false answer is exact; a true one
// only means s holds a literal every match starts with.
//
// optimization: skip a regexp when s lacks every literal its matches start with.
//
//	measured: BenchmarkJudgeText -68.8% ns/op geomean, reference 69.1ms to 20.9ms, -7.5% allocs/op,
//	          gating every hot pattern (benchstat, n=10).
//	trade-off: each pattern's syntax is parsed a second time, and analyze must
//	  stay sound for every syntax.Op it does not answer unknown to.
//	assumes:  patterns compile with syntax.Perl, as regexp.MustCompile does.
func mayMatch(re *regexp.Regexp, s string) bool {
	f := loadPrefilter(re)
	if f == nil || foldsOutsideASCII(s) {
		return true
	}

	for range f.starts(s) {
		return true
	}

	return false
}

// findAllIndex is re.FindAllStringIndex(s, -1), and with submatch
// re.FindAllStringSubmatchIndex(s, -1).
//
// optimization: try re only where a literal its matches start with occurs.
//
//	measured: BenchmarkJudgeText -24.6% ns/op geomean over mayMatch gating (review-reply -48%,
//	          reference -28%); commit-message +8.4%, its short paragraphs paying the whole scan
//	          (benchstat, n=10).
//	trade-off: two more compiled copies of each pattern, and starts must yield
//	  every offset a match can start at; TestFindAllIndexMatchesRegexp holds it to that.
//	assumes:  no pattern with a prefilter matches the empty string, which a
//	  literal set without "" guarantees.
func findAllIndex(re *regexp.Regexp, s string, submatch bool) [][]int {
	f := loadPrefilter(re)
	if f == nil || foldsOutsideASCII(s) {
		if submatch {
			return re.FindAllStringSubmatchIndex(s, -1)
		}

		return re.FindAllStringIndex(s, -1)
	}

	var out [][]int

	end := 0

	for at := range f.starts(s) {
		if at < end {
			continue
		}

		if m := f.matchAt(s, at, submatch); m != nil {
			out = append(out, m)
			end = m[1]
		}
	}

	return out
}

// matchAt returns the match of f's pattern that starts at offset at of s, as
// an unanchored search of s finds it there.
func (f *prefilter) matchAt(s string, at int, submatch bool) []int {
	re, from := f.atStart, 0
	if at > 0 {
		_, width := utf8.DecodeLastRuneInString(s[:at])
		re, from = f.afterRune, at-width
	}

	var m []int
	if submatch {
		m = re.FindStringSubmatchIndex(s[from:])
	} else {
		m = re.FindStringIndex(s[from:])
	}

	if m == nil {
		return nil
	}

	for i := range m {
		if m[i] >= 0 {
			m[i] += from
		}
	}

	m[0] = at

	return m
}

// foldsOutsideASCII reports whether s holds a long s or a Kelvin sign, which
// case-fold to an ASCII s and k that folding does not compare.
func foldsOutsideASCII(s string) bool {
	return strings.Contains(s, "ſ") || strings.Contains(s, "K")
}

// newPrefilter returns nil when some match of re can start with any text.
func newPrefilter(re *regexp.Regexp) *prefilter {
	parsed, err := syntax.Parse(re.String(), syntax.Perl)
	if err != nil {
		return nil
	}

	f := &prefilter{
		byPair:    map[uint16][]string{},
		atStart:   regexp.MustCompile(`^(?:` + re.String() + `)`),
		afterRune: regexp.MustCompile(`^(?s:.)(?:` + re.String() + `)`),
	}

	for _, lit := range analyze(parsed.Simplify()).set {
		switch len(lit) {
		case 0:
			return nil
		case 1:
			f.single[lit[0]/64] |= 1 << (lit[0] % 64)
		default:
			pair := uint16(lit[0])<<8 | uint16(lit[1])
			f.pairs[pair/64] |= 1 << (pair % 64)
			f.byPair[pair] = append(f.byPair[pair], lit)
		}
	}

	return f
}

// starts yields, in order, every offset of s where folded s holds one of f's
// literals. A literal that opens with a space yields each offset of the run of
// spaces it meets, since a match may start inside the run.
func (f *prefilter) starts(s string) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 0; i < len(s); i++ {
			b := foldByte(s[i])
			if b == ' ' && i > 0 && foldByte(s[i-1]) == ' ' {
				continue
			}

			next := i + 1
			for b == ' ' && next < len(s) && foldByte(s[next]) == ' ' {
				next++
			}

			if !f.hit(s, i, b, next) {
				continue
			}

			for at := i; at < next; at++ {
				if !yield(at) {
					return
				}
			}
		}
	}
}

// hit reports whether a literal of f starts at offset i of s, whose folded
// byte is b and whose next folded byte sits at offset next.
func (f *prefilter) hit(s string, i int, b byte, next int) bool {
	if f.single[b/64]&(1<<(b%64)) != 0 {
		return true
	}

	if next == len(s) {
		return false
	}

	pair := uint16(b)<<8 | uint16(foldByte(s[next]))
	if f.pairs[pair/64]&(1<<(pair%64)) == 0 {
		return false
	}

	for _, lit := range f.byPair[pair] {
		if foldedPrefix(s[i:], lit) {
			return true
		}
	}

	return false
}

// foldedPrefix reports whether s, folded, starts with lit.
func foldedPrefix(s, lit string) bool {
	j := 0

	for k := 0; k < len(lit); k++ {
		if j == len(s) {
			return false
		}

		b := foldByte(s[j])
		if b != lit[k] {
			return false
		}

		j++

		for b == ' ' && j < len(s) && foldByte(s[j]) == ' ' {
			j++
		}
	}

	return true
}

func foldByte(b byte) byte {
	switch {
	case 'A' <= b && b <= 'Z':
		return b + 'a' - 'A'
	case b == '\t', b == '\n', b == '\f', b == '\r':
		return ' '
	}

	return b
}

// fold folds s as starts reads text.
func fold(s string) string {
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		c := foldByte(s[i])
		if c == ' ' && i > 0 && foldByte(s[i-1]) == ' ' {
			continue
		}

		b.WriteByte(c)
	}

	return b.String()
}

// literalSet describes the folded strings a node matches. When exact, every
// match folds to one of set; otherwise every match, folded, starts with one of
// set, and a set holding "" says nothing.
type literalSet struct {
	set   []string
	exact bool
}

var unknown = literalSet{set: []string{""}}

// analyze answers unknown for any Op it does not handle, which is always sound.
func analyze(re *syntax.Regexp) literalSet {
	switch re.Op {
	case syntax.OpNoMatch:
		return literalSet{exact: true}
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return literalSet{set: []string{""}, exact: true}
	case syntax.OpLiteral:
		lit, ok := foldedLiteral(re.Rune, re.Flags&syntax.FoldCase != 0)
		if !ok {
			return unknown
		}

		return literalSet{set: []string{lit}, exact: true}
	case syntax.OpCharClass:
		return classLiterals(re.Rune)
	case syntax.OpCapture:
		return analyze(re.Sub[0])
	case syntax.OpPlus:
		return repeated(analyze(re.Sub[0]))
	case syntax.OpStar:
		if sub := repeated(analyze(re.Sub[0])); sub.exact {
			return literalSet{set: union(sub.set, []string{""}), exact: true}
		}

		return unknown
	case syntax.OpQuest:
		sub := analyze(re.Sub[0])

		return literalSet{set: union(sub.set, []string{""}), exact: sub.exact}
	case syntax.OpConcat:
		return concatLiterals(re.Sub)
	case syntax.OpAlternate:
		out := literalSet{exact: true}

		for _, sub := range re.Sub {
			s := analyze(sub)
			out.set, out.exact = union(out.set, s.set), out.exact && s.exact

			if len(out.set) > maxLiterals {
				return unknown
			}
		}

		return out
	}

	return unknown
}

// repeated is one or more of sub. A run of spaces folds to one, so a repeated
// space stays exact; anything else repeated only starts with sub.
func repeated(sub literalSet) literalSet {
	if sub.exact && slices.Equal(sub.set, []string{" "}) {
		return sub
	}

	return literalSet{set: sub.set}
}

func concatLiterals(subs []*syntax.Regexp) literalSet {
	acc := literalSet{set: []string{""}, exact: true}

	for _, sub := range subs {
		if !acc.exact {
			break
		}

		if !slices.ContainsFunc(acc.set, func(s string) bool { return len(s) < enoughLiteral }) {
			return literalSet{set: acc.set}
		}

		next := analyze(sub)
		if len(acc.set)*len(next.set) > maxLiterals {
			return literalSet{set: acc.set}
		}

		product := make([]string, 0, len(acc.set)*len(next.set))

		for _, a := range acc.set {
			for _, b := range next.set {
				product = append(product, fold(a+b))
			}
		}

		acc = literalSet{set: union(nil, product), exact: next.exact}
	}

	return acc
}

// foldedLiteral folds runes, or reports false when one case-folds to a rune
// outside ASCII, which starts does not compare.
func foldedLiteral(runes []rune, caseFold bool) (string, bool) {
	var b strings.Builder

	for _, r := range runes {
		if r >= utf8.RuneSelf && caseFold && unicode.SimpleFold(r) != r {
			return "", false
		}

		b.WriteRune(r)
	}

	return fold(b.String()), true
}

// maxClassRunes is the widest class whose runes become literals.
const maxClassRunes = 16

func classLiterals(ranges []rune) literalSet {
	n := 0
	for i := 0; i < len(ranges); i += 2 {
		n += int(ranges[i+1]-ranges[i]) + 1
		if n > maxClassRunes {
			return unknown
		}
	}

	var out []string

	for i := 0; i < len(ranges); i += 2 {
		for r := ranges[i]; r <= ranges[i+1]; r++ {
			// foldsOutsideASCII sends any text holding either to the plain search.
			if r == 'ſ' || r == 'K' {
				continue
			}

			out = append(out, fold(string(r)))
		}
	}

	return literalSet{set: union(nil, out), exact: true}
}

func union(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)

	return slices.Compact(out)
}
