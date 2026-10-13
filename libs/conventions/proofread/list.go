package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/libs/conventions/postag"
)

const (
	// RuleListMarks reports a list item whose initial capital or closing full
	// stop differs from the list's other items.
	RuleListMarks Rule = "list-marks"
	// RuleListParallel reports a list item that opens with another part of
	// speech, or another verb tense, than the list's other items.
	RuleListParallel Rule = "list-parallel"
	// RuleDateFormat reports a date written in another style than the text's
	// other dates: 2026-10-13, Oct 2026, October 2026, 10/13/2026.
	RuleDateFormat Rule = "date-format"
)

// listKinds are the Markdown kinds, each of which writes lists and dates.
var listKinds = slices.Concat(withReply, agentReply)

// listChecks judge a text's lists and dates. Each advises until a labeling
// round measures it.
var listChecks = []check{
	{rule: RuleListMarks, on: listKinds, advise: listKinds, judge: listMarks},
	{rule: RuleListParallel, on: listKinds, advise: listKinds, judge: listParallel},
	{rule: RuleDateFormat, on: listKinds, advise: listKinds, judge: dateFormat},
}

const listConvention = "Google's developer style guide (developers.google.com/style/lists) asks for \"the same " +
	"syntax/structure for all list items in a given list\", and Microsoft's (learn.microsoft.com/style-guide/" +
	"scannable-content/lists) for items \"consistent in structure\", in capitalization and end punctuation alike. "

var listTexts = map[Rule]ruleText{
	RuleListMarks: {
		code:      "PRF1060",
		dimension: DimensionConventions,
		catches:   "a list item whose initial capital or closing full stop differs from the list's other items",
		why: listConvention + "A reader scanning a list reads a change of form as a change of meaning. Only " +
			"a list of 3 or more items is judged, and an item that is only code or a link is skipped; so is " +
			"a first word with a capital past its first letter (API, GitHub), which is a name. The items " +
			"outside the most common form are reported, by name. It is a convention, not a study, and no " +
			"labeled firing yet, so it ships off and a decisions table turns it on.",
	},
	RuleListParallel: {
		code:      "PRF1061",
		dimension: DimensionStructure,
		catches:   "a list item that opens with a verb where the others open with a noun phrase, or in another tense",
		why: listConvention + "This repository already holds a guide's numbered steps to an imperative opener " +
			"(step-verb) and a commit subject to the imperative mood (subject-mood); this is the same " +
			"expectation for any list. Each item's first word is tagged in its sentence by the part-of-speech " +
			"tagger, which is accurate on sentences but guesses on fragments (\"Fixed\", \"Cache\", \"Set\"), " +
			"so only a list of 3 or more tagged items is judged, a form must hold two thirds of them, and " +
			"only the items outside it are named. An adjective opens a noun phrase (\"Fast builds\"), so it " +
			"counts as one. Tense is judged per list, so a current role's list may stay in the present while " +
			"the rest are past. Precision is unmeasured, with no labeled firing yet, so it ships off.",
	},
	RuleDateFormat: {
		code:      "PRF5007",
		dimension: DimensionConventions,
		catches: "dates in more than one style in one text: 2026-10-13, Oct 2026, October 2026, 10/13/2026, " +
			"or Sep beside Sept",
		why: "A reader compares dates by their form, and a mix makes each one a fresh parse; 10/13/2026 also " +
			"reads two ways across locales. This repository's first decision record mixes May 2026 and Sept " +
			"2026 with 2026-09-19, where every later record writes ISO 8601 alone. May is both the full and " +
			"the abbreviated name, so it takes no style; a date in code or quotes is not judged. The dates " +
			"outside the text's most common style are reported. It is a convention, not a study, and no " +
			"labeled firing yet, so it ships off and a decisions table turns it on.",
	},
}

// mdList is one list: the items at one indent under one kind of marker, in
// order. Each item is the paragraph its marker opens.
type mdList struct {
	marker string
	items  []paragraph
}

func (l mdList) numbered() bool { return l.marker == "1" }

