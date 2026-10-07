package prose

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// asideMessage names the fix rather than the sin: the wrong punctuation reads
// fine to everyone except the person who set the rule.
const asideMessage = `Comment uses " - " as an em-dash aside; write a colon, a semicolon, or parentheses instead. ` +
	"A spaced hyphen that belongs to a literal goes in backticks or an indented block, both of which are exempt."

const wrappedMessage = `Comment line ends in " -", carrying an em-dash aside onto the next line; ` +
	"write a colon, a semicolon, or parentheses instead and rewrap the paragraph."

const historyMessage = "Comment narrates a change (%q); describe the code as it stands " +
	"and leave its history to the commit message."

const docStubMessage = "Doc comment only repeats the name %s; state the contract a caller " +
	"relies on (edge cases, errors, ownership), or keep it to what the name cannot say."

// aside reports at most one spaced hyphen per line, so the count stays
// comparable to a grep over the same text. A doc-list bullet, a preformatted
// line and a backtick span are exempt: gofmt writes the first, and the other
// two hold literals.
func aside(in input) []Finding {
	var out []Finding

	for i, ln := range in.prose {
		itemFollows := i+1 < len(in.prose) && in.prose[i+1].item
		if f, ok := scanAside(ln.text, ln.start, itemFollows); ok {
			f.Line = ln.line
			out = append(out, f)
		}
	}

	return out
}

// scanAside judges text from start, which skips a list marker so the bullet's
// own hyphen is exempt while a second one later in the item is not. A line
// ENDING in a spaced hyphen is the same aside with its second clause on the
// next line, unless itemFollows: a list item on the next line is no clause.
func scanAside(text string, start int, itemFollows bool) (Finding, bool) {
	if start >= len(text) {
		return Finding{}, false
	}

	masked := blankBackticks(text)

	for i := start; i < len(masked); {
		j := strings.Index(masked[i:], " - ")
		if j < 0 {
			break
		}

		at := i + j + 1
		if !numeric(masked, at) {
			return Finding{Message: asideMessage, Match: text[wordBefore(text, at):wordAfter(text, at)]}, true
		}

		i = at + 1
	}

	trimmed := strings.TrimRight(masked, " \t")
	if itemFollows || len(trimmed) < start+2 || trimmed[len(trimmed)-1] != '-' {
		return Finding{}, false
	}

	if b := trimmed[len(trimmed)-2]; b != ' ' && b != '\t' {
		return Finding{}, false
	}

	at := len(trimmed) - 1

	return Finding{Message: wrappedMessage, Match: text[wordBefore(text, at) : at+1]}, true
}

// wordBefore returns the offset of the word preceding the hyphen at at, so a
// Match reads as the phrase a reader searches for.
func wordBefore(s string, at int) int {
	i := at
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}

	for i > 0 && s[i-1] != ' ' && s[i-1] != '\t' {
		i--
	}

	return i
}

// wordAfter returns the offset just past the word following the hyphen at at.
func wordAfter(s string, at int) int {
	i := at + 1
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}

	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		i++
	}

	return i
}

// numeric reports whether the hyphen at at has a digit on each side, which
// makes it arithmetic or a range. Neither a colon nor a parenthesis is the fix
// for those.
//
// A hyphen between IDENTIFIERS stays reported. `n - 1` is arithmetic too, but
// it is indistinguishable from prose without reading the surrounding code, and
// the tree this was measured on held one of them against 4513 findings.
func numeric(s string, at int) bool {
	left := at - 1
	for left >= 0 && (s[left] == ' ' || s[left] == '\t') {
		left--
	}

	right := at + 1
	for right < len(s) && (s[right] == ' ' || s[right] == '\t') {
		right++
	}

	return left >= 0 && isDigit(s[left]) && right < len(s) && isDigit(s[right])
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// blankBackticks overwrites every backtick span with a byte that carries no
// meaning to a rule, preserving length so offsets still point at the source.
//
// An unterminated backtick blanks to the end of the line rather than being
// ignored. A span that opens on one line and closes on the next therefore
// hides the first half and not the second, which is a false positive on the
// closing line; carrying the state across lines instead would let one stray
// backtick silence the rest of the doc, and a false negative is the one nobody
// ever reports.
func blankBackticks(s string) string {
	if !strings.ContainsRune(s, '`') {
		return s
	}

	b := []byte(s)

	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++

			continue
		}

		end := i + 1
		for end < len(b) && b[end] != '`' {
			end++
		}

		if end < len(b) {
			end++
		}

		for ; i < end; i++ {
			b[i] = '#'
		}
	}

	return string(b)
}

