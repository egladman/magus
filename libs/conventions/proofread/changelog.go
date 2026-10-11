package proofread

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// RuleChangelogHeading reports a changelog version heading that is not
	// `## [version] - date` or `## [Unreleased]`.
	RuleChangelogHeading Rule = "changelog-heading"
	// RuleChangelogGroup reports a heading that does not name a Keep a
	// Changelog group.
	RuleChangelogGroup Rule = "changelog-group"
	// RuleChangelogEntry reports an entry that names only a Go identifier.
	RuleChangelogEntry Rule = "changelog-entry"
)

var (
	releaseKinds = []Kind{KindReleaseNotes, KindChangelog}
	changelog    = []Kind{KindChangelog}
)

var changelogChecks = []check{
	{rule: RuleChangelogHeading, on: changelog, judge: changelogHeading},
	{rule: RuleChangelogGroup, on: releaseKinds, advise: []Kind{KindReleaseNotes}, judge: changelogGroup},
	{rule: RuleChangelogEntry, on: releaseKinds, advise: releaseKinds, judge: changelogEntry},
}

var changelogTexts = map[Rule]ruleText{
	RuleChangelogHeading: {
		code:    "PRF1030",
		catches: "a changelog version heading that is not \"## [version] - date\" or \"## [Unreleased]\"",
		why: "Keep a Changelog heads each release \"[version] - date\", so a reader and a tool find a release " +
			"by its number and see when it shipped; \"[Unreleased]\" carries no date because nothing has " +
			"shipped. A fragment under changes/unreleased/ has no version heading and is not judged by it.",
	},
	RuleChangelogGroup: {
		code:    "PRF1031",
		catches: "a changelog heading that is not Added, Changed, Deprecated, Removed, Fixed or Security",
		why: "The six groups are the ones Keep a Changelog names, and the ones this repository's fragment " +
			"grammar accepts, so a reader finds a kind of change in the same place in every release. In " +
			"release notes, which are looser, the rule advises and only for a heading that is a plain " +
			"synonym (\"Bug fixes\", \"Features\").",
	},
	RuleChangelogEntry: {
		code:    "PRF1032",
		catches: "a changelog entry that names a Go identifier and says little else",
		why: "An entry says what changed for the person using the software, not the mechanism that changed. " +
			"An entry of fewer than three words around an exported or qualified Go identifier names the " +
			"mechanism and leaves the effect out. A lowercase name in backticks is a flag, a key or a " +
			"command, which a user can act on, so it is left alone. It advises: it found nothing in the " +
			"682 fragments in changes/unreleased/.",
	},
}

// headingLevel is the number of hashes opening an ATX heading line.
func headingLevel(ln proseLine) int {
	t := strings.TrimLeft(ln.text, " \t")

	return len(t) - len(strings.TrimLeft(t, "#"))
}

var versionHeading = regexp.MustCompile(
	`^\[(?:Unreleased|(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)*))\](?: - \d{4}-\d{2}-\d{2})?(?: \[YANKED\])?$`)

func changelogHeading(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		if !ln.heading || headingLevel(ln) != 2 {
			continue
		}

		body := strings.TrimSpace(ln.body())
		m := versionHeading.FindStringSubmatch(body)
		dated := strings.Contains(body, " - ")

		if m == nil || (m[1] != "" && !dated) {
			out = append(out, Finding{
				Message: "Write a version heading as '## [1.2.0] - 2026-10-10', or '## [Unreleased]' for what has " +
					"not shipped.",
				Match: body, Line: ln.line,
			})
		}
	}

	return out
}

var changelogGroups = wordSet("Added", "Changed", "Deprecated", "Removed", "Fixed", "Security")

// groupSynonyms are headings release notes use for a group, and the group each
// names.
var groupSynonyms = map[string]string{
	"new": "Added", "features": "Added", "new features": "Added", "additions": "Added",
	"improvements": "Changed", "changes": "Changed", "breaking changes": "Changed", "updates": "Changed",
	"deprecations": "Deprecated",
	"removals":     "Removed",
	"fixes":        "Fixed", "bug fixes": "Fixed", "bugfixes": "Fixed",
}

func changelogGroup(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		if !ln.heading {
			continue
		}

		body := strings.TrimSpace(ln.body())

		switch level := headingLevel(ln); {
		case in.kind == KindChangelog && level == 3 && !changelogGroups[body]:
			out = append(out, Finding{
				Message: fmt.Sprintf("Name the group Added, Changed, Deprecated, Removed, Fixed or Security, not '%s'.",
					body),
				Match: body, Line: ln.line,
			})
		case in.kind == KindReleaseNotes && level >= 2 && level <= 3 && groupSynonyms[strings.ToLower(body)] != "" &&
			!changelogGroups[body]:
			want := groupSynonyms[strings.ToLower(body)]
			out = append(out, Finding{
				Message:      fmt.Sprintf("Write the group as '%s', the Keep a Changelog name, not '%s'.", want, body),
				Match:        body,
				Line:         ln.line,
				Replacements: []string{want},
			})
		}
	}

	return out
}

var (
	codeSpanText = regexp.MustCompile("`([^`\n]+)`")
	goIdentifier = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*(?:\(\))?$`)
	hasLetter    = regexp.MustCompile(`[A-Za-z]`)
)

// changelogEntry reports a list item that holds a Go identifier in code and
// fewer than three other words. The identifier must be exported or qualified:
// a bare lowercase word in backticks is a flag, a key or a command, which a
// user can act on.
func changelogEntry(in input) []Finding {
	var out []Finding

	masked := paragraphs(in.prose, mentionsMasked)
	plain := paragraphs(in.prose, func(s string) string { return s })

	for i, para := range masked {
		if !para.head.item {
			continue
		}

		words := 0

		for _, f := range strings.Fields(para.text) {
			if hasLetter.MatchString(f) {
				words++
			}
		}

		if words >= 3 {
			continue
		}

		for _, m := range codeSpanText.FindAllStringSubmatch(plain[i].text, -1) {
			id := m[1]
			if goIdentifier.MatchString(id) && (strings.ContainsAny(id, ".") || id != strings.ToLower(id)) {
				out = append(out, Finding{
					Message: fmt.Sprintf("Say what changed for the user: '%s' names an identifier and the entry "+
						"says little else.", m[0]),
					Match: m[0], Line: para.head.line,
				})

				break
			}
		}
	}

	return out
}