// lists groups the items of each list in prose, in order, with mask applied
// as [paragraphs] applies it. A list runs until a heading, a paragraph at its
// own indent or less, or an item at its indent with another kind of marker;
// a fenced block between two items, which prose never holds, does not end
// it. A nested list is a list of its own. Every numbered marker is one kind,
// so "1." and "2)" continue one list; a bullet's character is its kind, as
// CommonMark reads it.
func lists(prose []proseLine, mask func(string) string) []mdList {
	var (
		out  []mdList
		open = map[int]int{} // indent -> index in out of the list open there
	)

	closeFrom := func(indent int) {
		for at := range open {
			if at >= indent {
				delete(open, at)
			}
		}
	}

	for _, para := range paragraphs(prose, mask) {
		ln := para.head
		indent := len(ln.text) - len(strings.TrimLeft(ln.text, " \t"))

		switch {
		case ln.heading:
			clear(open)
		case !ln.item:
			closeFrom(indent)
		default:
			closeFrom(indent + 1)

			marker := "1"
			if !isDigit(ln.text[indent]) {
				_, size := utf8.DecodeRuneInString(ln.text[indent:])
				marker = ln.text[indent : indent+size]
			}

			if i, ok := open[indent]; ok && out[i].marker == marker {
				out[i].items = append(out[i].items, para)

				continue
			}

			open[indent] = len(out)
			out = append(out, mdList{marker: marker, items: []paragraph{para}})
		}
	}

	return out
}

func asWritten(s string) string { return s }

var (
	// taskBox opens a task list item: "[ ] " or "[x] ".
	taskBox = regexp.MustCompile(`^\[[ xX]\]\s+`)
	// linkText is a link's text once its target is dropped.
	linkText = regexp.MustCompile(`\[[^\]]*\]`)
	// boldLead is an item's bold label and the colon or full stop that ends it,
	// as a changelog writes "**cache:** keeps the key".
	boldLead = regexp.MustCompile(`^(?:\*\*|__)[^*_]+?(?:[:.](?:\*\*|__)|(?:\*\*|__)[:.])\s*`)
)

// itemBody is an item's text past its task box and any opening emphasis.
func itemBody(text string) string {
	return strings.TrimLeft(taskBox.ReplaceAllString(text, ""), "*_ ")
}

// onlyCodeOrLink reports whether text holds no letter outside its code spans
// and link texts.
func onlyCodeOrLink(text string) bool {
	return !strings.ContainsFunc(linkText.ReplaceAllString(blankBackticks(text), ""), unicode.IsLetter)
}

// minority returns the indexes of classes that differ from the class held by
// at least share of the classed entries, "" being unclassed, and that class.
// It returns nothing when fewer than 3 entries are classed or no class holds
// share; on a tie the class that comes first wins.
func minority(classes []string, share float64) ([]int, string, int) {
	common, n, classed, distinct := mostCommon(classes)
	if classed < 3 || distinct < 2 || float64(n) < share*float64(classed) {
		return nil, "", 0
	}

	var out []int

	for i, c := range classes {
		if c != "" && c != common {
			out = append(out, i)
		}
	}

	return out, common, n
}

// mostCommon returns the class most entries of classes hold, "" being
// unclassed, ties going to the class written first, with its count, the
// number of entries classed and the number of distinct classes.
func mostCommon(classes []string) (common string, n, classed, distinct int) {
	count := map[string]int{}

	for _, c := range classes {
		if c == "" {
			continue
		}

		if count[c] == 0 {
			distinct++
		}

		count[c]++
		classed++
	}

	for _, c := range classes {
		if c != "" && count[c] > count[common] {
			common = c
		}
	}

	return common, count[common], classed, distinct
}

// listItems returns l's items that hold a word outside code and links.
func listItems(l mdList) []paragraph {
	var out []paragraph

	for _, item := range l.items {
		if !onlyCodeOrLink(item.text) {
			out = append(out, item)
		}
	}

	return out
}

// listMarks reports, per list of 3 or more items, each item whose initial
// capital or closing full stop differs from what most of the list's items
// write.
func listMarks(in input) []Finding {
	var out []Finding

	for _, l := range lists(in.prose, asWritten) {
		items := listItems(l)
		if len(items) < 3 {
			continue
		}

		cases := make([]string, len(items))
		stops := make([]string, len(items))

		for i, item := range items {
			cases[i] = initialCase(item.text)
			stops[i] = closingStop(item.text)
		}

		odd, common, n := minority(cases, 0)
		for _, i := range odd {
			word := firstWord(itemBody(items[i].text))
			out = append(out, Finding{
				Message: fmt.Sprintf("Open '%s' with a %s letter, as the list's other %d items do: one list, "+
					"one form.", word, common, n),
				Match: word, Line: items[i].head.line,
			})
		}

		odd, common, n = minority(stops, 0)
		for _, i := range odd {
			text := strings.TrimRight(items[i].text, " \t")
			last := text[strings.LastIndexAny(text, " \t")+1:]

			verb := "End it with a full stop"
			if common == "none" {
				verb = "Drop the full stop"
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("%s after '%s', as the list's other %d items do: one list, one form.",
					verb, last, n),
				Match: last, Line: items[i].lineAt(len(text) - len(last)),
			})
		}
	}

	return out
}

