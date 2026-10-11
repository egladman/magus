// Package proofread judges written text: the doc comment and name of one symbol a SCIP
// index describes, a hand-written Markdown file, a skill, a pull request's
// title and description, or a message a program prints. It parses no
// programming language, so every
// indexer's symbols meet the same rules.
//
// A doc is read the way go/doc/comment reads one: the common indent comes off,
// a line still indented is preformatted unless it continues a list item, and a
// fenced block is code. Markdown is read the same way once what is not prose
// is blanked (see [JudgeText]). Only the prose that remains is judged.
package proofread

import (
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/diagnostics"
)

// Rule names one check. It is a [Finding]'s Rule and what a decisions table
// names to set the rule off, advise or deny.
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
	// RuleMessageLength reports a message over its rune cap.
	RuleMessageLength Rule = "message-length"
	// RuleMessageRationale reports a message that stacks reasons: more than
	// one causal join such as " so ", " because ", "; " or ", which ".
	RuleMessageRationale Rule = "message-rationale"
	// RuleMessageCommands reports a message naming more than one backticked
	// command.
	RuleMessageCommands Rule = "message-commands"
	// RuleMessageTag reports a message opening with a component tag such as
	// "server: ", or carrying a bracketed marker such as "[AGENT]".
	RuleMessageTag Rule = "message-tag"
)

// Kind names what a judged text is for, which decides the rules it meets.
type Kind string

const (
	// KindDocComment is a symbol's doc comment.
	KindDocComment Kind = "doc-comment"
	// KindReference is a hand-written Markdown page a reader looks things up
	// in.
	KindReference Kind = "reference"
	// KindChangeDescription is a pull request or merge request: its title on
	// the first line, its description after it.
	KindChangeDescription Kind = "change-description"
	// KindAgentInstructions is Markdown an agent loads as written, such as a
	// skill's SKILL.md: held to the terse rules, since every word costs context
	// in every session that loads it.
	KindAgentInstructions Kind = "agent-instructions"
	// KindAgentInstructionsTemplate is agent instructions written as a
	// text/template body that renders into a short and a full form, as
	// internal/agent renders a skill. What the short form shows is judged as
	// KindAgentInstructions; what only the full form shows, as KindReference.
	KindAgentInstructionsTemplate Kind = "agent-instructions-template"
	// KindGuide is a procedural page, one the reader follows with a
	// terminal open: Markdown held to the guide rules as well, which keep it
	// in the second person, its numbered steps imperative and its words free
	// of condescension.
	KindGuide Kind = "guide"
	// KindReviewReply is a review comment, a review's body or a reply in a
	// review thread: Markdown with no title line.
	KindReviewReply Kind = "review-reply"
	// KindMessage is one message a program prints to whoever runs it: a
	// diagnostic, a guard verdict, a breadcrumb's reason. It is plain text, not
	// Markdown, held to the message rules alone: a verdict, one next command,
	// and a ref for the rationale.
	KindMessage Kind = "message"
	// KindCommitMessage is a commit message: a subject line, then an optional
	// body after a blank line, read in a one-line log long after the change.
	KindCommitMessage Kind = "commit-message"
	// KindCLIHelp is a command's or a flag's help text, plain text a reader
	// scans in a terminal while deciding what to type.
	KindCLIHelp Kind = "cli-help"
	// KindIssue is an issue: a title on the first line, then a Markdown body
	// written to the people who will pick it up.
	KindIssue Kind = "issue"
	// KindReleaseNotes is a release's notes: Markdown a user reads to decide
	// whether to upgrade and what changes for them when they do.
	KindReleaseNotes Kind = "release-notes"
	// KindChangelog is a changelog in the Keep a Changelog shape, or one
	// fragment of it: entries under Added, Changed, Deprecated, Removed, Fixed
	// and Security, each saying what changed for the person using it.
	KindChangelog Kind = "changelog"
	// KindAgentReply is what a coding agent writes back to the person it works
	// for at the end of a turn: Markdown read once, right away, by someone who
	// asked for the work and wants its result.
	KindAgentReply Kind = "agent-reply"
	// KindToolDescription is the description a tool or a skill gives a model
	// choosing among many: an MCP tool's description or a skill's frontmatter
	// description, plain text read to decide whether to call it.
	KindToolDescription Kind = "tool-description"
)

