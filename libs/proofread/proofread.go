// Package proofread judges written text: the doc comment and name of one symbol a SCIP
// index describes, a hand-written Markdown file, a skill, a pull request's
// title and description, the comments of a stylesheet, or a message a program
// prints. It parses no programming language, so every indexer's symbols meet
// the same rules; the one reader it carries, for CSS, only finds comments and
// the code beside them.
//
// A doc is read the way go/doc/comment reads one: the common indent comes off,
// a line still indented is preformatted unless it continues a list item, and a
// fenced block is code. Markdown is read the same way once what is not prose
// is blanked (see [JudgeText]). Only the prose that remains is judged.
package proofread

import (
	"fmt"
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
	// RuleFirstPerson reports I, me or my in a reference page or a guide
	// outside an author's note, the one place a page speaks as its author.
	RuleFirstPerson Rule = "first-person"
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
	// KindPrinciple is a design-principle page, one rule argued in its
	// author's voice; it meets the reference rules except tense.
	KindPrinciple Kind = "principle"
	// KindChangeDescription is a pull request or merge request: its title on
	// the first line, its description after it.
	KindChangeDescription Kind = "change-description"
	// KindAgentBrief is hand-written Markdown an agent loads as written, such
	// as AGENTS.md, CLAUDE.md or a skill's SKILL.md: terse but grammatical,
	// held to the terse rules, since every word costs context in every session
	// that loads it.
	KindAgentBrief Kind = "agent-brief"
	// KindAgentCompact is text only an agent reads, written below grammar: one
	// fact per line, articles, linking verbs and emphasis dropped, every word
	// that carries negation, obligation, scope, a condition or an order kept.
	// The rules that hold a sentence to its grammar or its length leave it, and
	// the compact rules hold it to the token floor instead.
	KindAgentCompact Kind = "agent-compact"
	// KindSkillTemplate is a skill written as a text/template body that renders
	// into a short and a full form, as internal/agent renders one. What the
	// short form shows is judged as KindAgentCompact; what only the full form
	// shows, as KindReference, the full form being the copy a person reads.
	KindSkillTemplate Kind = "skill-template"
	// KindGuide is a procedural page, one the reader follows with a
	// terminal open: Markdown held to the guide rules as well, which keep it
	// in the second person, its numbered steps imperative and its words free
	// of condescension.
	KindGuide Kind = "guide"
	// KindReviewSummary is a review's body, the verdict a reviewer posts with
	// an approval, a request for changes or a comment: Markdown with no title
	// line. [WithEvent] says which.
	KindReviewSummary Kind = "review-summary"
	// KindReviewInline is a review comment posted on a line of the diff, which
	// is its referent: Markdown with no title line.
	KindReviewInline Kind = "review-inline"
	// KindReviewReply is a reply in a review thread, or a comment on a pull
	// request's conversation: Markdown with no title line.
	KindReviewReply Kind = "review-reply"
	// KindDiagnostic is one text a program prints to whoever runs it: an error,
	// a guard verdict, a breadcrumb's reason. It is plain text, not Markdown, held
	// to the message rules alone: a verdict, one next command, and a ref for the
	// rationale.
	KindDiagnostic Kind = "diagnostic"
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
	// KindStylesheet is a stylesheet whose comments are judged: each is held to
	// the doc comment rules and to some of the written ones, since a comment is
	// both a doc on the code beside it and prose a maintainer reads cold.
	KindStylesheet Kind = "stylesheet"
)

// compat(until: no caller, decisions table, round record or case file names
// agent-instructions or agent-instructions-template): the names these kinds
// had before kinds were named for their reader.
//
// compat(until: no caller, decisions table, round record or case file names the
// message kind): the name KindDiagnostic had before it was told apart from
// commit-message.
var kindAliases = map[string]Kind{
	"agent-instructions":          KindAgentBrief,
	"agent-instructions-template": KindSkillTemplate,
	"message":                     KindDiagnostic,
}

// ParseKind returns the built-in kind name names. A former name of a kind
// parses to that kind. A name that is none of the built-in kinds returns false
// and no kind.
func ParseKind(name string) (Kind, bool) {
	if k, ok := kindAliases[name]; ok {
		return k, true
	}

	if _, ok := builtins[Kind(name)]; ok {
		return Kind(name), true
	}

	return "", false
}

