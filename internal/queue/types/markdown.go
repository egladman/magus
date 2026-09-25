package types

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxClaim bounds a [Kick]'s Claim in bytes.
const MaxClaim = 4 << 10

// CodeSpan renders s as a Markdown inline code span showing s as text: no mention,
// link, reference or HTML inside it is read. An s holding invalid UTF-8 or a rune that
// is not printable (a line break, a control character, a bidirectional override) is
// shown Go-quoted, so the span stays on one line and shows every byte.
func CodeSpan(s string) string {
	if s == "" || !utf8.ValidString(s) || strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		s = strconv.Quote(s)
	}
	fence := strings.Repeat("`", longestRun(s, '`')+1)
	// CommonMark strips one space from each end of a span whose content starts and ends
	// with one, and a backtick against the fence would lengthen it.
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		s = " " + s + " "
	}
	return fence + s + fence
}

// CodeBlock renders s as a fenced Markdown code block showing s as text, lines and
// all, ending in a line break. The fence is longer than any run of backticks in s, so
// no line of s closes it. Invalid UTF-8 is shown as U+FFFD.
func CodeBlock(s string) string {
	s = strings.TrimSuffix(strings.ToValidUTF8(s, string(utf8.RuneError)), "\n")
	fence := strings.Repeat("`", max(3, longestRun(s, '`')+1))
	return fence + "text\n" + s + "\n" + fence + "\n"
}

func longestRun(s string, c byte) int {
	longest, run := 0, 0
	for i := range len(s) {
		if s[i] != c {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}
