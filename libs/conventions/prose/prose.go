// Package prose judges prose: the doc comment and name of one symbol a SCIP
// index describes, a hand-written Markdown file, a skill, or a pull request's
// title and description. It parses no programming language, so every
// indexer's symbols meet the same rules.
//
// A doc is read the way go/doc/comment reads one: the common indent comes off,
// a line still indented is preformatted unless it continues a list item, and a
// fenced block is code. Markdown is read the same way once what is not prose
// is blanked (see [JudgeText]). Only the prose that remains is judged.
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
	// RuleReplyVoice reports text that answers a prompt the reader never saw.
	RuleReplyVoice Rule = "reply-voice"
	// RuleTense reports a claim in the future tense or a first-person account
	// of a change.
	RuleTense Rule = "tense"
	// RuleHedge reports a softener qualifying a claim.
	RuleHedge Rule = "hedge"
	// RuleAttribution reports credit to a tool or an agent, or an account of
	// how the work was produced.
	RuleAttribution Rule = "attribution"
	// RuleLeadContext reports a pull request description that does not open
	// with a paragraph naming the goal behind the change.
	RuleLeadContext Rule = "lead-context"
	// RuleTerseSentence reports a skill sentence over 25 words.
	RuleTerseSentence Rule = "terse-sentence"
	// RuleTerseParagraph reports a skill paragraph or list item over 60 words.
	RuleTerseParagraph Rule = "terse-paragraph"
	// RuleWordy reports a phrase with a shorter equivalent in a skill.
	RuleWordy Rule = "wordy"
	// RuleBareRule reports "rule" in a skill with no mechanism named: there a
	// rule is only what magus enforces, and the rest is an instruction.
	RuleBareRule Rule = "bare-rule"
	// RuleTemplate reports a skill body that does not render, so neither of
	// its forms can be judged.
	RuleTemplate Rule = "template"
	// RuleSecondPerson reports we, us, our or ours in a guide, which speaks to
	// the reader as you.
	RuleSecondPerson Rule = "second-person"
	// RuleStepVerb reports a step of a numbered procedure in a guide that does
	// not open with its imperative verb.
	RuleStepVerb Rule = "step-verb"
	// RuleCondescension reports a word in a guide that tells the reader how
	// hard a step should feel: easy, simple, obviously, just, please.
	RuleCondescension Rule = "condescension"
)

// Kind names the kind of text a rule judges.
type Kind string

const (
	// KindDoc is a symbol's doc comment.
	KindDoc Kind = "doc"
	// KindMarkdown is a hand-written Markdown file.
	KindMarkdown Kind = "markdown"
	// KindPullRequest is a pull request: its title on the first line, its
	// description after it.
	KindPullRequest Kind = "pull-request"
	// KindSkill is a skill's SKILL.md as an agent loads it: Markdown held
	// to the terse rules, since every word costs context in every session that
	// loads it.
	KindSkill Kind = "skill"
	// KindSkillSource is a skill body internal/agent renders with
	// text/template into a short and a full form. What the short form shows is
	// judged as KindSkill; what only the full form shows, as
	// KindMarkdown.
	KindSkillSource Kind = "skill-source"
	// KindGuide is a procedural page, one the reader follows with a
	// terminal open: Markdown held to the guide rules as well, which keep it
	// in the second person, its numbered steps imperative and its words free
	// of condescension.
	KindGuide Kind = "guide"
)

var (
	docOnly = []Kind{KindDoc}
	written = []Kind{KindMarkdown, KindGuide, KindPullRequest, KindSkill}
	all     = []Kind{KindDoc, KindMarkdown, KindGuide, KindPullRequest, KindSkill}
	skill   = []Kind{KindSkill}
	guide   = []Kind{KindGuide}
)

// checks run in this order, which is the order [Judge] and [JudgeText] report
// in. A doc keeps the rules it was always judged by: the rules written for
// Markdown and pull requests would hold every doc comment in the tree to
// them at once, with no sweep behind it.
var checks = []struct {
	rule  Rule
	on    []Kind
	judge func(in input) []Finding
}{
	{RuleCommentBlock, docOnly, commentBlock},
	{RuleCommentSentence, docOnly, commentSentence},
	{RuleFiller, all, filler},
	{RuleTerms, all, terms},
	{RuleNameSuffix, docOnly, nameSuffix},
	{RuleAside, docOnly, aside},
	{RuleHistory, docOnly, history},
	{RuleDocStub, docOnly, docStub},
	{RuleLeadContext, []Kind{KindPullRequest}, leadContext},
	{RuleReplyVoice, written, replyVoice},
	{RuleTense, written, tense},
	{RuleHedge, written, hedge},
	{RuleAttribution, written, attribution},
	{RuleTerseSentence, skill, terseSentence},
	{RuleTerseParagraph, skill, terseParagraph},
	{RuleWordy, skill, wordy},
	{RuleBareRule, skill, bareRule},
	{RuleSecondPerson, guide, secondPerson},
	{RuleStepVerb, guide, stepVerb},
	{RuleCondescension, guide, condescension},
	// A skill body that does not render is reported before any rule runs; the
	// entry gives the rule its place in [Rules].
	{RuleTemplate, nil, nil},
}

// Rules returns every rule in the order [Judge] and [JudgeText] report them.
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

