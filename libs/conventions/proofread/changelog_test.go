package proofread

import (
	"slices"
	"testing"
)

// fragment is changes/unreleased/affected-ci-sizes-its-gate.md.
const fragment = `### Added

- **` + "`magus affected ci`" + ` sizes its gate to the change.** Every changed file gets a tier
  (trivial, mechanical, scoped, full) with its evidence. Below full the gate runs only
  the drift check and lint, the targets that declare a changed doc, or ` + "`test`" + ` with
  ` + "`go-test`" + ` narrowed through ` + "`go list`" + `. A trivial change exits 0.
  ` + "`--no-redundancy-check`" + ` runs the full gate.
`

func TestChangelogAcceptsAFragment(t *testing.T) {
	if got := JudgeText(fragment, KindChangelog); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}

	const consoleFragment = "### Changed\n\n- **The advice action links the console instead of posting a Mermaid fence.** " +
		"The\n  blast-radius section links the hosted console, which draws the graph in the browser.\n"

	if got := JudgeText(consoleFragment, KindChangelog); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestChangelogHeadingWantsAVersionAndADate(t *testing.T) {
	bad := func(heading string, line int) Finding {
		return Finding{
			Rule: RuleChangelogHeading,
			Message: "Write a version heading as '## [1.2.0] - 2026-10-10', or '## [Unreleased]' for what has " +
				"not shipped.",
			Match: heading, Line: line,
		}
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a dated release", "## [0.5.0-rc.3] - 2026-10-05\n\n### Added\n\n- **A thing works.**\n", nil},
		{"unreleased", "## [Unreleased]\n\n### Fixed\n\n- **A thing works.**\n", nil},
		{"a linked version", "## [0.4.3](https://github.com/egladman/magus/releases/tag/v0.4.3) - 2026-09-06\n", nil},
		{"a yanked release", "## [0.2.0] - 2026-01-01 [YANKED]\n", nil},
		{"no brackets", "## v0.5.0 - 2026-10-05\n", []Finding{bad("v0.5.0 - 2026-10-05", 1)}},
		{"no date", "## [0.5.0]\n", []Finding{bad("[0.5.0]", 1)}},
		{"a date in parentheses", "## [0.5.0] (2026-10-05)\n", []Finding{bad("[0.5.0] (2026-10-05)", 1)}},
		{"a fragment has no version heading", fragment, nil},
		{"a third-level heading is a group", "### Version 1\n", nil},
		{"the page title", "# Changelog\n\n## [Unreleased]\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindChangelog, WithOnly(RuleChangelogHeading)), tc.want)
		})
	}
}

func TestChangelogGroupNamesTheSixKeepAChangelogGroups(t *testing.T) {
	notGroup := func(kind Kind, heading string, line int, replacements []string) Finding {
		f := Finding{Rule: RuleChangelogGroup, Match: heading, Line: line}

		if kind == KindChangelog {
			f.Message = "Name the group Added, Changed, Deprecated, Removed, Fixed or Security, not '" + heading + "'."

			return f
		}

		f.Message = "Write the group as '" + replacements[0] + "', the Keep a Changelog name, not '" + heading + "'."
		f.Replacements, f.Decision = replacements, DecisionAdvise

		return f
	}

	cases := []struct {
		name, text string
		kind       Kind
		want       []Finding
	}{
		{"every group", "### Added\n\n### Changed\n\n### Deprecated\n\n### Removed\n\n### Fixed\n\n### Security\n", KindChangelog, nil},
		{"a lowercase group", "### fixed\n", KindChangelog, []Finding{notGroup(KindChangelog, "fixed", 1, nil)}},
		{"a synonym", "## [1.0.0] - 2026-01-01\n\n### Bug fixes\n", KindChangelog,
			[]Finding{notGroup(KindChangelog, "Bug fixes", 3, nil)}},
		{"a second-level heading is a release", "## Highlights\n", KindChangelog, nil},
		{"release notes with the groups", "## Added\n\n## Fixed\n", KindReleaseNotes, nil},
		{"release notes with a synonym", "## Bug fixes\n\n- **A thing works.**\n", KindReleaseNotes,
			[]Finding{notGroup(KindReleaseNotes, "Bug fixes", 1, []string{"Fixed"})}},
		{"release notes with a feature heading", "### Features\n", KindReleaseNotes,
			[]Finding{notGroup(KindReleaseNotes, "Features", 1, []string{"Added"})}},
		{"release notes with their own heading", "## Highlights\n\n## Upgrading\n", KindReleaseNotes, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, tc.kind, WithOnly(RuleChangelogGroup)), tc.want)
		})
	}
}

func TestChangelogEntryAdvisesAnEntryNamingOnlyAGoIdentifier(t *testing.T) {
	advised := func(span string, line int) []Finding {
		return []Finding{{
			Rule: RuleChangelogEntry,
			Message: "Say what changed for the user: '" + span + "' names an identifier and the entry says little " +
				"else.",
			Match: span, Line: line, Decision: DecisionAdvise,
		}}
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a bare identifier", "### Changed\n\n- **`Resolve`.**\n", advised("`Resolve`", 3)},
		{"a qualified name", "### Changed\n\n- Renamed `cache.Key` to `cache.ID`.\n", advised("`cache.Key`", 3)},
		{"a call", "### Fixed\n\n- Fixed `Open()`.\n", advised("`Open()`", 3)},
		{
			"an identifier with a user-facing sentence",
			"### Added\n\n- **`AncestryReporter` answers whether one revision reaches another.** All four backends " +
				"implement `IsAncestor`.\n",
			nil,
		},
		{"a flag is something a user can act on", "### Added\n\n- `--stdin`.\n", nil},
		{"a command", "### Added\n\n- **`magus affected ci` sizes its gate.**\n", nil},
		{"a lowercase name", "### Changed\n\n- `resolve`.\n", nil},
		{"prose in a paragraph, not an entry", "### Changed\n\n`Resolve`.\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindChangelog, WithOnly(RuleChangelogEntry)), tc.want)
		})
	}
}

func TestReleaseNotesJudgeEntriesAndGroupsWithoutAVersionHeading(t *testing.T) {
	const notes = "## Fixed\n\n- **Key order is stable.** Two runs of one tree hit each other's cache entries.\n"

	if got := JudgeText(notes, KindReleaseNotes); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}

	got := KindRules(KindReleaseNotes)
	if slices.Contains(got, RuleChangelogHeading) {
		t.Errorf("release notes need no version heading: %v", got)
	}

	for _, r := range []Rule{RuleChangelogGroup, RuleChangelogEntry, RuleFiller, RuleHedge} {
		if !slices.Contains(got, r) {
			t.Errorf("%s does not judge release notes", r)
		}
	}
}

func TestReleaseNotesCarryNoFrontMatter(t *testing.T) {
	got := JudgeText("---\ntitle: Simply a release\n---\n\nIt simply ships.\n", KindReleaseNotes, WithOnly(RuleFiller))
	if len(got) != 1 || got[0].Line != 5 {
		t.Errorf("got %+v, want the body's filler alone: front matter is skipped", got)
	}
}