// Decision is what a finding costs the caller that reads it, in the words a
// magus guard rule's decision uses. Each rule ships a default per kind, and a
// decisions table ([WithDecisions]) overrides it per rule. The zero value is
// unset: a finding a rule left unmarked, which takes the rule's decision.
type Decision int

const (
	// DecisionOff is a rule that does not run.
	DecisionOff Decision = iota + 1
	// DecisionAdvise is a graded finding: it fails nothing alone, and its
	// Weight lowers its text's [Grade].
	DecisionAdvise
	// DecisionDeny is a finding a gate refuses.
	DecisionDeny
)

var decisionNames = [...]string{DecisionOff: "off", DecisionAdvise: "advise", DecisionDeny: "deny"}

// String is d's wire name, or "unset" for the zero value.
func (d Decision) String() string {
	switch {
	case d == 0:
		return "unset"
	case d.valid():
		return decisionNames[d]
	}

	return fmt.Sprintf("Decision(%d)", int(d))
}

// MarshalText writes d's wire name, or empty text when unset. A value outside
// the constants is an error.
func (d Decision) MarshalText() ([]byte, error) {
	switch {
	case d == 0:
		return []byte{}, nil
	case d.valid():
		return []byte(decisionNames[d]), nil
	}

	return nil, fmt.Errorf("proofread decision %d is not off, advise or deny", int(d))
}

// UnmarshalText sets d from its wire name. Empty text is unset; any other
// name than off, advise or deny is an error.
func (d *Decision) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*d = 0

		return nil
	}

	i := slices.Index(decisionNames[:], string(text))
	if i <= 0 {
		return fmt.Errorf("unknown decision %q: want off, advise or deny", text)
	}

	*d = Decision(i)

	return nil
}

func (d Decision) valid() bool { return d >= DecisionOff && d <= DecisionDeny }

// check is one rule: the formats its judge can read, and the judge itself.
// Which kinds it judges, and at what decision, is each [KindSpec]'s to say.
type check struct {
	rule  Rule
	reads []Format
	judge func(in input) []Finding
}

// defaultDecision is what c decides on s when no decisions table names it:
// s's decision, or off for an [unmeasured] rule on a built-in kind.
func (c check) defaultDecision(s *KindSpec) Decision {
	if unmeasured[c.rule] && !s.declared {
		return DecisionOff
	}

	return s.Rules[c.rule]
}

// isHouse reports whether r is house style: off on every built-in kind that
// it judges, so it runs only where a decisions table names it.
func isHouse(r Rule) bool {
	judged := false

	for i := range builtinKinds {
		if d, ok := builtinKinds[i].Rules[r]; ok {
			if d != DecisionOff {
				return false
			}

			judged = true
		}
	}

	return judged
}

// checks run in this order, which is the order [Judge] and [JudgeText] report
// in. A doc keeps the rules it was always judged by: the rules written for
// Markdown and pull requests would hold every doc comment in the tree to
// them at once, with no sweep behind it.
var checks = slices.Concat(coreChecks, cssChecks, toneChecks, slopChecks, markupChecks, messageChecks, commitChecks, helpChecks,
	issueChecks, densityChecks, reviewChecks, reviewKindChecks, suppressChecks, agentReplyChecks, toolChecks, listChecks, voiceChecks,
	compactChecks, skillChecks, []check{templateCheck, gradeCheck}, triadChecks)

var messageChecks = []check{
	{rule: RuleMessageLength, reads: []Format{FormatMessage}, judge: messageLength},
	{rule: RuleMessageRationale, reads: []Format{FormatMessage}, judge: messageRationale},
	{rule: RuleMessageCommands, reads: []Format{FormatMessage}, judge: messageCommands},
	{rule: RuleMessageTag, reads: []Format{FormatMessage}, judge: messageTag},
}

// templateCheck has no judge: a template that does not render is reported
// before any rule runs, and the entry gives the rule its decisions and its
// place in [Rules].
var templateCheck = check{rule: RuleTemplate, reads: []Format{FormatTemplate}}

// Another rule leaves a span to each of these where it reports the span
// itself, so one span gives one finding; see [input.reports].
var (
	fillerCheck = check{
		rule: RuleFiller, reads: []Format{FormatMarkdown, FormatLines, FormatPlain, FormatDocComment, FormatStylesheet}, judge: filler,
	}
	leadContextCheck = check{rule: RuleLeadContext, reads: proseFormats, judge: leadContext}
	tenseCheck       = check{rule: RuleTense, reads: []Format{FormatMarkdown, FormatLines, FormatStylesheet}, judge: tense}
)