// listMarker returns the length of the list marker at the head of body plus the
// whitespace after it, or 0 when body does not open a list item.
//
// The markers are the ones go/doc/comment recognizes. A marker with nothing
// after it is not an item, which keeps a line holding a bare hyphen from
// putting the rest of the doc into list mode.
func listMarker(body string) int {
	end := 0

	switch {
	case strings.HasPrefix(body, "-"), strings.HasPrefix(body, "*"), strings.HasPrefix(body, "+"):
		end = 1
	case strings.HasPrefix(body, "•"):
		end = len("•")
	default:
		for end < len(body) && isDigit(body[end]) {
			end++
		}

		if end == 0 || end >= len(body) || (body[end] != '.' && body[end] != ')') {
			return 0
		}

		end++
	}

	rest := body[end:]

	after := strings.TrimLeft(rest, " \t")
	if len(after) == len(rest) {
		return 0
	}

	return len(body) - len(after)
}

// markers open a line that history and docstub never report. Each carries
// meaning a person or tool acts on, which is the opposite of noise.
var markers = []string{"TODO", "FIXME", "BUG", "compat(until:", "compat:", "Deprecated:"}

// marked reports whether any line of doc opens with a marker. The whole doc is
// skipped, since a marker's explanation often continues on the lines below it.
func marked(doc string) bool {
	for _, ln := range strings.Split(doc, "\n") {
		text := strings.TrimSpace(ln)
		for _, m := range markers {
			if !strings.HasPrefix(text, m) {
				continue
			}

			// TODOS or BUGGY is a word, not the marker.
			rest := text[len(m):]
			if strings.HasSuffix(m, ":") || rest == "" || !unicode.IsLetter(rune(rest[0])) {
				return true
			}
		}
	}

	return false
}

// historyPatterns are phrases that only make sense against the previous
// version of the code. Bare "now", "fixed", "instead of" and "correctly" are
// left out: most of their hits describe the code as it stands, "instead of"
// above all, which is how a comment names the alternative it rejected.
var historyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bused to\b`),
	regexp.MustCompile(`(?i)\bpreviously\b`),
	regexp.MustCompile(`(?i)\bnow (?:correctly|properly)\b`),
	regexp.MustCompile(`(?i)\bfixe[sd] (?:a|the) bug\b`),
	regexp.MustCompile(`(?i)\bbug ?fix\b`),
	regexp.MustCompile(`(?i)\bthe old (?:code|behaviou?r|implementation|version)\b`),
}

// purposeLead is the word before a `used to` that states a purpose
// (`is used to sign`) rather than a past habit (`it used to sign`).
var purposeLead = wordSet("is", "are", "was", "were", "be", "been", "being", "get", "gets", "got", "getting")

// history reports the first history phrase on each prose line.
func history(in input) []Finding {
	if marked(in.symbol.Doc) {
		return nil
	}

	var out []Finding

	for _, ln := range in.prose {
		if at, phrase := historyPhrase(ln.text); at >= 0 {
			out = append(out, Finding{Message: fmt.Sprintf(historyMessage, phrase), Match: phrase, Line: ln.line})
		}
	}

	return out
}

// historyPhrase returns the offset and text of the first history phrase in
// text, or -1. Backtick spans are literals and never match.
func historyPhrase(text string) (int, string) {
	masked := blankBackticks(text)

	best, phrase := -1, ""

	for _, re := range historyPatterns {
		for _, m := range re.FindAllStringIndex(masked, -1) {
			if strings.EqualFold(masked[m[0]:m[1]], "used to") && purposeLead[prevWord(masked, m[0])] {
				continue
			}

			if best < 0 || m[0] < best {
				best, phrase = m[0], masked[m[0]:m[1]]
			}

			break
		}
	}

	return best, phrase
}

func prevWord(s string, at int) string {
	end := at
	for end > 0 && s[end-1] == ' ' {
		end--
	}

	start := end
	for start > 0 && unicode.IsLetter(rune(s[start-1])) {
		start--
	}

	return strings.ToLower(s[start:end])
}

// docStub reports a one-line doc that says nothing but the symbol's name and
// stub vocabulary, the shape gocritic's docStub targets: "Foo is a Foo",
// "NewFoo creates a new Foo", "Foo ...".
func docStub(in input) []Finding {
	s := in.symbol
	if s.Name == "" || marked(s.Doc) {
		return nil
	}

	text := strings.TrimSpace(s.Doc)
	if text == "" || strings.Contains(text, "\n") || spellsSyntax(text) {
		return nil
	}

	// words rather than identWords: a display name from another language can
	// carry a sigil or a dot that identWords would keep inside a word.
	known := map[string]bool{}
	for _, w := range words(s.Name) {
		known[w] = true
	}

	for _, w := range words(s.Owner) {
		known[w] = true
	}

	for _, w := range words(text) {
		if !known[w] && !stubWords[w] && !stopWords[w] {
			return nil
		}
	}

	return []Finding{{Message: fmt.Sprintf(docStubMessage, s.Name), Match: text}}
}

// spellsSyntax reports whether text holds what no name can say: a token that
// is not a plain word (an operator, a flag, a quoted literal, a bracket), or no
// letter at all. Such a doc is the token or grammar its symbol stands for, as
// in `OpBXor // ^` or `BlockStmt // BlockStmt: { stmt* }`. Sentence
// punctuation, parentheses and an ellipsis are prose, so `Close ...` is still
// a stub.
func spellsSyntax(text string) bool {
	if !strings.ContainsFunc(text, unicode.IsLetter) {
		return true
	}

	for _, token := range strings.Fields(text) {
		token = strings.TrimLeft(strings.TrimRight(token, ".,;:!?…)"), "(")
		if token != "" && !plainWord(token) {
			return true
		}
	}

	return false
}

