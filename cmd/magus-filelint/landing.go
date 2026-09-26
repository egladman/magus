package main

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

const (
	landingMarkup = "docs/site/landing.buzz"
	siteStyles    = "docs/src/styles/site.css"
)

var (
	rotatorDelay    = regexp.MustCompile(`\.landing-rotate:nth-child\((\d+)\) \{ animation-delay: -(\d+)s; \}`)
	rotatorDuration = regexp.MustCompile(`animation: landing-rotate (\d+)s`)
)

// landingRotatorSlotsMatchHeadlineCount: the landing page's headlines live in
// landing.buzz and the slot arithmetic in site.css, so nothing but agreement
// between two files keeps them in step, and that agreement broke once: a
// thirteenth line was added while site.css still had delays for twelve.
//
// A span with no delay rule does not lose its animation, it inherits delay 0 and
// rides the FIRST span's timeline, so two headlines fade in stacked on each other
// and the <h1> renders as interleaved glyphs.
func landingRotatorSlotsMatchHeadlineCount(fsys fs.FS) []finding {
	markup, bad := readFile(fsys, landingMarkup)
	if bad != nil {
		return bad
	}
	headlines := strings.Count(markup, `class="landing-rotate"`)
	if headlines == 0 {
		return []finding{{path: landingMarkup, problem: "no rotating headlines found",
			fix: "If the markup changed, update this rule's class anchor."}}
	}
	styles, bad := readFile(fsys, siteStyles)
	if bad != nil {
		return bad
	}

	// The first span needs no rule: its slot is delay 0. So the delays cover 2..N,
	// and the highest selector must be exactly N.
	delay := map[int]int{1: 0}
	line := map[int]int{}
	highest := 1
	for _, m := range rotatorDelay.FindAllStringSubmatchIndex(styles, -1) {
		n, _ := strconv.Atoi(styles[m[2]:m[3]])
		d, _ := strconv.Atoi(styles[m[4]:m[5]])
		delay[n] = d
		line[n] = lineAt(styles, m[0])
		highest = max(highest, n)
	}
	firstRule := lineOf(styles, ".landing-rotate:nth-child(")

	var findings []finding
	var uncovered []int
	for i := 1; i <= headlines; i++ {
		if _, ok := delay[i]; !ok {
			uncovered = append(uncovered, i)
		}
	}
	if len(uncovered) > 0 {
		findings = append(findings, finding{path: siteStyles, line: firstRule,
			problem: fmt.Sprintf("%s has %d headlines but no animation-delay covers span(s) %v", landingMarkup, headlines, uncovered),
			fix:     "Each falls back to delay 0 and renders on top of the first headline; add a rule per span."})
	}
	if highest != headlines {
		findings = append(findings, finding{path: siteStyles, line: line[highest],
			problem: fmt.Sprintf("the highest .landing-rotate:nth-child(%d) does not match %s's %d headlines", highest, landingMarkup, headlines),
			fix:     "Keep one delay rule per headline, 2..N."})
	}

	// The slot length is a design choice, so read it from the CSS (span 2's offset
	// IS one slot). What must hold is the arithmetic around it: every span sits one
	// slot further along, and the cycle is exactly one slot per headline.
	slot := delay[2]
	if slot == 0 {
		return append(findings, finding{path: siteStyles, line: firstRule,
			problem: "span 2 has no animation-delay; cannot derive the slot length"})
	}
	for i := 2; i <= headlines; i++ {
		if d, ok := delay[i]; ok && d != (i-1)*slot {
			findings = append(findings, finding{path: siteStyles, line: line[i],
				problem: fmt.Sprintf("span %d is offset -%ds; one slot is %ds", i, d, slot),
				fix:     fmt.Sprintf("Offset it -%ds.", (i-1)*slot)})
		}
	}
	dur := rotatorDuration.FindStringSubmatchIndex(styles)
	if dur == nil {
		return append(findings, finding{path: siteStyles, problem: "no landing-rotate animation duration"})
	}
	if got := styles[dur[2]:dur[3]]; got != strconv.Itoa(headlines*slot) {
		findings = append(findings, finding{path: siteStyles, line: lineAt(styles, dur[0]),
			problem: fmt.Sprintf("landing-rotate runs %ss for %d headlines at %ds a slot", got, headlines, slot),
			fix:     fmt.Sprintf("Make it %ds.", headlines*slot)})
	}
	return findings
}