var coreChecks = []check{
	{rule: RuleCommentBlock, reads: []Format{FormatDocComment, FormatStylesheet}, judge: commentBlock},
	{rule: RuleCommentSentence, reads: []Format{FormatDocComment, FormatStylesheet}, judge: commentSentence},
	fillerCheck,
	{rule: RuleTerms, reads: []Format{FormatMarkdown, FormatLines, FormatPlain, FormatDocComment, FormatStylesheet}, judge: terms},
	{rule: RuleAside, reads: []Format{FormatPlain, FormatDocComment, FormatStylesheet}, judge: aside},
	{rule: RuleHistory, reads: []Format{FormatDocComment, FormatStylesheet}, judge: history},
	{rule: RuleDocStub, reads: []Format{FormatDocComment}, judge: docStub},
	leadContextCheck,
	{rule: RuleReplyVoice, reads: []Format{FormatMarkdown, FormatLines, FormatStylesheet}, judge: replyVoice},
	tenseCheck,
	{rule: RuleHedge, reads: []Format{FormatMarkdown, FormatLines, FormatStylesheet}, judge: hedge},
	{rule: RuleAttribution, reads: []Format{FormatMarkdown, FormatLines, FormatStylesheet}, judge: attribution},
	{rule: RuleTerseSentence, reads: proseFormats, judge: terseSentence},
	{rule: RuleTerseParagraph, reads: proseFormats, judge: terseParagraph},
	{rule: RuleWordy, reads: proseFormats, judge: wordy},
	{rule: RuleBareRule, reads: proseFormats, judge: bareRule},
	{rule: RuleSecondPerson, reads: proseFormats, judge: secondPerson},
	{rule: RuleStepVerb, reads: proseFormats, judge: stepVerb},
	{rule: RuleFirstPerson, reads: proseFormats, judge: firstPersonSingular},
	{rule: RuleCondescension, reads: []Format{FormatMarkdown, FormatLines, FormatPlain}, judge: condescension},
}

// Rules returns every rule in the order [Judge] and [JudgeText] report them.
func Rules() []Rule {
	out := make([]Rule, len(checks))
	for i, c := range checks {
		out[i] = c.rule
	}

	return out
}

// RuleDecisions checks each name of a decisions table against [Rules]
// and each value against the three decisions.
func RuleDecisions(in map[string]string) (map[Rule]Decision, error) {
	known := Rules()
	out := make(map[Rule]Decision, len(in))

	for _, name := range slices.Sorted(maps.Keys(in)) {
		r := Rule(name)
		if !slices.Contains(known, r) {
			return nil, fmt.Errorf("unknown rule %q", name)
		}

		var d Decision
		if err := d.UnmarshalText([]byte(in[name])); err != nil || d == 0 {
			return nil, fmt.Errorf("rule %q: unknown decision %q: want off, advise or deny", name, in[name])
		}

		out[r] = d
	}

	return out, nil
}

// KindRules returns the rules that judge the built-in kind, in [Rules] order,
// and none for a name that is no built-in kind.
func KindRules(kind Kind) []Rule {
	s, ok := builtins[kind]
	if !ok {
		return nil
	}

	return s.RuleList()
}