// Decision is what a finding costs the caller that reads it, in the words a
// magus guard rule's decision uses. Each rule ships a default per kind, and a
// decisions table ([WithDecisions]) overrides it per rule.
type Decision string

const (
	// DecisionOff is a rule that does not run.
	DecisionOff Decision = "off"
	// DecisionAdvise is a finding a gate reports and lets through.
	DecisionAdvise Decision = "advise"
	// DecisionDeny is a finding a gate refuses.
	DecisionDeny Decision = "deny"
)

var (
	docOnly = []Kind{KindDocComment}
	written = []Kind{KindReference, KindGuide, KindChangeDescription, KindAgentInstructions}
	all     = []Kind{KindDocComment, KindReference, KindGuide, KindChangeDescription, KindAgentInstructions}
	skill   = []Kind{KindAgentInstructions}
	guide   = []Kind{KindGuide}
	// teammate is text written to the people working on the change, where a
	// sentence about past work or a teammate is about someone the reader knows.
	teammate = []Kind{KindChangeDescription, KindReviewReply}
	message  = []Kind{KindMessage}
	// changeText is what a repository writes about its own changes for the people
	// who follow them: a commit, an issue, release notes and a changelog.
	changeText = []Kind{KindCommitMessage, KindIssue, KindReleaseNotes, KindChangelog}
	// proseKinds are the written kinds and changeText: the kinds the word and claim
	// rules read as another person's words.
	proseKinds = slices.Concat(written, changeText)
	help       = []Kind{KindCLIHelp}
	// toneKinds are the kinds the posture rules judge: text written to the people
	// working on the change. Release notes and a changelog speak to users, who are
	// owed what changed rather than a posture, so only a commit and an issue join.
	toneKinds = slices.Concat(teammate, []Kind{KindCommitMessage, KindIssue})
)

// check is one rule: the kinds it judges, its default decision on each, and
// the judge itself.
type check struct {
	rule Rule
	on   []Kind
	// advise lists the kinds of on where the default is [DecisionAdvise]: the
	// rule's words also have senses there it cannot tell apart from the one it
	// means. On the rest of on the default is [DecisionDeny].
	advise []Kind
	// house marks a rule that encodes one repository's conventions rather than
	// writing a teammate reads. It defaults to [DecisionOff] on every kind and
	// runs only where a decisions table names it.
	house bool
	judge func(in input) []Finding
}

// defaultDecision is what c decides on kind when no decisions table names it.
func (c check) defaultDecision(kind Kind) Decision {
	switch {
	case c.house || !slices.Contains(c.on, kind):
		return DecisionOff
	case slices.Contains(c.advise, kind):
		return DecisionAdvise
	default:
		return DecisionDeny
	}
}

// checks run in this order, which is the order [Judge] and [JudgeText] report
// in. A doc keeps the rules it was always judged by: the rules written for
// Markdown and pull requests would hold every doc comment in the tree to
// them at once, with no sweep behind it.
var checks = slices.Concat(coreChecks, toneChecks, slopChecks, messageChecks, commitChecks, helpChecks,
	issueChecks, densityChecks, reviewChecks, suppressChecks, agentReplyChecks, toolChecks, voiceChecks, []check{templateCheck})

var messageChecks = []check{
	{rule: RuleMessageLength, on: message, judge: messageLength},
	{rule: RuleMessageRationale, on: message, judge: messageRationale},
	{rule: RuleMessageCommands, on: message, judge: messageCommands},
	{rule: RuleMessageTag, on: message, judge: messageTag},
}

