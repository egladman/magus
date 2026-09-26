package main

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

// rotator is a landing page with headlines spans, and a stylesheet whose rules
// are the given lines.
func rotator(headlines int, rules ...string) fstest.MapFS {
	return fstest.MapFS{
		landingMarkup: {Data: []byte(strings.Repeat(`<span class="landing-rotate">x</span>`+"\n", headlines))},
		siteStyles:    {Data: []byte(strings.Join(rules, "\n") + "\n")},
	}
}

func TestLandingRotatorSlotsMatchHeadlineCount(t *testing.T) {
	assert.Empty(t, landingRotatorSlotsMatchHeadlineCount(repoFS(t, landingMarkup, siteStyles)))
	assert.Empty(t, landingRotatorSlotsMatchHeadlineCount(rotator(3,
		".landing-rotate { animation: landing-rotate 21s linear infinite; }",
		".landing-rotate:nth-child(2) { animation-delay: -7s; }",
		".landing-rotate:nth-child(3) { animation-delay: -14s; }")))

	assert.Equal(t, []string{
		"docs/site/landing.buzz has 4 headlines but no animation-delay covers span(s) [4]",
		"the highest .landing-rotate:nth-child(3) does not match docs/site/landing.buzz's 4 headlines",
		"landing-rotate runs 21s for 4 headlines at 7s a slot",
	}, problems(landingRotatorSlotsMatchHeadlineCount(rotator(4,
		".landing-rotate { animation: landing-rotate 21s linear infinite; }",
		".landing-rotate:nth-child(2) { animation-delay: -7s; }",
		".landing-rotate:nth-child(3) { animation-delay: -14s; }"))), "a headline added without its slot")

	got := landingRotatorSlotsMatchHeadlineCount(rotator(3,
		".landing-rotate { animation: landing-rotate 21s linear infinite; }",
		".landing-rotate:nth-child(2) { animation-delay: -7s; }",
		".landing-rotate:nth-child(3) { animation-delay: -12s; }"))
	assert.Equal(t, []finding{{path: siteStyles, line: 3, problem: "span 3 is offset -12s; one slot is 7s", fix: "Offset it -14s."}}, got)

	assert.Equal(t, []string{"no rotating headlines found"}, problems(landingRotatorSlotsMatchHeadlineCount(rotator(0))))
	assert.Equal(t, []string{"span 2 has no animation-delay; cannot derive the slot length"},
		problems(landingRotatorSlotsMatchHeadlineCount(rotator(1, ".landing-rotate { animation: landing-rotate 7s; }"))))
}
