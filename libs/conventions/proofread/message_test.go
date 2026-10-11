package proofread

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestMessageLengthReportsAMessageOverTheCap(t *testing.T) {
	over := func(cap, n int) []Finding {
		return []Finding{{Rule: RuleMessageLength, Line: 1, Message: "Keep a message to " + strconv.Itoa(cap) +
			" runes (this one has " + strconv.Itoa(n) + "): state the verdict and one next command, and move the " +
			"rationale behind a ref."}}
	}

	cases := []struct {
		name string
		text string
		cap  int
		want []Finding
	}{
		{"at the default cap", strings.Repeat("a", 160), 0, nil},
		{"over the default cap", strings.Repeat("a", 161), 0, over(160, 161)},
		{"runes, not bytes", strings.Repeat("é", 20), 20, nil},
		{"over a caller's cap", strings.Repeat("a", 21), 20, over(20, 21)},
		{"surrounding space is free", "  " + strings.Repeat("a", 20) + "\n", 20, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeMessage(tc.text, tc.cap), tc.want)
		})
	}
}

func TestMessageRationaleReportsAStackedReason(t *testing.T) {
	stacked := func(joins int, match string, line int) []Finding {
		return []Finding{{Rule: RuleMessageRationale, Match: match, Line: line, Message: "Give a message one " +
			"reason (this one joins " + strconv.Itoa(joins) + "): keep the verdict and move the rest behind a ref."}}
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"one reason", "the cache dir is magus's own, so write under .magus/tmp", nil},
		{"two reasons", "denied because the cache is shared, so write elsewhere", stacked(2, "so", 1)},
		{"a semicolon and a which", "refused; the row is ended, which no exec revives", stacked(2, ", which", 1)},
		{"on a later line", "refused because x\nretry, meaning y", stacked(2, ", meaning", 2)},
		{"inside a code span", "run `a so b because c` instead", nil},
		{"inside a quoted mention", `the words "so" and "because" are joins`, nil},
		{"a word holding one", "also absorbs the sonic boom", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeMessage(tc.text, 0), tc.want)
		})
	}
}

func TestMessageCommandsReportsASecondCommand(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"one command", "stale graph: run `magus graph build`", nil},
		{"a command and names", "`go.mod` is stale in `libs/x`: run `magus run tidy .`", nil},
		{"two commands", "run `magus graph build`, then `magus refs X`", []Finding{{
			Rule: RuleMessageCommands, Match: "`magus refs X`", Line: 1,
			Message: "Name one next command (this message names 2): keep the one to run first.",
		}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeMessage(tc.text, 0), tc.want)
		})
	}
}

func TestMessageTagReportsAComponentTagOrMarker(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a verdict", "graph is stale: run `magus graph build`", nil},
		{"a leading tag", "server: not running", []Finding{{
			Rule: RuleMessageTag, Match: "server:", Line: 1,
			Message: "Drop the leading 'server:' tag: open with the verdict.",
		}}},
		{"a hyphenated tag", "merge-driver: refreshed", []Finding{{
			Rule: RuleMessageTag, Match: "merge-driver:", Line: 1,
			Message: "Drop the leading 'merge-driver:' tag: open with the verdict.",
		}}},
		{"a capitalized lead is a sentence", "Server: not running", nil},
		{"a two-word lead is no tag", "magus workspace: denied", nil},
		{"a marker", "[AGENT] read the plan", []Finding{{
			Rule: RuleMessageTag, Match: "[AGENT]", Line: 1,
			Message: "Drop the '[AGENT]' marker: a message is plain text for whoever reads it.",
		}}},
		{"a marker in a code span", "run `x [TAG]`", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeMessage(tc.text, 0), tc.want)
		})
	}
}

func TestMessageRulesJudgeOnlyMessages(t *testing.T) {
	want := []Rule{RuleMessageLength, RuleMessageRationale, RuleMessageCommands, RuleMessageTag}
	got := slices.DeleteFunc(KindRules(KindMessage), func(r Rule) bool { return r == RuleSuppressionUnused })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("KindRules(KindMessage) = %q, want %q", got, want)
	}

	text := "server: refused because x, so y"
	for _, f := range JudgeText(text, KindReference) {
		if slices.Contains(want, f.Rule) {
			t.Errorf("Markdown judged by the message rules: %#v", f)
		}
	}

	assertFindings(t, JudgeText(text, KindMessage), JudgeMessage(text, MessageRunes))
}
