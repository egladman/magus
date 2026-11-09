package proofread

import (
	"strings"
	"testing"
)

func bullets(items ...string) string { return "- " + strings.Join(items, "\n- ") + "\n" }

func TestRepeatedOpenerShipsOff(t *testing.T) {
	shipEvidence(t)

	text := bullets("Fixed the race.", "Fixed the leak.", "Fixed the exit code.", "Fixed the docs.", "Fixed a typo.")
	if got := JudgeText(text, KindChangelog, WithOnly(RuleRepeatedOpener)); len(got) != 0 {
		t.Errorf("repeated-opener ships on: %v", got)
	}
}

func TestRepeatedOpenerReportsAListThatOpensOnOneWord(t *testing.T) {
	wrote := bullets("Wrote the release script.", "Wrote tests for the parser.", "Cut the build time in half.",
		"Wrote the migration guide.", "Wrote a benchmark.", "Rewrote the config loader.", "Wrote docs for every flag.",
		"Wrote the upgrade notes.", "Built a new parser.", "Wrote the changelog generator.", "Wrote examples.",
		"Wrote the security policy.")

	runTextCases(t, RuleRepeatedOpener, []textCase{
		{"9 of 12", KindChangelog, wrote, []string{"1:Wrote"}},
		{"4 of 5", KindChangelog, bullets("Fixed the race.", "Added a flag.", "Fixed the leak.", "Fixed the exit code.",
			"Fixed the docs."), []string{"1:Fixed"}},
		{"5 of 6 after a bold label", KindReference, bullets("**Fast:** Uses a scheduler.", "**Small:** Uses one binary.",
			"**Portable:** Uses the standard library.", "**Safe:** Uses a sandbox.", "**Simple:** Ships defaults.",
			"**Open:** Uses the MIT license."), []string{"1:Uses"}},
		{"case is ignored", KindChangelog, bullets("now caches results.", "Now prints a summary.", "Now reads env.",
			"Now exits 2.", "Now warns."), []string{"1:now"}},
		{"3 of 5", KindChangelog, bullets("Added a flag.", "Fixed a crash.", "Added YAML config.",
			"Removed the old format.", "Added completions."), nil},
		{"4 of 6", KindChangelog, bullets("Added a flag.", "Added YAML.", "Fixed a crash.", "Added completions.",
			"Removed the old format.", "Added a progress bar."), nil},
		{"4 items are too few", KindChangelog, bullets("Run the build.", "Run the tests.", "Run the linter.",
			"Run the docs."), nil},
		{"articles open noun phrases", KindReference, bullets("The cache lives here.", "The config is read once.",
			"The API is stable.", "The binary is small.", "The tests run offline."), nil},
		{"a numbered list is the author's steps", KindGuide,
			"1. Run `init`.\n2. Run the build.\n3. Run the tests.\n4. Run the linter.\n5. Run the release.\n", nil},
		{"code openers are not words", KindChangelog, bullets("`--quiet` silences.", "`--jobs` sets workers.",
			"`--cache` picks a dir.", "`--offline` skips.", "`--color` forces."), nil},
	})
}