// templateCheck has no judge: a template that does not render is reported
// before any rule runs, and the entry gives the rule its decisions and its
// place in [Rules].
var templateCheck = check{rule: RuleTemplate, on: []Kind{KindAgentInstructionsTemplate}, house: true}

// Another rule leaves a span to each of these where it reports the span
// itself, so one span gives one finding; see [input.reports].
var (
	fillerCheck      = check{rule: RuleFiller, on: everywhere, advise: agentReply, judge: filler}
	leadContextCheck = check{rule: RuleLeadContext, on: []Kind{KindChangeDescription}, judge: leadContext}
	tenseCheck       = check{rule: RuleTense, on: proseKinds, house: true, judge: tense}
)

var coreChecks = []check{
	{rule: RuleCommentBlock, on: docOnly, house: true, judge: commentBlock},
	{rule: RuleCommentSentence, on: docOnly, house: true, judge: commentSentence},
	fillerCheck,
	{rule: RuleTerms, on: everywhere, house: true, judge: terms},
	{rule: RuleNameSuffix, on: docOnly, house: true, judge: nameSuffix},
	{rule: RuleAside, on: slices.Concat(docOnly, help), house: true, judge: aside},
	{rule: RuleHistory, on: docOnly, house: true, judge: history},
	{rule: RuleDocStub, on: docOnly, house: true, judge: docStub},
	leadContextCheck,
	{rule: RuleReplyVoice, on: withReply, judge: replyVoice},
	tenseCheck,
	{rule: RuleHedge, on: slices.Concat(proseKinds, agentReply), advise: agentReply, judge: hedge},
	{rule: RuleAttribution, on: withReply, house: true, judge: attribution},
	{rule: RuleTerseSentence, on: skill, house: true, judge: terseSentence},
	{rule: RuleTerseParagraph, on: skill, house: true, judge: terseParagraph},
	{rule: RuleWordy, on: slices.Concat(proseKinds, agentReply), advise: agentReply, judge: wordy},
	{rule: RuleBareRule, on: skill, house: true, judge: bareRule},
	{rule: RuleSecondPerson, on: guide, judge: secondPerson},
	{rule: RuleStepVerb, on: guide, judge: stepVerb},
	{rule: RuleCondescension, on: slices.Concat(withReply, help, agentReply), judge: condescension},
}

// Rules returns every rule in the order [Judge] and [JudgeText] report them.
func Rules() []Rule {
	out := make([]Rule, len(checks))
	for i, c := range checks {
		out[i] = c.rule
	}

	return out
}

// KindRules returns the rules that judge kind, in [Rules] order.
func KindRules(kind Kind) []Rule {
	var out []Rule

	for _, c := range checks {
		if slices.Contains(c.on, kind) {
			out = append(out, c.rule)
		}
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
	// Decision is the rule's default for the kind judged, unless a decisions
	// table names the rule, or the rule marked the finding [DecisionAdvise]
	// itself and no table names it: a rule whose words are sure in one reading
	// and a guess in another reports the guess that way.
	Decision Decision
	// Code is the rule's PRF code, and URL the page that documents it.
	Code diagnostics.Code
	URL  string
	// Column is the 1-based byte column Match starts at on Line, and EndLine
	// and EndColumn the position just past its last byte, all 0 when Match is
	// "" or could not be found on Line as written.
	Column, EndLine, EndColumn int
	// Replacements are the texts Match may be replaced with, best first. Only
	// a rule that substitutes a word or a phrase offers one: a rewrite of a
	// tone or claim finding would change what the writer meant.
	Replacements []string
}

// Option tunes one [Judge] or [JudgeText] call.
type Option func(*options)

type options struct {
	only         []Rule
	decisions    map[Rule]Decision
	threadLength int
	voice        *Voice
}

// WithOnly judges by the named rules alone. A named rule that does not apply
// to the kind judged, or that its decision sets off, still does not run.
func WithOnly(rules ...Rule) Option { return func(o *options) { o.only = append(o.only, rules...) } }

// WithDecisions sets each named rule's decision on every kind it judges, in
// place of its default: [DecisionOff] leaves it out, and [DecisionAdvise] or
// [DecisionDeny] runs it, house style included, with every finding carrying
// that decision. A rule the table does not name keeps its default. A name
// that is no rule matches nothing, so a caller reading a table from a user
// checks the names against [Rules] first. Later calls add to the table. A
// voice ([WithVoice]) may still turn a deny of tense into advice.
func WithDecisions(table map[Rule]Decision) Option {
	return func(o *options) {
		if o.decisions == nil {
			o.decisions = map[Rule]Decision{}
		}

		maps.Copy(o.decisions, table)
	}
}

// WithThreadLength tells the [KindReviewReply] rules how many replies the
// author already posted in the thread the judged reply joins.
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
	// text is a [KindMessage]'s whole text and maxRunes its length cap.
	text     string
	maxRunes int
}

