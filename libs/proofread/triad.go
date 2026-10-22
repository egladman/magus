package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	// RuleTriad reports a series of exactly three that repeats: "X, Y, and Z",
	// three parallel short sentences or three parallel bullets, twice in a text
	// or once in its lead.
	RuleTriad Rule = "triad"
	// RuleStackedColon reports two or more clause-introducing colons in one
	// paragraph.
	RuleStackedColon Rule = "stacked-colon"
	// RuleRestatesDiff reports a bullet or paragraph that only names what was
	// touched and a verb: "Updates foo.go", "Adds X to Y".
	RuleRestatesDiff Rule = "restates-diff"
)

// triadKinds are the texts that describe a change or answer one: a pull request,
// a commit and every review text.
var triadKinds = slices.Concat(reviewed, []Kind{KindCommitMessage})

// triadChecks are the tells of a text no one wrote for the person reading it.
// Each is graded, and ships off until a labeling round measures it.
var triadChecks = []check{
	{rule: RuleTriad, on: triadKinds, graded: true, judge: ownWords(triad)},
	{rule: RuleStackedColon, on: triadKinds, graded: true, judge: ownWords(stackedColon)},
	{rule: RuleRestatesDiff, on: triadKinds, graded: true, judge: ownWords(restatesDiff)},
}

var triadTexts = map[Rule]ruleText{
	RuleTriad: {
		code:      "PRF4030",
		dimension: DimensionEconomy,
		catches: "a series of exactly three (\"X, Y, and Z\", three parallel short sentences, three parallel " +
			"bullets) twice in a text, or once in the lead's first sentence",
		why: "Wikipedia's \"Signs of AI writing\" lists the rule of three among the habits of generated prose, " +
			"and MeasureShape already counts the series per 1000 words as a feature a voice profile can " +
			"compare. One natural series is ordinary writing, so a single one mid-paragraph is left alone; a " +
			"text with two, or a lead that opens on one, reads as a template. Code spans, paths, flags, " +
			"ALL_CAPS names, numbers and a list the author numbered are not judged. No firing has been " +
			"labeled, so it ships off.",
	},
	RuleStackedColon: {
		code:      "PRF4031",
		dimension: DimensionEconomy,
		catches:   "two or more clause-introducing colons in one paragraph",
		why: "A colon that introduces a clause reads as a label on the next idea; a second one in the same " +
			"paragraph turns the prose into a run of labeled claims, a shape generated text takes and a " +
			"person writing to a colleague rarely does. A leading label of three words or fewer (\"Not " +
			"done:\"), a colon inside a code span, a quote or a URL, and a time are not counted. No firing " +
			"has been labeled, so it ships off.",
	},
	RuleRestatesDiff: {
		code:      "PRF4032",
		dimension: DimensionEvidence,
		catches:   "a bullet or paragraph that is a verb and the things touched, with no reason: \"Updates foo.go\"",
		why: "The diff already shows what was touched; a reader opening the description wants why. A sentence " +
			"of a verb such as updates, adds or removes and nothing but code spans, paths and identifiers " +
			"carries nothing the file list lacks. A reason marker (because, so, since, a colon, a purpose " +
			"such as \"to avoid\") or any other content word passes. No firing has been labeled, so it " +
			"ships off.",
	},
}

var (
	allCapsName = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
	dottedName  = regexp.MustCompile(`\w\.\w`)
	camelName   = regexp.MustCompile(`[a-z][A-Z]`)
	// numberedInline marks a list the author numbered: "(1) a, (2) b" or "1) a".
	numberedInline = regexp.MustCompile(`\(\d+\)|\b\d+\)\s`)
	ordinalWord    = regexp.MustCompile(`(?i)\b(?:first|second|third|finally|lastly)\b`)
	seriesSplit    = regexp.MustCompile(`,\s+and\s+|\s+and\s+|,\s+`)
)

// codeLike reports whether word names code rather than saying something: a
// code span (masked to '#'), a path, a flag, an ALL_CAPS name, a dotted or
// camelCase identifier, a number, or a lone capital other than the words "A"
// and "I".
func codeLike(word string) bool {
	w := strings.Trim(word, ".,;:!?\"'`*_")

	switch {
	case w == "":
		return false
	case strings.ContainsAny(w, "#/_~"):
		return true
	case len(w) > 1 && strings.HasPrefix(w, "-"):
		return true
	case allCapsName.MatchString(w), dottedName.MatchString(w), camelName.MatchString(w):
		return true
	case len(w) == 1 && unicode.IsUpper(rune(w[0])):
		return w != "A" && w != "I"
	}

	return strings.IndexFunc(w, func(r rune) bool { return !unicode.IsDigit(r) && r != '.' && r != '-' }) < 0
}

// maskedParagraph is a paragraph as written beside the same text with code
// spans, quotes and URLs blanked to '#', which keeps every offset.
type maskedParagraph struct {
	paragraph
	masked string
}

func maskURLs(s string) string {
	return bareURL.ReplaceAllStringFunc(s, func(m string) string { return strings.Repeat("#", len(m)) })
}

