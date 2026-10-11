package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// RuleSuppressionUnused reports a suppression that gives no reason or that
// matched no finding.
const RuleSuppressionUnused Rule = "suppression-unused"

// everyKind is each kind a text may be judged as, for a rule that reads the
// text's own markers rather than its prose.
var everyKind = []Kind{
	KindDocComment, KindReference, KindChangeDescription, KindAgentInstructions, KindAgentInstructionsTemplate,
	KindGuide, KindReviewReply, KindMessage, KindCommitMessage, KindCLIHelp, KindIssue, KindReleaseNotes,
	KindChangelog,
}

// suppressUnusedCheck has no judge: [input.suppress] reports its findings
// after every other rule has run, since only then is a suppression known to
// have matched nothing.
var suppressUnusedCheck = check{rule: RuleSuppressionUnused, on: everyKind}

var (
	suppressChecks = []check{suppressUnusedCheck}
	suppressTexts  = map[Rule]ruleText{
		RuleSuppressionUnused: {
			code:    "PRF1090",
			catches: "a suppression comment that gives no reason or that matched no finding",
			why: "A suppression is a claim that a finding is wrong here, and a reviewer can only weigh the " +
				"claim when the reason is written beside it. One with no reason suppresses nothing. One that " +
				"matched nothing is left over from text that has since changed, and it would hide the next " +
				"finding that lands on its lines, so it is reported for removal.",
		},
	}
)

const ruleList = `[A-Za-z0-9_-]+(?:\s*,\s*[A-Za-z0-9_-]+)*`

var (
	// suppressOff matches `<!-- proofread off RULES: REASON -->`. Group 1 is the
	// comma-separated rules and group 2 the reason, empty when none is given.
	suppressOff = regexp.MustCompile(`<!--\s*proofread\s+off\s+(` + ruleList + `)\s*(?::\s*(.*?))?\s*-->`)
	// suppressOn matches `<!-- proofread on [RULES] -->`, which ends the block a
	// matching off opened. Group 1 is the rules, empty for all of them.
	suppressOn = regexp.MustCompile(`<!--\s*proofread\s+on(?:\s+(` + ruleList + `))?\s*-->`)
	// suppressIgnore matches `proofread:ignore RULES REASON`. Group 1 is the
	// comma-separated rules and group 2 the reason.
	suppressIgnore = regexp.MustCompile(`proofread:ignore[ \t]+([A-Za-z0-9_-]+(?:,[A-Za-z0-9_-]+)*)(?:[ \t]+(.*\S))?`)
	fenceOpen      = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
)

// directive is one suppression comment in the judged text.
type directive struct {
	line int
	// text is the comment as written, which a finding about it quotes.
	text   string
	reason bool
	rules  []Rule
	spans  []*span
}

// span is the lines of one rule a directive covers, both ends included. An
// off comment's span has no end, to 0, until an on comment closes it.
type span struct {
	rule     Rule
	from, to int
	used     bool
}

// suppress drops the findings an inline suppression in the judged text covers
// and reports the suppressions that did not earn their place.
//
// A suppression needs a reason. In Markdown it is
// "<!-- proofread off RULE[,RULE]: REASON -->", and in a doc comment or plain
// text "proofread:ignore RULE[,RULE] REASON". Either covers its own line and
// the line after it. An off comment followed later by "<!-- proofread on -->",
// or by "<!-- proofread on RULE -->" for some of its rules, instead covers
// every line up to the on comment. A finding with no line, such as a doc
// comment's budget, is covered by a suppression of its rule anywhere in the
// text. A suppression with no reason covers nothing. Backtick spans, double
// quotes and fenced code blocks hold no suppression, so a page or a doc
// comment may show the syntax.
//
// With [RuleSuppressionUnused] on, each suppression with no reason, and each
// with a rule that matched no finding on its lines, is reported once, at its
// own line.
func (in input) suppress(found []Finding) []Finding {
	ds := in.directives()
	if len(ds) == 0 {
		return found
	}

	var kept []Finding

	for _, f := range found {
		covered := false

		for _, d := range ds {
			for _, s := range d.spans {
				if s.rule == f.Rule && (f.Line < 1 || (f.Line >= s.from && f.Line <= s.to)) {
					s.used, covered = true, true
				}
			}
		}

		if !covered {
			kept = append(kept, f)
		}
	}

	decision := in.opts.decide(suppressUnusedCheck, in.kind)
	if decision == DecisionOff {
		return kept
	}

	for _, d := range ds {
		if msg := d.complaint(); msg != "" {
			kept = append(kept, in.locate(in.opts.settle(suppressUnusedCheck,
				Finding{Message: msg, Match: d.text, Line: d.line}, decision)))
		}
	}

	return kept
}

