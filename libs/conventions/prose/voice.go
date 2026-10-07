package prose

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The rules in this file hold Markdown and pull request text to plain,
// prescriptive technical writing: it describes the code as it stands for a
// reader who never saw the request, the conversation or the tool behind it.

// leadFloor is the fewest words a pull request's lead paragraph may hold. The
// shortest lead among the 60 pull requests before #557 that opened with a
// paragraph held 12, and each of those named a problem and a change.
const leadFloor = 12

var (
	// sentenceBreak ends a sentence, past any closing quote or bracket.
	sentenceBreak = regexp.MustCompile(`[.!?]["')\]]*\s+`)

	// replyOpener opens a sentence that answers a request: it points at the
	// change or at the author where a description names the code.
	replyOpener = regexp.MustCompile(`^(?:This (?:PR|pull request|change|commit|patch)\b|` +
		`Here(?:'s| is| are)\b|I(?:'ve| have)\b|Let me\b)`)

	// conversation refers to an exchange the reader was not part of.
	conversation = regexp.MustCompile(`(?i)\b(?:as (?:requested|discussed)|per your|` +
		`address(?:es|ed|ing)? (?:the |your |all )?(?:review(?:er)? )?(?:feedback|review comments))\b`)

	// youAsked is a conversation only in a pull request. A page addresses its
	// reader as the one who runs magus, so "the run you asked for" names a
	// command there.
	youAsked = regexp.MustCompile(`(?i)\byou asked\b`)

	// boldLabel opens a list item with a label in bold, its colon inside or
	// after the bold: `**Cache:** text` or `**Cache**: text`. A bold headline
	// that ends in a period, as a changelog fragment opens, is a sentence.
	boldLabel = regexp.MustCompile(`^(?:\*\*|__)[^*_]{1,80}?(?::(?:\*\*|__)|(?:\*\*|__)\s*:)`)

	// stockLabel is a section label standing alone on a line.
	stockLabel = regexp.MustCompile(`(?i)^(?:\*\*|__)?(?:summary|changes|test plan|testing|overview|motivation|` +
		`what changed|why)(?:\*\*|__)?:?(?:\*\*|__)?$`)
)