// plainWord reports whether token is a word or an identifier: letters, digits
// and underscores, joined inside by the dot of a qualified name, a hyphen or an
// apostrophe.
func plainWord(token string) bool {
	for i, r := range token {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_':
		case (r == '.' || r == '-' || r == '\'' || r == '’') && i > 0 && i+utf8.RuneLen(r) < len(token):
		default:
			return false
		}
	}

	return true
}

// stubWords is the vocabulary a stub pads a name with, stemmed. "implement" is
// absent on purpose: "String implements fmt.Stringer" names the contract.
var stubWords = wordSet("return", "create", "new", "instance", "function", "func", "method",
	"type", "struct", "constructor", "represent", "define", "get", "set", "value", "object",
	"variable", "constant", "const", "var", "given", "specified", "provided", "construct",
	"build", "make", "initialize", "init")

var stopWords = wordSet("a", "an", "the", "this", "that", "these", "those", "to", "of", "for",
	"in", "on", "at", "by", "with", "from", "into", "onto", "and", "or", "is", "are", "be",
	"it", "its", "as", "we", "our", "then", "here", "there", "all", "each", "every", "if",
	"so", "any", "some", "just")

// words splits text into lowercased, stemmed words with the stop words
// dropped, splitting identifiers written in prose the way identWords does.
func words(text string) []string {
	var out []string

	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		for _, w := range identWords(field) {
			if !stopWords[w] {
				out = append(out, w)
			}
		}
	}

	return out
}

// identWords splits an identifier at case and underscore boundaries, keeping an
// acronym whole: HTTPServer is http and server. The words come back stemmed.
func identWords(ident string) []string {
	var out []string

	runes := []rune(ident)
	start := 0

	flush := func(end int) {
		if end > start {
			out = append(out, stem(strings.ToLower(string(runes[start:end]))))
		}

		start = end
	}

	for i, r := range runes {
		switch {
		case r == '_':
			flush(i)
			start = i + 1
		case i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]):
			flush(i)
		case i > 0 && i+1 < len(runes) && unicode.IsUpper(r) && unicode.IsUpper(runes[i-1]) &&
			unicode.IsLower(runes[i+1]):
			flush(i)
		}
	}

	flush(len(runes))

	return out
}

// stem folds a plural or third-person s, which is all docstub needs to match
// "creates" to create and "users" to user.
func stem(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}

	return w
}

func wordSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}

	return m
}
