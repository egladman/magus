package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Protected is a span a fix should leave alone: a specific that shows a
// person wrote the text, which a rewrite toward the rules tends to drop.
type Protected struct {
	// Text is the span as written, and Reason what kind of specific it is.
	Text   string `json:"text"`
	Reason string `json:"reason"`
	// Line is the span's 1-based line, and Column and EndColumn its 1-based
	// byte columns, the end just past it.
	Line      int `json:"line"`
	Column    int `json:"column"`
	EndColumn int `json:"end_column"`
}

const (
	reasonNumber   = "a number with its unit"
	reasonAnecdote = "a moment the author saw"
	reasonName     = "a name"
)

var (
	unitNumber = regexp.MustCompile(`\b\d+(?:[.,]\d+)*(?:%|\s?(?:ms|s|seconds?|minutes?|mins?|hours?|days?|weeks?|` +
		`months?|years?|[KMGT]i?B|px|pixels|words|lines|files|bytes|characters|runs|tests|times)\b)`)
	anecdote = regexp.MustCompile(`\b(?i:until|when|after)\s+(?:I|we|you|he|she|they|a|an|the|my|our|their|his|` +
		`her|[A-Z][\w-]*)\s[^,.;:!?()]{3,60}`)
	// properName is one capitalized word between lowercase ones, so it opens
	// no sentence and is no Title Case label.
	properName = regexp.MustCompile(`(?:^|\s)[a-z][\w'\x{2019}-]*,?\s+([A-Z][a-z]+)\s+[a-z]`)
)

// protectedSpans finds the protected spans of source outside code spans.
func protectedSpans(source []string) []Protected {
	var out []Protected

	for i, line := range source {
		code := anySpan.FindAllStringIndex(line, -1)

		add := func(start, end int, reason string) {
			end = start + len(strings.TrimRight(line[start:end], " "))
			if slices.ContainsFunc(code, func(c []int) bool { return start >= c[0] && start < c[1] }) {
				return
			}

			out = append(out, Protected{
				Text: line[start:end], Reason: reason, Line: i + 1, Column: start + 1, EndColumn: end + 1,
			})
		}

		for _, m := range unitNumber.FindAllStringIndex(line, -1) {
			add(m[0], m[1], reasonNumber)
		}

		for _, m := range anecdote.FindAllStringIndex(line, -1) {
			add(m[0], m[1], reasonAnecdote)
		}

		for _, m := range properName.FindAllStringSubmatchIndex(line, -1) {
			add(m[2], m[3], reasonName)
		}
	}

	return out
}

// protect marks each economy finding whose Match holds a protected span
// whole: cutting those words would drop a specific the author wrote. Other
// dimensions ask for evidence, markup or structure, which keep the words.
func protect(out []Finding, spans []Protected) []Finding {
	for i, f := range out {
		if f.Column == 0 || ruleTexts[f.Rule].dimension != DimensionEconomy {
			continue
		}

		for _, s := range spans {
			if s.Line == f.Line && f.Column <= s.Column && s.EndColumn <= f.EndColumn {
				out[i].Protected = append(out[i].Protected, s)
			}
		}

		if len(out[i].Protected) == 0 {
			continue
		}

		names := make([]string, len(out[i].Protected))
		for j, s := range out[i].Protected {
			names[j] = fmt.Sprintf("'%s', %s", s.Text, s.Reason)
		}

		out[i].Message += " Keep " + strings.Join(names, " and ") + ", if you rewrite it."

		if out[i].Decision == DecisionDeny {
			out[i].Decision = DecisionAdvise
		}
	}

	return out
}