// Judge runs the doc rules over s. They judge nothing when Doc is empty, and
// [RuleNameSuffix] judges only a callable. Findings come in [Rules] order,
// then in the order their text appears in Doc.
func Judge(s Symbol, opts ...Option) []Finding {
	lines := strings.Split(s.Doc, "\n")
	out := run(input{
		symbol: s, kind: KindDocComment, prose: readProse(lines, false), lines: lines, source: lines, opts: collect(opts),
	})

	for i := range out {
		out[i].Line, out[i].Column, out[i].EndLine, out[i].EndColumn = 0, 0, 0, 0
	}

	return out
}

func run(in input) []Finding {
	var out []Finding

	for _, c := range checks {
		d := in.opts.decide(c, in.kind)
		if d == DecisionOff || c.judge == nil {
			continue
		}

		// A rule may scan its lines and its paragraphs in separate passes, so
		// its findings are put back in text order. Budgets carry no line and
		// keep their own order.
		found := c.judge(in)
		slices.SortStableFunc(found, func(a, b Finding) int { return a.Line - b.Line })

		for _, f := range found {
			out = append(out, in.locate(in.opts.settle(c, f, d)))
		}
	}

	return in.suppress(out)
}

// locate gives f the columns its Match spans on its line as written. A Match
// that occurs twice on its line is placed at the first occurrence.
func (in input) locate(f Finding) Finding {
	if f.Match == "" || f.Line < 1 || f.Line > len(in.source) {
		return f
	}

	at := strings.Index(in.source[f.Line-1], f.Match)
	if at < 0 {
		return f
	}

	f.Column, f.EndLine, f.EndColumn = at+1, f.Line, at+1+len(f.Match)

	return f
}

// decide returns the decision c's findings on kind carry, or [DecisionOff]
// when c does not run there.
func (o options) decide(c check, kind Kind) Decision {
	if !slices.Contains(c.on, kind) || (len(o.only) > 0 && !slices.Contains(o.only, c.rule)) ||
		(c.rule == RuleVoiceDrift && o.voice == nil) {
		return DecisionOff
	}

	d, ok := o.decisions[c.rule]
	if !ok {
		d = c.defaultDecision(kind)
	}

	if d == DecisionDeny && o.voice.relaxes(c.rule, kind) {
		return DecisionAdvise
	}

	return d
}

// reports reports whether c runs on the text in holds. A rule that leaves a
// span to c asks first, so the span is still reported where c is off.
func (in input) reports(c check) bool { return in.opts.decide(c, in.kind) != DecisionOff }

// settle gives f the rule, code and decision d of c. A decision the rule
// marked on f itself stands only while no table names the rule.
func (o options) settle(c check, f Finding, d Decision) Finding {
	if _, named := o.decisions[c.rule]; named || f.Decision == "" {
		f.Decision = d
	}

	f.Rule = c.rule
	f.Code = ruleTexts[c.rule].code
	f.URL = prf.URL(f.Code)

	return f
}

func collect(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	return o
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