// RuleList returns the rules that judge s, in [Rules] order.
func (s *KindSpec) RuleList() []Rule {
	var out []Rule

	for _, c := range checks {
		if s.judges(c.rule) {
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
	// Weight is what the finding costs its text's [Grade]: its rule's measured
	// weight on an advise finding, 0 on a deny finding or an unmeasured rule.
	Weight float64
	// WeightSet marks a Weight the kind's spec set rather than one a
	// labeling round measured.
	WeightSet bool
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
	event        Event
	voice        *Voice
	// localBranches are the names [RuleLocalBranch] reports.
	localBranches []string
	// futureSections are the headings [RuleTense] lets speak of later work.
	futureSections []string
	// minimum is the grade under which a text gets a [RuleGrade] finding, nil
	// for a call that grades nothing.
	minimum *int
	// kinds are the kinds [WithKinds] declared, which [JudgeText] resolves
	// after the built-ins.
	kinds []KindSpec
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

// WithThreadLength tells the review rules how many replies the author
// already posted in the thread the judged text joins.
func WithThreadLength(n int) Option { return func(o *options) { o.threadLength = n } }

// Event is the verdict a review is posted with, in the words GitHub's REST API
// uses for a review's event.
type Event string

const (
	// EventApprove approves the change.
	EventApprove Event = "APPROVE"
	// EventRequestChanges holds the change until what the review names is
	// fixed.
	EventRequestChanges Event = "REQUEST_CHANGES"
	// EventComment comments without a verdict.
	EventComment Event = "COMMENT"
)

// WithEvent tells the [KindReviewSummary] rules the verdict the review is
// posted with. Without it, a rule that needs the verdict judges nothing.
func WithEvent(e Event) Option { return func(o *options) { o.event = e } }

// input is what a rule reads.
type input struct {
	symbol Symbol
	spec   *KindSpec
	prose  []proseLine
	// lines are the judged text's lines with what is not prose blanked, code
	// still in place, for a rule that asks what a block is rather than what it
	// says.
	lines []string
	// source are the judged text's lines as written, link targets and URLs
	// still in them, for a rule that asks whether a sentence cites something.
	source []string
	opts   options
	// text is a [KindDiagnostic]'s whole text and maxRunes its length cap.
	text     string
	maxRunes int
	// css is the comment [judgeStylesheet] is judging, set for
	// [KindStylesheet] alone.
	css *cssComment
}

// Judge runs the doc rules over s. They judge nothing when Doc is empty.
// Findings come in [Rules] order, then in the order their text appears in Doc.
func Judge(s Symbol, opts ...Option) []Finding {
	lines := strings.Split(s.Doc, "\n")
	out := run(input{
		symbol: s, spec: spec(KindDocComment), prose: readProse(lines, false), lines: lines, source: lines, opts: collect(opts),
	})

	for i := range out {
		out[i].Line, out[i].Column, out[i].EndLine, out[i].EndColumn = 0, 0, 0, 0
	}

	return out
}

func run(in input) []Finding {
	var out []Finding

	for _, c := range checks {
		d := in.opts.decide(c, in.spec)
		if d == DecisionOff || c.judge == nil {
			continue
		}

		// A rule may scan its lines and its paragraphs in separate passes, so
		// its findings are put back in text order. Budgets carry no line and
		// keep their own order.
		found := c.judge(in)
		slices.SortStableFunc(found, func(a, b Finding) int { return a.Line - b.Line })

		for _, f := range found {
			out = append(out, in.locate(in.opts.settle(in.spec, c, f, d)))
		}
	}

	out = in.suppress(out)
	if f, ok := in.grade(out); ok {
		out = append(out, f)
	}

	return out
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

// decide returns the decision c's findings on s carry, or [DecisionOff] when
// c does not run there.
func (o options) decide(c check, s *KindSpec) Decision {
	if !s.judges(c.rule) || (len(o.only) > 0 && !slices.Contains(o.only, c.rule)) ||
		(c.rule == RuleVoiceDrift && o.voice == nil) {
		return DecisionOff
	}

	d, ok := o.decisions[c.rule]
	if !ok {
		d = c.defaultDecision(s)
	}

	if d == DecisionDeny && o.voice.relaxes(c.rule, s) {
		return DecisionAdvise
	}

	return d
}

// reports reports whether c runs on the text in holds. A rule that leaves a
// span to c asks first, so the span is still reported where c is off.
func (in input) reports(c check) bool { return in.opts.decide(c, in.spec) != DecisionOff }

// settle gives f the rule, code and decision d of c, judged on s. A decision
// the rule marked on f itself stands only while no table names the rule.
func (o options) settle(s *KindSpec, c check, f Finding, d Decision) Finding {
	if _, named := o.decisions[c.rule]; named || f.Decision == 0 {
		f.Decision = d
	}

	f.Rule = c.rule
	f.Code = ruleTexts[c.rule].code
	f.URL = prf.URL(f.Code)

	f.Weight, f.WeightSet = 0, false
	if f.Decision == DecisionAdvise {
		f.Weight = weights[c.rule]
		if w, set := s.Weights[c.rule]; set {
			f.Weight, f.WeightSet = w, true
		}
	}

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