// complaint is what is wrong with d, or "" when it did its work.
func (d *directive) complaint() string {
	if !d.reason {
		return "Add a reason to this suppression: it suppresses nothing without one."
	}

	var idle []string

	for _, s := range d.spans {
		if !s.used {
			idle = append(idle, string(s.rule))
		}
	}

	if len(idle) == 0 {
		return ""
	}

	return fmt.Sprintf("Remove the suppression of %s: it matches no finding.", strings.Join(idle, ", "))
}

// directives reads every suppression in the judged text, in line order, and
// ends the span of each off comment.
func (in input) directives() []*directive {
	var (
		out   []*directive
		open  []*span
		fence string
	)

	for i, raw := range in.source {
		n := i + 1

		if fence != "" {
			if closesFence(raw, fence) {
				fence = ""
			}

			continue
		}

		if m := fenceOpen.FindStringSubmatch(raw); m != nil {
			fence = m[1]

			continue
		}

		masked := mentionsMasked(raw)

		if at := suppressOff.FindStringSubmatchIndex(masked); at != nil {
			d := offDirective(raw, at, n)
			out = append(out, d)
			open = append(open, d.spans...)
		}

		if at := suppressOn.FindStringSubmatchIndex(masked); at != nil {
			open = closeSpans(open, ruleSet(raw, at, 1), n)
		}

		if at := suppressIgnore.FindStringSubmatchIndex(masked); at != nil {
			out = append(out, ignoreDirective(raw, at, n))
		}
	}

	for _, s := range open {
		s.to = s.from + 1
	}

	return out
}

// offDirective builds the directive an off comment at line n is, from the
// submatch indices at of suppressOff in raw.
func offDirective(raw string, at []int, n int) *directive {
	d := &directive{line: n, text: raw[at[0]:at[1]], rules: ruleSet(raw, at, 1)}

	if at[4] >= 0 {
		d.reason = strings.TrimSpace(raw[at[4]:at[5]]) != ""
	}

	d.cover(n, 0)

	return d
}

// ignoreDirective builds the directive a `proofread:ignore` at line n is, from
// the submatch indices at of suppressIgnore in raw.
func ignoreDirective(raw string, at []int, n int) *directive {
	d := &directive{
		line: n, text: strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw[at[0]:at[1]]), "-->")),
		rules: ruleSet(raw, at, 1),
	}

	if at[4] >= 0 {
		d.reason = strings.TrimSpace(strings.TrimSuffix(raw[at[4]:at[5]], "-->")) != ""
	}

	d.cover(n, n+1)

	return d
}

// cover gives d a span per rule from line from to line to, or none when d has
// no reason.
func (d *directive) cover(from, to int) {
	if !d.reason {
		return
	}

	for _, r := range d.rules {
		d.spans = append(d.spans, &span{rule: r, from: from, to: to})
	}
}

// closeSpans ends the open spans of rules at line n, or all of them when
// rules is empty, and returns the spans still open.
func closeSpans(open []*span, rules []Rule, n int) []*span {
	var still []*span

	for _, s := range open {
		if len(rules) == 0 || slices.Contains(rules, s.rule) {
			s.to = n

			continue
		}

		still = append(still, s)
	}

	return still
}

// ruleSet reads the comma-separated rules of submatch group g, whose indices
// are in at, from raw.
func ruleSet(raw string, at []int, g int) []Rule {
	if at[2*g] < 0 {
		return nil
	}

	var out []Rule

	for name := range strings.SplitSeq(raw[at[2*g]:at[2*g+1]], ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, Rule(name))
		}
	}

	return out
}

// closesFence reports whether line closes a fence opened by marker.
func closesFence(line, marker string) bool {
	rest := strings.TrimLeft(strings.TrimLeft(line, " "), marker[:1])
	ticks := len(strings.TrimLeft(line, " ")) - len(rest)

	return ticks >= len(marker) && strings.TrimSpace(rest) == ""
}
