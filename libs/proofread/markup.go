package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// RuleEmphasis reports bold or italic text in a pull request or a review.
	RuleEmphasis Rule = "emphasis"
	// RuleBareSymbol reports a code name written outside backticks, such as
	// `prepareLoad`, `fmt.Sprintf` or `go_test`.
	RuleBareSymbol Rule = "bare-symbol"
)

// forgeText is the text a forge posts for a reviewer to read.
var forgeText = slices.Concat([]Kind{KindChangeDescription}, reviewText)

var markupChecks = []check{
	{rule: RuleEmphasis, on: forgeText, house: true, judge: emphasis},
	{rule: RuleBareSymbol, on: forgeText, house: true, judge: bareSymbol},
}

var markupTexts = map[Rule]ruleText{
	RuleEmphasis: {
		code:      "PRF5010",
		dimension: DimensionConventions,
		catches:   "a bold or italic span in a pull request description or a review",
		why: "House style: a description is read beside the diff, and emphasis there reads as generated " +
			"text before a reader weighs what it says. Code goes in backticks, which this rule leaves alone.",
	},
	RuleBareSymbol: {
		code:      "PRF5011",
		dimension: DimensionConventions,
		catches:   "a code name outside backticks: a call, a dotted name, a lowerCamel or a snake_case word",
		why: "House style: a name in backticks is one a reader can search for, and prose around it stays " +
			"prose. Product names that look like code (macOS, iPhone, Node.js) are left alone, and so is " +
			"PascalCase, which is how most brands are spelled.",
	},
}

func emphasis(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		body := blankBackticks(ln.body())
		for _, at := range compactBold.FindAllStringIndex(body, -1) {
			if !dunder(body, at) && !openingLabel(body, at) {
				out = append(out, emphasisFinding(body[at[0]:at[1]], ln.line))
			}
		}
		for _, m := range groupMatches(compactItalic, compactBold.ReplaceAllString(body, "")) {
			out = append(out, emphasisFinding(m, ln.line))
		}
	}

	return out
}

// openingLabel reports whether the bold span at opens its line as a label,
// which [RuleReplyVoice] reports instead.
func openingLabel(body string, at []int) bool {
	if before := body[:at[0]]; strings.TrimSpace(before) != "" && listMarker(strings.TrimSpace(before)+" x") != len(strings.TrimSpace(before))+1 {
		return false
	}
	return strings.HasSuffix(body[at[0]:at[1]-2], ":") || strings.HasPrefix(body[at[1]:], ":")
}

func emphasisFinding(m string, line int) Finding {
	return Finding{
		Message: fmt.Sprintf("Drop the emphasis in '%s': say why it matters in words, and put code in backticks.", m),
		Match:   m, Line: line,
	}
}

var (
	// codeNames are a call, a dotted name, a lowerCamel word and a snake_case
	// word, each standing as a whole token.
	codeNames = regexp.MustCompile(`(?:^|[^\w./-])(` +
		`[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*\(\)` +
		`|[A-Za-z_]\w*\.[a-z_]\w*[A-Z]\w*|[A-Za-z_]\w*\.[A-Z]\w*[a-z]\w*` +
		`|[a-z]+[A-Z]\w*` +
		`|[a-z]+_[a-z]\w*` +
		`)(?:[^\w./-]|[.,;:!?](?:\s|$)|$)`)
	// productNames are spelled like code and are not code.
	productNames = []string{"macOS", "iOS", "iPadOS", "iPhone", "iPad", "iCloud", "eBay", "watchOS", "tvOS", "visionOS"}
)

func bareSymbol(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		body := blankBackticks(ln.body())
		body = urls.ReplaceAllStringFunc(body, func(u string) string { return strings.Repeat(" ", len(u)) })
		for _, m := range groupMatches(codeNames, body) {
			if slices.Contains(productNames, m) || strings.HasSuffix(strings.ToLower(m), ".js") {
				continue
			}
			out = append(out, Finding{
				Message: fmt.Sprintf("Put '%s' in backticks: it names code.", m),
				Match:   m, Line: ln.line,
			})
		}
	}

	return out
}
