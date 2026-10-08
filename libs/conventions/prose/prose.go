// Package prose judges the prose of one symbol a SCIP index describes: its doc
// comment, and the name of a function or method. It reads no source file and
// parses no language, so every indexer's symbols meet the same rules.
//
// A doc is read the way go/doc/comment reads one: the common indent comes off,
// a line still indented is preformatted unless it continues a list item, and a
// fenced block is code. Only the prose that remains is judged.
package prose

import (
	"slices"
	"strings"
)

// Rule names one check. It is a [Finding]'s Rule and what a caller lists to
// leave a rule out of its report.
type Rule string

const (
	// RuleCommentBlock reports a doc paragraph over 250 words.
	RuleCommentBlock Rule = "comment-block"
	// RuleCommentSentence reports a doc sentence over 60 words.
	RuleCommentSentence Rule = "comment-sentence"
	// RuleFiller reports throat-clearing and filler adverbs.
	RuleFiller Rule = "filler"
	// RuleTerms reports a spelling the glossary replaces.
	RuleTerms Rule = "terms"
	// RuleNameSuffix reports a function or method name whose last word is Of or For.
	RuleNameSuffix Rule = "name-suffix"
	// RuleAside reports a spaced hyphen spelling an em-dash.
	RuleAside Rule = "aside"
	// RuleHistory reports a phrase narrating the change rather than the code.
	RuleHistory Rule = "history"
	// RuleDocStub reports a one-line doc that only repeats the symbol's name.
	RuleDocStub Rule = "docstub"
)

// checks run in this order, which is the order [Judge] reports in.
var checks = []struct {
	rule  Rule
	judge func(s Symbol, prose []proseLine) []Finding
}{
	{RuleCommentBlock, commentBlock},
	{RuleCommentSentence, commentSentence},
	{RuleFiller, filler},
	{RuleTerms, terms},
	{RuleNameSuffix, nameSuffix},
	{RuleAside, aside},
	{RuleHistory, history},
	{RuleDocStub, docStub},
}

// Rules returns every rule in the order [Judge] reports them.
func Rules() []Rule {
	out := make([]Rule, len(checks))
	for i, c := range checks {
		out[i] = c.rule
	}

	return out
}

// Symbol is what an index records about one declaration.
type Symbol struct {
	// Name is the SCIP display name.
	Name string
	// Owner is the display name of the enclosing type when Name is a method.
	Owner string
	// Callable marks a function or method, the only symbols whose name is judged.
	Callable bool
	// Doc is the doc comment, with any signature code block the indexer
	// prepends already dropped.
	Doc string
}

// Finding is one rule's verdict on a [Symbol].
type Finding struct {
	Rule Rule
	// Message names the fix.
	Message string
	// Match is the offending text, or "" for a rule that judges a budget, where
	// no one span is at fault.
	Match string
}

// Judge runs every rule over s. Doc rules judge nothing when Doc is empty, and
// [RuleNameSuffix] judges only a callable. Findings come in [Rules] order, then
// in the order their text appears in Doc.
func Judge(s Symbol) []Finding {
	prose := proseLines(s.Doc)

	var out []Finding

	for _, c := range checks {
		for _, f := range c.judge(s, prose) {
			f.Rule = c.rule
			out = append(out, f)
		}
	}

	return out
}

// proseLine is a line of a doc that renders as prose.
type proseLine struct {
	text string
	// start is the offset past the line's indent and any list marker, so a
	// bullet's own hyphen is never read as an aside.
	start int
	// opens is set where no sentence runs on from the line before: on the
	// first line of a paragraph and on every list item.
	opens bool
	// paragraph is set on the first line of a paragraph as Vale's text scope
	// reads one: after a blank or a dropped line, and where a list starts or
	// ends. A list with no blank line between its items is one paragraph, so
	// its later items set opens and not paragraph.
	paragraph bool
	// item is set on a line that opens a list item.
	item bool
}

// body is the line's prose without its indent or list marker.
func (ln proseLine) body() string { return ln.text[ln.start:] }

// proseLines returns the lines of doc that are neither blank, fenced, nor
// preformatted.
func proseLines(doc string) []proseLine {
	lines := unfenced(strings.Split(doc, "\n"))
	unindent(lines)

	var out []proseLine

	inList, opens, paragraph := false, true, true

	for _, text := range lines {
		if strings.TrimSpace(text) == "" {
			// A blank line neither opens nor closes a list: go/doc/comment keeps a
			// list running across the blank line that separates loose items.
			opens, paragraph = true, true

			continue
		}

		body := strings.TrimLeft(text, " \t")
		indent := len(text) - len(body)
		marker := listMarker(body)

		switch {
		case marker > 0:
			paragraph = paragraph || !inList
			inList, opens = true, true
		case indent > 0 && inList:
			// A wrapped list item is prose, not code, however deep it sits.
		case indent > 0:
			opens, paragraph = true, true

			continue
		case inList:
			inList, opens, paragraph = false, true, true
		}

		out = append(out, proseLine{
			text: text, start: indent + marker, opens: opens, paragraph: paragraph, item: marker > 0,
		})
		opens, paragraph = false, false
	}

	return out
}

// unfenced blanks every line of a fenced code block, fences included, so the
// lines around it still read as separate paragraphs.
func unfenced(lines []string) []string {
	out := slices.Clone(lines)
	fence := ""

	for i, ln := range out {
		trimmed := strings.TrimLeft(ln, " \t")

		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "```"):
			fence = "```"
		case strings.HasPrefix(trimmed, "~~~"):
			fence = "~~~"
		default:
			continue
		}

		out[i] = ""
	}

	return out
}

// unindent removes the whitespace prefix every non-blank line shares, which
// go/doc/comment does before deciding which lines are preformatted. Without it
// a doc whose every line is indented would read as one long code block.
func unindent(lines []string) {
	prefix, found := "", false

	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}

		lead := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
		if !found {
			prefix, found = lead, true

			continue
		}

		prefix = commonPrefix(prefix, lead)
	}

	if prefix == "" {
		return
	}

	for i, ln := range lines {
		lines[i] = strings.TrimPrefix(ln, prefix)
	}
}

func commonPrefix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}

	return a[:n]
}

// paragraphs joins the bodies of prose into one line per paragraph or list
// item, so a phrase or sentence wrapped across lines is read whole and none
// runs from one item into the next. mask rewrites each line before the join.
func paragraphs(prose []proseLine, mask func(string) string) []string {
	var out []string

	for _, ln := range prose {
		body := mask(ln.body())
		if ln.opens || len(out) == 0 {
			out = append(out, body)

			continue
		}

		out[len(out)-1] += " " + body
	}

	return out
}
