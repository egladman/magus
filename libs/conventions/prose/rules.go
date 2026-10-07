package prose

import (
	"fmt"
	"regexp"
	"strings"
)

// Budgets measured 2026-10-06 over 46664 comment blocks: a block's p50 is 25
// words, p90 72, p99 168; a sentence's p50 is 17 words, p90 34, p99 50.
const (
	maxBlockWords    = 250
	maxSentenceWords = 60
)

// fillerPattern matches case-sensitively. Throat-clearing opens a sentence ("Note
// that", "This function"), so those match only capitalized: "a note that ..."
// names the notes feature, and "this function's job" is a contract. The
// adverbs are filler wherever they sit.
var fillerPattern = regexp.MustCompile(`\b(?:Note that|Please note|It should be noted|It is worth noting|` +
	`It's worth noting|It is important to|It's important to|This function|This method|` +
	`[Ss]imply|[Bb]asically|[Ee]ssentially|[Nn]eedless to say)\b`)

// termsPattern holds the spellings docs/glossary.md replaces. "Magus" is left
// out: in a doc it usually names the Go type, which no rule can tell from the
// tool.
var termsPattern = regexp.MustCompile(`(?i)\bsub-agents?\b`)

// nameSuffixPattern matches a name whose last word is Of or For, in camel or
// snake case, after at least one other word. A name ending in Of or For
// describes its argument rather than what it returns.
var nameSuffixPattern = regexp.MustCompile(`\b(?:\w*[a-z0-9](?:Of|For)|\w+_(?:of|for))\b`)

func commentBlock(_ Symbol, prose []proseLine) []Finding {
	n := 0
	for _, ln := range prose {
		n += len(strings.Fields(ln.body()))
	}

	if n <= maxBlockWords {
		return nil
	}

	return []Finding{{Message: "Keep a comment block under 250 words: say why, and move the rest to docs."}}
}

func commentSentence(_ Symbol, prose []proseLine) []Finding {
	var out []Finding

	for _, para := range paragraphs(prose, keep) {
		n := 0
		for _, token := range strings.Fields(para) {
			n++

			if !endsSentence(token) {
				continue
			}

			if n > maxSentenceWords {
				out = append(out, Finding{Message: "Keep a comment sentence under 60 words: split it."})
			}

			n = 0
		}

		if n > maxSentenceWords {
			out = append(out, Finding{Message: "Keep a comment sentence under 60 words: split it."})
		}
	}

	return out
}

// endsSentence reports whether token closes a sentence, looking past closing
// quotes and brackets.
func endsSentence(token string) bool {
	token = strings.TrimRight(token, `"')]`+"`")

	return strings.HasSuffix(token, ".") || strings.HasSuffix(token, "!") || strings.HasSuffix(token, "?")
}

func filler(_ Symbol, prose []proseLine) []Finding {
	var out []Finding

	for _, para := range paragraphs(prose, blankBackticks) {
		for _, m := range fillerPattern.FindAllString(para, -1) {
			out = append(out, Finding{Message: fmt.Sprintf("Drop '%s': state the fact.", m), Match: m})
		}
	}

	return out
}

func terms(_ Symbol, prose []proseLine) []Finding {
	var out []Finding

	for _, para := range paragraphs(prose, blankBackticks) {
		for _, m := range termsPattern.FindAllString(para, -1) {
			want := "subagent"
			if strings.HasSuffix(strings.ToLower(m), "s") {
				want += "s"
			}

			out = append(out, Finding{Message: fmt.Sprintf("Write '%s', not '%s'.", want, m), Match: m})
		}
	}

	return out
}

func nameSuffix(s Symbol, _ []proseLine) []Finding {
	if !s.Callable {
		return nil
	}

	m := nameSuffixPattern.FindString(s.Name)
	if m == "" {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Rename '%s': no function or method name ends in the word Of or For.", m),
		Match:   m,
	}}
}

func keep(s string) string { return s }