// initialCase is "capital" or "lowercase" for the letter an item opens with,
// or "" when it opens with code, a link, a non-letter or a name: a word with
// a capital past its first letter.
func initialCase(text string) string {
	body := itemBody(text)
	if strings.HasPrefix(body, "`") || strings.HasPrefix(body, "[") {
		return ""
	}

	word := firstWord(body)
	if word == "" {
		return ""
	}

	first, size := utf8.DecodeRuneInString(word)
	if strings.ContainsFunc(word[size:], unicode.IsUpper) {
		return ""
	}

	if unicode.IsUpper(first) {
		return "capital"
	}

	return "lowercase"
}

// closingStop is "stop" for an item that ends a sentence, "none" for one that
// ends on a word or code, and "" for one that ends introducing what follows
// (a colon) or mid-sentence (a comma or semicolon).
func closingStop(text string) string {
	t := strings.TrimRight(text, " \t*_\"')")
	if t == "" {
		return ""
	}

	switch t[len(t)-1] {
	case '.', '!', '?':
		return "stop"
	case ':', ';', ',':
		return ""
	}

	return "none"
}

// opener is the form an item opens with: a coarse word class, and a tense for
// a verb.
type opener struct {
	word, class, tense string
}

// listParallel reports, per list of 3 or more tagged items, each item that
// opens with another word class than two thirds of the items, then each verb
// item in another tense than two thirds of the verb items.
func listParallel(in input) []Finding {
	var out []Finding

	for _, l := range lists(in.prose, asWritten) {
		var (
			items   []paragraph
			openers []opener
		)

		for _, item := range listItems(l) {
			if o, ok := itemOpener(item.text); ok {
				items = append(items, item)
				openers = append(openers, o)
			}
		}

		classes := make([]string, len(openers))
		for i, o := range openers {
			classes[i] = o.class
		}

		odd, common, n := minority(classes, 2.0/3)
		for _, i := range odd {
			out = append(out, parallelFinding(items[i], openers[i], common, n, openers, classes))
		}

		tenses := make([]string, len(openers))

		for i, o := range openers {
			if o.class == "verb" && !slices.Contains(odd, i) {
				tenses[i] = o.tense
			}
		}

		odd, common, n = minority(tenses, 2.0/3)
		for _, i := range odd {
			out = append(out, parallelFinding(items[i], openers[i], common, n, openers, tenses))
		}
	}

	return out
}

// openerNames say what each class and tense opens an item with.
var openerNames = map[string]string{
	"verb": "a verb", "noun": "a noun phrase",
	"base": "an imperative verb", "present": "a present-tense verb", "past": "a past-tense verb",
	"gerund": "an -ing verb",
}

func parallelFinding(item paragraph, o opener, common string, n int, openers []opener, classes []string) Finding {
	example := ""

	for i, c := range classes {
		if c == common {
			example = openers[i].word

			break
		}
	}

	return Finding{
		Message: fmt.Sprintf("Open '%s' with %s, as the list's other %d items do ('%s'): items of one list "+
			"share one shape.", o.word, openerNames[common], n, example),
		Match: o.word, Line: item.head.line,
	}
}

// itemOpener tags the first sentence of an item past any bold label and
// returns the class of its first word. An item opening with code or a link,
// or with a word of no class judged here (an adverb, a preposition, a
// conjunction), has none.
func itemOpener(text string) (opener, bool) {
	body := itemBody(boldLead.ReplaceAllString(taskBox.ReplaceAllString(text, ""), ""))
	if strings.HasPrefix(body, "`") || strings.HasPrefix(body, "[") {
		return opener{}, false
	}

	word := firstWord(body)

	sentences := splitSentences(maskSpans(body))
	if word == "" || len(sentences) == 0 {
		return opener{}, false
	}

	s := tagSentence(sentences[0])

	for i, t := range s.tags {
		if t.UPOS == postag.PUNCT || t.UPOS == postag.SYM {
			continue
		}

		if s.words[i] != strings.ToLower(word) {
			return opener{}, false
		}

		switch t.UPOS {
		case postag.VERB, postag.AUX:
			return opener{word: word, class: "verb", tense: verbTense(t.XPOS)}, true
		case postag.NOUN, postag.PROPN, postag.PRON, postag.DET, postag.NUM, postag.ADJ:
			return opener{word: word, class: "noun"}, true
		}

		return opener{}, false
	}

	return opener{}, false
}

// verbTense groups a Penn Treebank verb tag. An item has no subject, so VBP
// there is the tagger reading an imperative, and VBN a past tense.
func verbTense(xpos string) string {
	switch xpos {
	case "VB", "VBP":
		return "base"
	case "VBZ":
		return "present"
	case "VBD", "VBN":
		return "past"
	case "VBG":
		return "gerund"
	}

	return ""
}