func maskProse(s string) string { return maskURLs(mentionsMasked(s)) }

func maskedParagraphs(in input) []maskedParagraph {
	var out []maskedParagraph

	for _, p := range paragraphs(in.prose, func(s string) string { return s }) {
		out = append(out, maskedParagraph{paragraph: p, masked: maskProse(p.text)})
	}

	return out
}

// triadHit is one series of exactly three, at offset at of its paragraph.
type triadHit struct {
	match string
	line  int
	lead  bool
}

// triad reports each series of exactly three when the text holds two or more,
// or when one stands in the first sentence of its lead.
func triad(in input) []Finding {
	paras := maskedParagraphs(in)

	var (
		hits    []triadHit
		leadEnd = -1
		leadAt  = -1
	)

	for i, p := range paras {
		if p.head.heading || p.head.item || (titled(in.kind) && p.head.line == 1) {
			continue
		}

		leadAt = i

		if spans := sentenceBounds(p.masked); len(spans) > 0 {
			leadEnd = spans[0][1]
		}

		break
	}

	for i, p := range paras {
		if p.head.heading || numberedInline.MatchString(p.masked) {
			continue
		}

		for _, h := range paragraphSeries(p) {
			hits = append(hits, triadHit{match: h.match, line: p.lineAt(h.at), lead: i == leadAt && h.at < leadEnd})
		}
	}

	for _, l := range lists(in.prose, maskProse) {
		if first, ok := parallelBullets(l); ok {
			hits = append(hits, triadHit{match: first.text, line: first.head.line})
		}
	}

	if len(hits) < 2 && (len(hits) == 0 || !hits[0].lead) {
		return nil
	}

	var out []Finding

	for _, h := range hits {
		out = append(out, Finding{
			Message: fmt.Sprintf("Name what is so, however many that is: '%s' is a series of exactly three, "+
				"a count that reads as a template.", h.match),
			Match: h.match, Line: h.line,
		})
	}

	return out
}

type seriesHit struct {
	match string
	at    int
}

// paragraphSeries returns the series of exactly three in one paragraph, as
// "X, Y, and Z" and as three parallel short sentences.
func paragraphSeries(p maskedParagraph) []seriesHit {
	var out []seriesHit

	for _, m := range series.FindAllStringSubmatchIndex(p.masked, -1) {
		from, to := m[2], m[3]
		list := p.masked[from:to]

		bounds := finalAnd.FindAllStringIndex(list, -1)
		if head := strings.TrimRight(list[:bounds[len(bounds)-1][0]], ","); strings.Count(head, ",") != 1 {
			continue
		}

		parts := seriesSplit.Split(list, -1)
		if len(parts) != 3 || partOfLonger(p.masked, from, to) || !plainParts(parts) {
			continue
		}

		out = append(out, seriesHit{match: p.text[from:to], at: from})
	}

	return append(out, parallelSentences(p)...)
}

// partOfLonger reports whether the series at text[from:to] is a piece of a
// longer list or of a path: a comma or a masked span against it, or a slash or
// a dotted name against either edge.
func partOfLonger(text string, from, to int) bool {
	before := strings.TrimRight(text[:from], " \t")
	if before != "" && strings.ContainsRune(",#", rune(before[len(before)-1])) {
		return true
	}

	if from > 0 && strings.ContainsRune("/.~@$-", rune(text[from-1])) {
		return true
	}

	after := strings.TrimLeft(text[to:], " \t")
	if after != "" && strings.ContainsRune(",#", rune(after[0])) {
		return true
	}

	return to+1 < len(text) && (text[to] == '/' || (text[to] == '.' && unicode.IsLetter(rune(text[to+1]))))
}

// plainParts reports whether every item of a series says something: none is
// code, a number or an ordinal the author numbered.
func plainParts(parts []string) bool {
	for _, part := range parts {
		if ordinalWord.MatchString(part) || slices.ContainsFunc(strings.Fields(part), codeLike) {
			return false
		}
	}

	return true
}

const (
	parallelMaxWords = 8
	bulletMaxWords   = 6
	bulletSpread     = 3
)

// parallelSentences finds a run of exactly three consecutive short sentences
// that open on the same word: "It reads the cache. It reads the graph. It
// reads the log."
func parallelSentences(p maskedParagraph) []seriesHit {
	var (
		out    []seriesHit
		run    [][2]int
		opener string
	)

	flush := func() {
		if len(run) == 3 {
			from, to := run[0][0], run[2][1]
			m := p.text[from:to]
			out = append(out, seriesHit{match: strings.TrimSpace(m), at: from + len(m) - len(strings.TrimLeft(m, " \t"))})
		}

		run, opener = nil, ""
	}

	for _, span := range sentenceBounds(p.masked) {
		words := strings.Fields(p.masked[span[0]:span[1]])
		first := ""

		if len(words) > 0 {
			first = lexical(words[0])
		}

		plain := len(words) >= 2 && len(words) <= parallelMaxWords && first != "" &&
			strings.HasSuffix(words[len(words)-1], ".") && !slices.ContainsFunc(words, codeLike)

		switch {
		case !plain:
			flush()
		case first != opener:
			flush()

			run, opener = [][2]int{span}, first
		default:
			run = append(run, span)
		}
	}

	flush()

	return out
}

