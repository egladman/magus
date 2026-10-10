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
	// RuleCondescension reports a word that tells the reader how hard a step
	// should feel or what they should already know: in a guide easy, simple,
	// obviously, just, please; in other written text and in a reply the words
	// that presume ("of course", "everyone knows").
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
	// KindReply is a review comment, a review's body or a reply in a review
	// thread: Markdown with no title line.
	KindReply Kind = "reply"
)

// Severity is what a finding costs the caller that reads it.
type Severity string

const (
	// SeverityError is a finding a gate refuses.
	SeverityError Severity = "error"
	// SeverityAdvisory is a finding a gate reports and lets through: its rule's
	// words also have senses the rule cannot tell apart from the one it means.
	SeverityAdvisory Severity = "advisory"
)

// Profile names a set of rules a writer is held to. The zero value is
// [ProfilePlain].
type Profile string

const (
	// ProfilePlain is every rule, this repository's own house style included.
	ProfilePlain Profile = "plain"
	// ProfileCollaborative leaves out the rules that encode this repository's
	// house style, so text written for a team elsewhere is judged for tone,
	// claims and generated-writing tells alone.
	ProfileCollaborative Profile = "collaborative"
)

// profileSkips lists the rules a profile leaves out. Each rule
// ProfileCollaborative skips encodes this repository rather than writing a
// teammate reads: terms is its glossary, tense its voice (the present tense,
// and no author in a description, where a team writes "we" and "I"), and
// bare-rule its own meaning of "rule".
var profileSkips = map[Profile][]Rule{
	ProfileCollaborative: {RuleTerms, RuleTense, RuleBareRule},
}

var (
	docOnly = []Kind{KindDoc}
	written = []Kind{KindMarkdown, KindGuide, KindPullRequest, KindSkill}
	all     = []Kind{KindDoc, KindMarkdown, KindGuide, KindPullRequest, KindSkill}
	skill   = []Kind{KindSkill}
	guide   = []Kind{KindGuide}
	// teammate is text written to the people working on the change, where a
	// sentence about past work or a teammate is about someone the reader knows.
	teammate = []Kind{KindPullRequest, KindReply}
)

// check is one rule: the kinds it judges, the kinds where its findings are
// [SeverityAdvisory] rather than [SeverityError], and the judge itself.
type check struct {
	rule     Rule
	on       []Kind
	advisory []Kind
	judge    func(in input) []Finding
}

// checks run in this order, which is the order [Judge] and [JudgeText] report
// in. A doc keeps the rules it was always judged by: the rules written for
// Markdown and pull requests would hold every doc comment in the tree to
// them at once, with no sweep behind it.
var checks = slices.Concat(coreChecks, toneChecks, slopChecks, []check{
	// A skill body that does not render is reported before any rule runs; the
	// entry gives the rule its place in [Rules].
	{rule: RuleTemplate},
})

var coreChecks = []check{
	{rule: RuleCommentBlock, on: docOnly, judge: commentBlock},
	{rule: RuleCommentSentence, on: docOnly, judge: commentSentence},
	{rule: RuleFiller, on: everywhere, judge: filler},
	{rule: RuleTerms, on: everywhere, judge: terms},
	{rule: RuleNameSuffix, on: docOnly, judge: nameSuffix},
	{rule: RuleAside, on: docOnly, judge: aside},
	{rule: RuleHistory, on: docOnly, judge: history},
	{rule: RuleDocStub, on: docOnly, judge: docStub},
	{rule: RuleLeadContext, on: []Kind{KindPullRequest}, judge: leadContext},
	{rule: RuleReplyVoice, on: withReply, judge: replyVoice},
	{rule: RuleTense, on: written, judge: tense},
	{rule: RuleHedge, on: written, judge: hedge},
	{rule: RuleAttribution, on: withReply, judge: attribution},
	{rule: RuleTerseSentence, on: skill, judge: terseSentence},
	{rule: RuleTerseParagraph, on: skill, judge: terseParagraph},
	{rule: RuleWordy, on: skill, judge: wordy},
	{rule: RuleBareRule, on: skill, judge: bareRule},
	{rule: RuleSecondPerson, on: guide, judge: secondPerson},
	{rule: RuleStepVerb, on: guide, judge: stepVerb},
	{rule: RuleCondescension, on: withReply, judge: condescension},
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
	// Severity is set by the rule's entry for the kind judged, unless the rule
	// marked the finding [SeverityAdvisory] itself: a rule whose words are sure
	// in one reading and a guess in another reports the guess that way.
	Severity Severity
}

// Option tunes one [Judge] or [JudgeText] call.
type Option func(*options)

type options struct {
	profile      Profile
	only, skip   []Rule
	threadLength int
}

// WithProfile judges by the rules profile p keeps.
func WithProfile(p Profile) Option { return func(o *options) { o.profile = p } }

// WithOnly judges by the named rules alone. A named rule that does not apply
// to the kind judged still does not run.
func WithOnly(rules ...Rule) Option { return func(o *options) { o.only = append(o.only, rules...) } }

// WithSkip leaves the named rules out.
func WithSkip(rules ...Rule) Option { return func(o *options) { o.skip = append(o.skip, rules...) } }

// WithThreadLength tells the [KindReply] rules how many replies the author
// already posted in the thread the judged reply joins.
func WithThreadLength(n int) Option { return func(o *options) { o.threadLength = n } }

// input is what a rule reads.
type input struct {
	symbol Symbol
	kind   Kind
	prose  []proseLine
	// lines are the judged text's lines with what is not prose blanked, code
	// still in place, for a rule that asks what a block is rather than what it
	// says.
	lines []string
	// source are the judged text's lines as written, link targets and URLs
	// still in them, for a rule that asks whether a sentence cites something.
	source []string
	opts   options
}

// Judge runs the doc rules over s. They judge nothing when Doc is empty, and
// [RuleNameSuffix] judges only a callable. Findings come in [Rules] order,
// then in the order their text appears in Doc.
func Judge(s Symbol, opts ...Option) []Finding {
	lines := strings.Split(s.Doc, "\n")
	out := run(input{
		symbol: s, kind: KindDoc, prose: readProse(lines, false), lines: lines, source: lines, opts: collect(opts),
	})

	for i := range out {
		out[i].Line = 0
	}

	return out
}

func run(in input) []Finding {
	var out []Finding

	for _, c := range checks {
		if !in.opts.keeps(c.rule) || !slices.Contains(c.on, in.kind) {
			continue
		}

		severity := SeverityError
		if slices.Contains(c.advisory, in.kind) {
			severity = SeverityAdvisory
		}

		// A rule may scan its lines and its paragraphs in separate passes, so
		// its findings are put back in text order. Budgets carry no line and
		// keep their own order.
		found := c.judge(in)
		slices.SortStableFunc(found, func(a, b Finding) int { return a.Line - b.Line })

		for _, f := range found {
			f.Rule = c.rule
			if f.Severity == "" {
				f.Severity = severity
			}

			out = append(out, f)
		}
	}

	return out
}

func collect(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func (o options) keeps(r Rule) bool {
	switch {
	case len(o.only) > 0 && !slices.Contains(o.only, r):
		return false
	case slices.Contains(o.skip, r):
		return false
	default:
		return !slices.Contains(profileSkips[o.profile], r)
	}
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
