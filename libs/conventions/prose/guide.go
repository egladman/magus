package prose

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// The rules in this file hold a guide, a page the reader follows with a
// terminal open, to the voice of a procedure: it speaks to the reader as you,
// each numbered step opens with the action, and no word tells the reader how
// hard the step ought to feel.

// projectVoice is the first person plural. A concept page may argue as the
// project ("we believe"); a guide speaks to the one following it. "US" in
// capitals is a country or a locale, not the pronoun.
var projectVoice = regexp.MustCompile(`\b(?:[Ww]e|[Uu]s|[Oo]urs?|[Ll]et's)\b`)

// secondPerson reports the project's voice in a guide. A "we" that tense
// already reports, as the actor of a change or in "we'll", is left to it.
func secondPerson(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		taken := append(future.FindAllStringIndex(para.text, -1), changeActor.FindAllStringIndex(para.text, -1)...)

		for _, at := range projectVoice.FindAllStringIndex(para.text, -1) {
			if within(at[0], taken) {
				continue
			}

			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Address the reader as you, not '%s': a guide speaks to the person "+
					"following it, and names magus or the project where it means them.", m),
				Match: m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

// imperatives are verbs a step opens with, mined from the steps the guides
// write. The list only decides which numbered lists are procedures, so a verb
// missing from it never fails a step; it can only leave a list unjudged.
var imperatives = wordSet(
	"accept", "add", "allow", "append", "apply", "approve", "ask", "attach", "bind", "build", "call",
	"change", "check", "choose", "clear", "clone", "close", "commit", "compare", "configure", "confirm",
	"connect", "copy", "create", "declare", "define", "delete", "deploy", "describe", "diff", "disable",
	"download", "drop", "edit", "enable", "export", "extract", "fetch", "find", "fix", "follow",
	"generate", "give", "grant", "import", "include", "inspect", "install", "keep", "launch", "link",
	"load", "make", "mark", "merge", "move", "open", "pass", "paste", "pick", "pin", "pipe", "point",
	"publish", "pull", "push", "put", "read", "rebuild", "record", "regenerate", "register", "reload",
	"remove", "rename", "replace", "rerun", "restart", "restore", "retry", "revert", "review", "rewrite",
	"rotate", "run", "save", "select", "set", "sign", "skip", "start", "stop", "switch", "tag", "take",
	"tell", "trust", "try", "uninstall", "unpack", "unset", "update", "upgrade", "upload", "use",
	"verify", "visit", "wait", "watch", "wire", "write",
)

// stepOpeners are the words that open a sentence about something rather than
// an action: the articles and the pronouns.
var stepOpeners = wordSet(
	"a", "an", "the",
	"i", "we", "he", "she", "it", "they", "this", "that", "these", "those",
	"my", "our", "your", "its", "their",
)

// stepVerb reports a step of a procedure that does not open with its action.
// A numbered list is a procedure when one of its items opens with an
// imperative; the rest (a recap of what a page did, a precedence order, a
// list of reasons) are ordered facts and keep the general rules. In a
// procedure, a step that opens with an article, a pronoun or "You" is
// refused, and so is one that opens with a code span or a link: that is the
// object of a verb the step leaves out.
func stepVerb(in input) []Finding {
	var out []Finding

	for _, list := range numberedLists(in.prose) {
		procedure := false

		for _, item := range list {
			if imperatives[strings.ToLower(firstWord(stepBody(item)))] {
				procedure = true

				break
			}
		}

		if !procedure {
			continue
		}

		for _, item := range list {
			if f, ok := stepFinding(stepBody(item)); ok {
				f.Line = item.line
				out = append(out, f)
			}
		}
	}

	return out
}

func stepFinding(body string) (Finding, bool) {
	const verbs = "(Run, Add, Open, Set)"

	switch {
	case strings.HasPrefix(body, "`"), strings.HasPrefix(body, "["):
		end := strings.IndexAny(body[1:], "`]") + 2
		if end < 2 {
			end = len(body)
		}

		m := body[:end]

		return Finding{
			Message: fmt.Sprintf("Put the verb before '%s': a step opens with the action it is the object of, "+
				"as in Run `magus init`.", m),
			Match: m,
		}, true
	}

	word := firstWord(body)

	switch lower := strings.ToLower(word); {
	case lower == "you":
		return Finding{
			Message: fmt.Sprintf("Drop '%s' and open the step with its verb %s: a step is an instruction, "+
				"not a description of the reader.", word, verbs),
			Match: word,
		}, true
	case stepOpeners[lower]:
		return Finding{
			Message: fmt.Sprintf("Open the step with the action, not '%s': start with the imperative verb "+
				"the reader carries out %s.", word, verbs),
			Match: word,
		}, true
	}

	return Finding{}, false
}

// stepBody is an item's text past its marker and any opening emphasis, with
// quotes masked so a quoted opener reads as the mention it is.
func stepBody(ln proseLine) string {
	return strings.TrimLeft(blankQuoted(ln.body()), "*_ ")
}

// firstWord returns the letters and apostrophes body opens with.
func firstWord(body string) string {
	end := strings.IndexFunc(body, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' })
	if end < 0 {
		return body
	}

	return body[:end]
}

// numberedLists groups the items of each numbered list in prose, in order. A
// list runs until a heading, a paragraph at its own indent or less, or a
// bulleted item at its indent; a fenced block between two steps, which
// prose never holds, does not end it.
func numberedLists(prose []proseLine) [][]proseLine {
	var (
		out  [][]proseLine
		open = map[int]int{} // indent -> index in out of the list open there
	)

	closeFrom := func(indent int) {
		for at := range open {
			if at >= indent {
				delete(open, at)
			}
		}
	}

	for _, ln := range prose {
		indent := len(ln.text) - len(strings.TrimLeft(ln.text, " \t"))

		switch {
		case ln.heading:
			clear(open)
		case !ln.item:
			if ln.opens {
				closeFrom(indent)
			}
		case !isDigit(ln.text[indent]):
			closeFrom(indent)
		default:
			closeFrom(indent + 1)

			if i, ok := open[indent]; ok {
				out[i] = append(out[i], ln)

				continue
			}

			open[indent] = len(out)
			out = append(out, []proseLine{ln})
		}
	}

	return out
}

// condescending tells the reader how hard a step should feel, which reads as
// their failure when it is not. Each matches in either case.
var condescending = regexp.MustCompile(`(?i)\b(?:easy|easily|simple|simply|obviously|of course|clearly|` +
	`just|please)\b`)

// condescension reports a word that tells the reader a step is easy. A word
// filler already reports (`simply`, `just` meaning merely, `Please note`) is
// left to it, so one word gives one finding.
func condescension(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		taken := fillerSpans(para.text, writtenFillerPattern)

		for _, at := range condescending.FindAllStringIndex(para.text, -1) {
			if within(at[0], taken) || condescensionExempt(para.text, at) {
				continue
			}

			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s': it tells the reader how hard the step should feel; "+
					"state the step.", m),
				Match: m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

var (
	// negations before "easy" or "easily" turn a promise into a warning:
	// "cannot easily audit". The "t" is what prevWord leaves of "isn't".
	negations = wordSet("not", "cannot", "never", "no", "hardly", "t")

	// easyMistake follows an "easy" that warns rather than reassures: "an order
	// that is easy to get wrong".
	easyMistake = regexp.MustCompile(`^ to (?:get (?:\w+ )?wrong|miss|forget|overlook|misread|mistake|` +
		`confuse|believe|break|lose|trip)\b`)
)

// condescensionExempt reports a word from condescending used in a sense that
// carries meaning. Measured over the guides before this rule: none of 16
// lowercase "just" minimized a step the reader takes; nearly all meant only
// ("extracts just the binary"), recency ("you just downloaded") or contrast
// ("not just the hunks"). So "just" counts only before a verb the reader
// carries out ("just run"). Each of the three "easy" and "easily" warned of a
// mistake or denied the ease.
func condescensionExempt(text string, at []int) bool {
	switch strings.ToLower(text[at[0]:at[1]]) {
	case "just":
		return !imperatives[strings.ToLower(firstWord(strings.TrimLeft(text[at[1]:], " ")))]
	case "easy", "easily":
		return negations[prevWord(text, at[0])] || easyMistake.MatchString(text[at[1]:])
	}

	return false
}