// parallelBullets reports the first item of a list of exactly three plain
// bullets of about one length. A numbered list is the author's own count.
func parallelBullets(l mdList) (paragraph, bool) {
	if l.numbered() || len(l.items) != 3 {
		return paragraph{}, false
	}

	lo, hi := bulletMaxWords+1, 0

	for _, it := range l.items {
		words := strings.Fields(it.text)
		if len(words) == 0 || len(words) > bulletMaxWords || slices.ContainsFunc(words, codeLike) ||
			strings.Contains(it.text, ":") {
			return paragraph{}, false
		}

		lo, hi = min(lo, len(words)), max(hi, len(words))
	}

	if hi-lo > bulletSpread {
		return paragraph{}, false
	}

	first := l.items[0]
	first.text = strings.TrimSpace(first.text)

	return first, true
}

// stackedColon reports a paragraph with two or more colons that introduce a
// clause. A leading label of three words or fewer is not counted, and neither
// is a colon in a code span, a quote or a URL, nor a time.
func stackedColon(in input) []Finding {
	var out []Finding

	for _, p := range maskedParagraphs(in) {
		if p.head.heading {
			continue
		}

		var colons []int

		for i := 1; i+2 < len(p.masked); i++ {
			prev := rune(p.masked[i-1])
			if p.masked[i] != ':' || (p.masked[i+1] != ' ' && p.masked[i+1] != '\t') ||
				!(unicode.IsLetter(prev) || unicode.IsDigit(prev) || prev == ')') ||
				strings.TrimSpace(p.masked[i+1:]) == "" {
				continue
			}

			colons = append(colons, i)
		}

		if len(colons) > 0 && len(strings.Fields(p.masked[:colons[0]])) <= 3 &&
			!strings.ContainsAny(p.masked[:colons[0]], ".!?") {
			colons = colons[1:]
		}

		if len(colons) < 2 {
			continue
		}

		at := colons[1]
		start := strings.LastIndexAny(p.masked[:at], " \t(") + 1
		m := p.text[start : at+1]

		out = append(out, Finding{
			Message: fmt.Sprintf("Keep one colon in a paragraph: '%s' is the second, and the clauses read as a "+
				"run of labels. Join them with a verb or split the paragraph.", m),
			Match: m, Line: p.lineAt(start),
		})
	}

	return out
}

var (
	// restateVerb opens a sentence that says what was done to a thing.
	restateVerb = regexp.MustCompile(`(?i)^(?:update|add|remove|change|rename|move|fix|delete|refactor|modify|` +
		`create|introduce|replace|edit|touch|tweak|adjust|bump|drop)(?:s|es|d|ed|ing)?\b`)

	// restateFiller are the words that join a verb to the things it touched.
	restateFiller = wordSet("to", "in", "into", "from", "of", "the", "a", "an", "and", "for", "on", "with", "also",
		"file", "files", "function", "functions", "method", "methods", "type", "types", "struct", "test", "tests",
		"field", "fields", "package", "new", "flag", "rule", "const", "constants", "variable", "variables",
		"import", "imports", "code", "docs", "config", "now", "again", "some", "several", "various", "other")
)

const restatesMaxWords = 12

// restatesDiff reports a one-sentence bullet or paragraph that is a verb and
// the code it touched, with no reason beside it.
func restatesDiff(in input) []Finding {
	var out []Finding

	for _, p := range maskedParagraphs(in) {
		if p.head.heading || len(sentenceBounds(p.masked)) != 1 {
			continue
		}

		text := strings.TrimSpace(p.masked)
		if len(strings.Fields(text)) > restatesMaxWords || reasonGiven.MatchString(text) || purpose.MatchString(text) ||
			groundGiven.MatchString(text) {
			continue
		}

		verb := restateVerb.FindStringIndex(text)
		if verb == nil || !onlyThingsTouched(text[verb[1]:]) {
			continue
		}

		start := strings.Index(p.masked, text)
		m := strings.TrimSpace(p.text[start : start+len(text)])

		out = append(out, Finding{
			Message: fmt.Sprintf("Say why, not what: '%s' names the edit, and the diff already shows it. Add what "+
				"the change fixes or allows.", m),
			Match: m, Line: p.lineAt(start),
		})
	}

	return out
}

// onlyThingsTouched reports whether rest holds code and filler words only,
// with at least one piece of code.
func onlyThingsTouched(rest string) bool {
	code := 0

	for _, w := range strings.Fields(rest) {
		trimmed := strings.ToLower(strings.Trim(w, ".,;!?\"'()"))

		switch {
		case trimmed == "" || restateFiller[trimmed]:
		case codeLike(w):
			code++
		default:
			return false
		}
	}

	return code > 0
}