// replyVoice reports text that answers a prompt the reader never saw: a reply
// opener, a reference to a conversation, and a list item that opens with a bold
// label, which turns a sentence into a form to fill in. A pull request also
// may not carry a heading or a stock section label: its description is a lead
// paragraph and bullets. The lead's own opener and heading are lead-context's
// to report.
func replyVoice(in input) []Finding {
	lead := 0
	if in.surface == SurfacePullRequest {
		lead = leadLine(in.lines)
	}

	var out []Finding

	for _, ln := range in.prose {
		switch body := strings.TrimSpace(ln.body()); {
		case ln.heading:
			if in.surface == SurfacePullRequest && ln.line != lead {
				out = append(out, Finding{
					Message: fmt.Sprintf("Drop the heading '%s': a description is a lead paragraph and bullets, "+
						"with no sections.", body),
					Match: body, Line: ln.line,
				})
			}
		case in.surface == SurfacePullRequest && ln.line > 1 && stockLabel.MatchString(body):
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop the section label '%s': say the thing itself.", body),
				Match:   body, Line: ln.line,
			})
		case ln.item:
			if m := boldLabel.FindString(body); m != "" {
				out = append(out, Finding{
					Message: fmt.Sprintf("Drop the bold label '%s': write the item as a sentence "+
						"that opens with its subject.", m),
					Match: m, Line: ln.line,
				})
			}
		}
	}

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, start := range sentenceStarts(para.text) {
			lead := strings.TrimLeft(para.text[start:], "*_(")
			m := replyOpener.FindString(lead)

			line := para.lineAt(start)
			if m == "" || (in.surface == SurfacePullRequest && line == leadLine(in.lines)) {
				continue
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("Open with the subject, not '%s': state what the code does for a reader "+
					"who never saw the request.", m),
				Match: m, Line: line,
			})
		}

		spans := conversation.FindAllStringIndex(para.text, -1)
		if in.surface == SurfacePullRequest {
			spans = append(spans, youAsked.FindAllStringIndex(para.text, -1)...)
			slices.SortFunc(spans, func(a, b []int) int { return a[0] - b[0] })
		}

		for _, at := range spans {
			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s': the reader never saw that conversation; state the reason itself.", m),
				Match:   m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

// sentenceStarts returns the offset of each sentence in text.
func sentenceStarts(text string) []int {
	starts := []int{0}
	for _, m := range sentenceBreak.FindAllStringIndex(text, -1) {
		if m[1] < len(text) {
			starts = append(starts, m[1])
		}
	}

	return starts
}

var (
	// future is a claim in the future tense; tense skips the noun in "at will".
	future = regexp.MustCompile(`\b(?:[Ww]ill|[Ww]on't|\w+'ll)\b`)

	// changeActor is a first-person account of a change: the author as the
	// subject of a change verb, or the change as the author's.
	changeActor = regexp.MustCompile(`\b(?:I|[Ww]e)(?:'ve| have| had)?\s+(?:added|changed|fixed|removed|` +
		`updated|moved|renamed|made|replaced|refactored|introduced|implemented|dropped|rewrote|rewritten|` +
		`switched|split|reworked|bumped|deleted|cleaned|extracted|wired|tightened|reverted|migrated|` +
		`converted|simplified|adjusted|tweaked|landed|shipped)\b|\b[Oo]ur (?:change|fix|patch|refactor|` +
		`rewrite|commit|PR|pull request)\b`)

	// firstPerson is the author in the first person singular.
	firstPerson = regexp.MustCompile(`\b(?:I|I'm|I'd|I'll|I've|me|my|myself)\b`)
)

// tense reports a claim in the future tense and a first-person account of a
// change. Prose describes the code after the change in the present tense, and
// names the code as the actor.
//
// First person is judged per surface. A pull request describes the change, so
// its author has no place in it: every first-person singular is reported, and
// "we" or "our" as the actor of a change. A Markdown page may speak as the
// project ("we believe", "our users"), the voice docs/doctrine.md and the
// decision records argue in, so only "we" or "I" as the actor of a change is
// reported there.
func tense(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, at := range future.FindAllStringIndex(para.text, -1) {
			m := para.text[at[0]:at[1]]
			if strings.EqualFold(m, "will") && strings.HasSuffix(strings.ToLower(para.text[:at[0]]), "at ") {
				continue
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("Write the present tense, not '%s': describe what the code does.", m),
				Match:   m, Line: para.lineAt(at[0]),
			})
		}

		actors := changeActor.FindAllStringIndex(para.text, -1)
		for _, at := range actors {
			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Name the code, not the author, in '%s': say what the change does.", m),
				Match:   m, Line: para.lineAt(at[0]),
			})
		}

		if in.surface != SurfacePullRequest {
			continue
		}

		for _, at := range firstPerson.FindAllStringIndex(para.text, -1) {
			if within(at[0], actors) || (at[1] < len(para.text) && para.text[at[1]] == '/') {
				// An actor is already reported; I/O is no pronoun.
				continue
			}

			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s': a description names the code, not its author.", m),
				Match:   m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

func within(offset int, spans [][]int) bool {
	for _, s := range spans {
		if s[0] <= offset && offset < s[1] {
			return true
		}
	}

	return false
}

// hedging softens a claim. A modal alone is left out: "a workspace may
// declare" grants permission, and "the value may be empty" or "a wait that
// might still end" states a contract a caller relies on. Only a modal before a
// verb of benefit hedges: "might fix" claims a fix without making it.
var hedging = regexp.MustCompile(`\b(?:(?:[Mm]ay|[Mm]ight|[Cc]ould) (?:help|improve|reduce|speed up|fix|solve|` +
	`prevent|avoid|be worth)|[Ss]hould probably|[Pp]robably|[Aa]ims? to|[Tt]ries to|[Hh]opefully|[Aa]rguably|` +
	`[Pp]erhaps|[Ss]eems? to|[Ii]t seems)\b`)

// hedge reports a softener qualifying a claim.
func hedge(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, at := range hedging.FindAllStringIndex(para.text, -1) {
			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s': state the claim, or the condition under which it holds.", m),
				Match:   m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

var (
	// credit names a tool as the producer of the work. "written by an agent"
	// is left out: the product records what agents write, so on a page that
	// is subject matter.
	credit = regexp.MustCompile(`(?i)\bco-authored-by\b|🤖|` +
		`\b(?:generated|written|drafted|authored|produced|created|assisted|co-written) (?:with|by|using|via) ` +
		`(?:the help of )?\[?(?:claude|chatgpt|copilot|gpt|cursor|codex|an? (?:ai assistant|ai|llm|language model))\b`)

	// narrative recounts how the work went, which a reader of the result
	// cannot see and does not need.
	narrative = regexp.MustCompile(`(?i)\b(?:(?:in|during|from|earlier in) (?:this|the previous|the last) ` +
		`(?:conversation|chat)|the (?:original )?prompt (?:asked|said|wanted)|` +
		`(?:after|over|took) (?:several|a few|many|multiple|\d+) iterations)\b`)

	// workNarrative is narrative only in a pull request. On a page "this
	// session" is a Buzz or agent session, "the report above" a section of the
	// page, and an agent finding or fixing something is what the product
	// documents; in a pull request each is the story of how the change was
	// made.
	workNarrative = regexp.MustCompile(`(?i)\b(?:this (?:week's )?session|the report above|as reported|` +
		`branch report|(?:the|an?|my|our) (?:sub)?agent(?:'s)? (?:found|implemented|built|fixed|reported|wrote|` +
		`discovered|verified|measured|investigated|drafted|produced|suggested|proposed))\b`)

	// toolName is a bare model or vendor name. Only a pull request reports it,
	// since there it can only be credit; a path or flag spelling it
	// (`.claude/`, `claude-code`) and the Claude Code harness magus supports
	// are subject matter.
	toolName = regexp.MustCompile(`(?i)(?:^|[^./\w-])(claude|anthropic|copilot|chatgpt|openai)\b`)
)

// attribution reports credit to a tool or an agent and an account of how the
// work was produced. This repository's product is about agents, so "agent",
// "subagent", "prompt" and "session" are its subject matter on every surface:
// the rule fires on them only in a narrative of the work's making, and a page
// documents what agents do where a pull request would narrate it.
func attribution(in input) []Finding {
	var out []Finding

	report := func(m string, line int) {
		out = append(out, Finding{
			Message: fmt.Sprintf("Drop '%s': describe the change, not who or what produced it.", m),
			Match:   m, Line: line,
		})
	}

	patterns := []*regexp.Regexp{credit, narrative}
	if in.surface == SurfacePullRequest {
		patterns = append(patterns, workNarrative)
	}

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		var spans [][]int
		for _, re := range patterns {
			spans = append(spans, re.FindAllStringIndex(para.text, -1)...)
		}

		if in.surface == SurfacePullRequest {
			for _, at := range toolName.FindAllStringSubmatchIndex(para.text, -1) {
				if !strings.HasPrefix(strings.ToLower(para.text[at[2]:]), "claude code") && !within(at[2], spans) {
					spans = append(spans, at[2:4])
				}
			}
		}

		slices.SortFunc(spans, func(a, b []int) int { return a[0] - b[0] })

		for _, at := range spans {
			report(para.text[at[0]:at[1]], para.lineAt(at[0]))
		}
	}

	return out
}

// leadLine returns the line of a pull request's first block of description,
// the first non-blank line after the title, or 0 when there is none.
func leadLine(lines []string) int {
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i + 1
		}
	}

	return 0
}

