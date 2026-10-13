package proofread

import (
	"fmt"
	"regexp"
	"strings"
)

// dateStyle is a regexp group per style a text writes a date in. ISO 8601
// is one style at day or month precision, as is a month name with or without
// its day. May is both a full and an abbreviated name, so it takes no style.
var dateStyle = regexp.MustCompile(`\b(?:` +
	`(?P<iso>(?:19|20)\d{2}-(?:0[1-9]|1[0-2])(?:-(?:0[1-9]|[12]\d|3[01]))?)|` +
	`(?P<numeric>\d{1,2}/(?:\d{1,2}/)?(?:19|20)\d{2})|` +
	`(?:\d{1,2}\s+)?(?:` +
	`(?P<full>(?i:January|February|March|April|June|July|August|September|October|November|December))|` +
	`(?P<abbr>(?i:Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sept?|Oct|Nov|Dec))\.?|(?i:May))` +
	`(?:\s+\d{1,2},?)?\s+(?:19|20)\d{2}` +
	`)\b`)

// datedAt is one date a text writes, with its style and line. september is
// the abbreviation of a September date, "sep" or "sept", and "" otherwise.
type datedAt struct {
	text, style, september string
	line                   int
}

// dateFormat reports each date written in another style than the text's
// most common one, then each September abbreviation spelled unlike the most
// common one. Code spans and quotes are not read.
func dateFormat(in input) []Finding {
	var dates []datedAt

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, m := range dateStyle.FindAllStringSubmatchIndex(para.text, -1) {
			for g, name := range dateStyle.SubexpNames() {
				if name == "" || m[2*g] < 0 {
					continue
				}

				d := datedAt{text: para.text[m[0]:m[1]], style: name, line: para.lineAt(m[0])}
				if month := strings.ToLower(para.text[m[2*g]:m[2*g+1]]); name == "abbr" && strings.HasPrefix(month, "sep") {
					d.september = month
				}

				dates = append(dates, d)
			}
		}
	}

	styles := make([]string, len(dates))
	septembers := make([]string, len(dates))

	for i, d := range dates {
		styles[i], septembers[i] = d.style, d.september
	}

	return append(
		dateFindings(dates, styles, "as the text's other %d dates are written, as in '%s': one text, one date style."),
		dateFindings(dates, septembers, "with the abbreviation the text's other %d September dates use, as in '%s'.")...,
	)
}

// dateFindings reports each date whose class differs from the most common
// one, ties going to the class written first. Unlike [minority] it judges two
// dates: two styles in one text already disagree.
func dateFindings(dates []datedAt, classes []string, tail string) []Finding {
	common, n, _, distinct := mostCommon(classes)
	if distinct < 2 {
		return nil
	}

	example := ""

	for i, c := range classes {
		if c == common {
			example = dates[i].text

			break
		}
	}

	var out []Finding

	for i, d := range dates {
		if classes[i] == "" || classes[i] == common {
			continue
		}

		out = append(out, Finding{
			Message: fmt.Sprintf("Write '%s' "+tail, d.text, n, example),
			Match:   d.text, Line: d.line,
		})
	}

	return out
}

// dateRange reports whether the dash at text[from:to] joins the two ends of a
// date range, as in "Jan 2020 -- Present" or "2019 -- 2021": a range is no
// aside.
func dateRange(text string, from, to int) bool {
	before := strings.Fields(text[:from])
	after := strings.Fields(text[to:])

	return len(before) > 0 && len(after) > 0 && dateEnd(before[len(before)-1]) && dateEnd(after[0])
}

// rangeWords close a range that is still open.
var rangeWords = wordSet("present", "current", "now", "today", "ongoing")

// monthWords are the month names and their abbreviations.
var monthWords = wordSet("jan", "january", "feb", "february", "mar", "march", "apr", "april", "may", "jun", "june",
	"jul", "july", "aug", "august", "sep", "sept", "september", "oct", "october", "nov", "november", "dec", "december")

// year is a year in a word: 2019, 2019-03, 03/2019.
var year = regexp.MustCompile(`(?:^|\D)(?:19|20)\d{2}(?:\D|$)`)

// dateEnd reports whether word can end a date range: a word holding a year,
// a month, or a word for now. Any other number is not enough: "step 2 --
// now" is an aside.
func dateEnd(word string) bool {
	w := strings.ToLower(strings.Trim(word, ".,;()"))

	return year.MatchString(w) || rangeWords[w] || monthWords[w]
}