// Finding is one rule's verdict.
type Finding struct {
	Rule Rule
	// Message names the fix.
	Message string
	// Match is the offending text, or "" for a rule that judges a budget, where
	// no one span is at fault.
	Match string
	// Line is the 1-based line of the judged text that Match sits on, or that
	// a skill's over-budget sentence or paragraph opens on, or 0 for a doc
	// comment's budget. [Judge] leaves it 0: a doc comment's lines are not its
	// source file's, and the index places the symbol.
	Line int
}

// input is what a rule reads.
type input struct {
	symbol Symbol
	kind   Kind
	prose  []proseLine
	// lines are the judged text's lines with what is not prose blanked, code
	// still in place, for a rule that asks what a block is rather than what it
	// says.
	lines []string
}

// Judge runs the doc rules over s. They judge nothing when Doc is empty, and
// [RuleNameSuffix] judges only a callable. Findings come in [Rules] order,
// then in the order their text appears in Doc.
func Judge(s Symbol) []Finding {
	lines := strings.Split(s.Doc, "\n")
	out := run(input{symbol: s, kind: KindDoc, prose: readProse(lines, false), lines: lines})

	for i := range out {
		out[i].Line = 0
	}

	return out
}

func run(in input) []Finding {
	var out []Finding

	for _, c := range checks {
		if !slices.Contains(c.on, in.kind) {
			continue
		}

		// A rule may scan its lines and its paragraphs in separate passes, so
		// its findings are put back in text order. Budgets carry no line and
		// keep their own order.
		found := c.judge(in)
		slices.SortStableFunc(found, func(a, b Finding) int { return a.Line - b.Line })

		for _, f := range found {
			f.Rule = c.rule
			out = append(out, f)
		}
	}

	return out
}

// proseLine is a line of a doc that renders as prose.
type proseLine struct {
	text string
	// line is the line's 1-based number in the judged text.
	line int
	// start is the offset past the line's indent and any list or heading
	// marker, so a bullet's own hyphen is never read as an aside.
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
	// heading is set on a Markdown ATX heading, which is a paragraph of its
	// own.
	heading bool
}

// body is the line's prose without its indent or list marker.
func (ln proseLine) body() string { return ln.text[ln.start:] }

// proseLines returns the lines of doc that are neither blank, fenced, nor
// preformatted.
func proseLines(doc string) []proseLine { return readProse(strings.Split(doc, "\n"), false) }

// readProse is [proseLines] over raw, one entry per line of the judged text.
// markdown reads an ATX heading as a paragraph of its own; a doc keeps reading
// one as the line it always was.
func readProse(raw []string, markdown bool) []proseLine {
	lines := unfenced(raw)
	unindent(lines)

	var out []proseLine

	inList, opens, paragraph := false, true, true

	for i, text := range lines {
		if strings.TrimSpace(text) == "" {
			// A blank line neither opens nor closes a list: go/doc/comment keeps a
			// list running across the blank line that separates loose items.
			opens, paragraph = true, true

			continue
		}

		body := strings.TrimLeft(text, " \t")
		indent := len(text) - len(body)

		if markdown && indent < 4 {
			if marker := headingMarker(body); marker > 0 {
				out = append(out, proseLine{
					text: text, line: i + 1, start: indent + marker, opens: true, paragraph: true, heading: true,
				})
				inList, opens, paragraph = false, true, true

				continue
			}
		}

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
			text: text, line: i + 1, start: indent + marker, opens: opens, paragraph: paragraph, item: marker > 0,
		})
		opens, paragraph = false, false
	}

	return out
}

// headingMarker returns the length of an ATX heading's hashes plus the space
// after them, or 0 when body is no heading. `#hashtag` is not one.
func headingMarker(body string) int {
	n := 0
	for n < len(body) && body[n] == '#' {
		n++
	}

	switch {
	case n == 0 || n > 6:
		return 0
	case n == len(body):
		return n
	case body[n] != ' ' && body[n] != '\t':
		return 0
	}

	return len(body) - len(strings.TrimLeft(body[n:], " \t"))
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

// paragraph is one paragraph or list item with its lines joined, so a phrase
// or sentence wrapped across lines is read whole.
type paragraph struct {
	text string
	// head is the line that opens it.
	head proseLine
	// starts holds the offset in text where each joined line begins, in order,
	// beside that line's number.
	starts []lineStart
}

type lineStart struct{ offset, line int }

// lineAt returns the line holding offset of text.
func (p paragraph) lineAt(offset int) int {
	line := p.head.line

	for _, s := range p.starts {
		if s.offset > offset {
			break
		}

		line = s.line
	}

	return line
}

// paragraphs joins the bodies of prose into one paragraph per paragraph or
// list item, so none runs from one item into the next. mask rewrites each line
// before the join, preserving its length.
func paragraphs(prose []proseLine, mask func(string) string) []paragraph {
	var out []paragraph

	for _, ln := range prose {
		body := mask(ln.body())
		if ln.opens || len(out) == 0 {
			out = append(out, paragraph{text: body, head: ln, starts: []lineStart{{0, ln.line}}})

			continue
		}

		p := &out[len(out)-1]
		p.text += " "
		p.starts = append(p.starts, lineStart{len(p.text), ln.line})
		p.text += body
	}

	return out
}