// leadContext reports a pull request whose description does not open with a
// paragraph that names the goal behind the change and why this code stands
// in its way. A reviewer's first question is what drives the change; a list,
// a heading or a reply opener in that place answers a different one.
func leadContext(in input) []Finding {
	const ask = "open the description with a paragraph naming the goal behind the change and why this code " +
		"stands in its way, then the bullets."

	line := leadLine(in.lines)
	if line == 0 {
		return []Finding{{Message: "No description: " + ask}}
	}

	first := in.lines[line-1]
	body := strings.TrimLeft(first, " \t")

	var problem string

	switch {
	case strings.HasPrefix(body, "```") || strings.HasPrefix(body, "~~~") || len(first)-len(body) >= 4:
		problem = "It opens with code"
	case headingMarker(body) > 0:
		problem = "It opens with a heading"
	case listMarker(body) > 0:
		problem = "It opens with a list"
	}

	if problem != "" {
		return []Finding{{Message: problem + ": " + ask, Match: strings.TrimSpace(first), Line: line}}
	}

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		if para.head.line != line {
			continue
		}

		if m := replyOpener.FindString(strings.TrimLeft(para.text, "*_(")); m != "" {
			return []Finding{{Message: fmt.Sprintf("It opens with '%s': %s", m, ask), Match: m, Line: line}}
		}

		if n := len(strings.Fields(para.text)); n < leadFloor {
			return []Finding{{
				Message: fmt.Sprintf("Its lead holds %d words, too few to carry a reason (at least %d): %s",
					n, leadFloor, ask),
				Line: line,
			}}
		}
	}

	return nil
}
