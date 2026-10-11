package proofread

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	// RuleSubjectMood reports a commit subject that opens in the past tense, the
	// third person or a gerund rather than the imperative.
	RuleSubjectMood Rule = "subject-mood"
	// RuleSubjectLength reports a commit subject over 100 bytes.
	RuleSubjectLength Rule = "subject-length"
	// RuleSubjectPeriod reports a commit subject that ends in a period.
	RuleSubjectPeriod Rule = "subject-period"
	// RuleBodySeparator reports a commit body that starts on the line after the
	// subject, with no blank line between.
	RuleBodySeparator Rule = "body-separator"
)

// subjectMaxBytes is the longest commit subject, in bytes, that
// [RuleSubjectLength] accepts. It is no option: a decisions table sets the
// rule off, advise or deny.
const subjectMaxBytes = 100

var commitKind = []Kind{KindCommitMessage}

var commitChecks = []check{
	{rule: RuleSubjectMood, on: commitKind, judge: subjectMood},
	{rule: RuleSubjectLength, on: commitKind, judge: subjectLength},
	{rule: RuleSubjectPeriod, on: commitKind, judge: subjectPeriod},
	{rule: RuleBodySeparator, on: commitKind, judge: bodySeparator},
}

var commitTexts = map[Rule]ruleText{
	RuleSubjectMood: {
		code:      "PRF1010",
		dimension: DimensionConventions,
		catches:   "a commit subject opening in the past tense, the third person or a gerund (\"added\", \"fixes\", \"making\")",
		why: "A subject completes \"if applied, this commit will ...\", so it opens with the verb in the " +
			"imperative. The rule reads a closed list of about 30 verbs in their past, third-person and " +
			"gerund forms, not a tagger, so a plural noun that is also a verb (\"changes to the key\") " +
			"is reported; the list is the one hack/policy/commits.buzz already denied. Over the 1729 " +
			"commit messages on main it found nothing, so it denies at no cost.",
	},
	RuleSubjectLength: {
		code:      "PRF1011",
		dimension: DimensionStructure,
		catches:   "a commit subject over 100 bytes",
		why: "A one-line log cuts a long subject off. The cap is 100 bytes, commitlint's header-max-length " +
			"and the limit this repository's commit hook applies, not git's customary 72, since a " +
			"semicolon joining two clauses already runs past 72 on main. A \" (#123)\" a forge appends is " +
			"not counted. Over the 1729 commit messages on main, 485 run past 100, but 2 of the newest " +
			"120 do: the limit arrived with the commit hook. The number is fixed in the rule; a " +
			"decisions table sets the rule off, advise or deny.",
	},
	RuleSubjectPeriod: {
		code:      "PRF1012",
		dimension: DimensionConventions,
		catches:   "a commit subject ending in a period",
		why: "A subject is a title, and a title carries no full stop. A subject ending in \"...\" is left " +
			"alone. None of the 1729 commit messages on main ends in one.",
	},
	RuleBodySeparator: {
		code:      "PRF1013",
		dimension: DimensionStructure,
		catches:   "a commit body that starts on the line after the subject",
		why: "Git, and every tool built on it, takes the first paragraph as the subject; with no blank line " +
			"the body joins it and a one-line log shows both. A message that is a subject and trailers " +
			"(\"Key: value\" lines) is left alone. None of the 1729 commit messages on main runs the body " +
			"into the subject.",
	},
}

// conventionalPrefix is the "type(scope)!: " a conventional commit opens with,
// which is not part of what the subject says.
var conventionalPrefix = regexp.MustCompile(`^[a-z]+(?:\([^)\n]*\))?!?: `)

// exemptSubject reports a subject a tool writes, which the author does not.
func exemptSubject(subject string) bool {
	for _, p := range []string{`Revert "`, "fixup! ", "squash! ", "amend! ", "Merge "} {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}

	return false
}

// subjectLine returns the first line of a commit message, or "" when the message
// is empty or the subject a tool writes.
func subjectLine(in input) string {
	if len(in.source) == 0 || exemptSubject(in.source[0]) {
		return ""
	}

	return strings.TrimRight(in.source[0], " \t\r")
}

// moodForms maps each past, third-person and gerund opener a subject must not
// use to the imperative that replaces it. It is a closed list: a plural noun
// such as "changes to the key" is indistinguishable from the verb without a
// tagger, so only forms measured safe on this repository's history are here.
var moodForms = func() map[string]string {
	forms := map[string][]string{
		"add":       {"added", "adds", "adding"},
		"fix":       {"fixed", "fixes", "fixing"},
		"update":    {"updated", "updates", "updating"},
		"remove":    {"removed", "removes", "removing"},
		"change":    {"changed", "changes", "changing"},
		"implement": {"implemented", "implements", "implementing"},
		"refactor":  {"refactored", "refactors", "refactoring"},
		"improve":   {"improved", "improves", "improving"},
		"create":    {"created", "creates", "creating"},
		"introduce": {"introduced", "introduces", "introducing"},
		"rename":    {"renamed", "renames", "renaming"},
		"move":      {"moved", "moves", "moving"},
		"make":      {"made", "makes", "making"},
		"drop":      {"dropped", "drops", "dropping"},
		"delete":    {"deleted", "deletes", "deleting"},
		"replace":   {"replaced", "replaces", "replacing"},
		"bump":      {"bumped", "bumps", "bumping"},
		"switch":    {"switched", "switching"},
		"stop":      {"stopped", "stopping"},
		"enable":    {"enabled", "enabling"},
		"disable":   {"disabled", "disabling"},
		"allow":     {"allowed", "allowing"},
		"skip":      {"skipped", "skipping"},
		"handle":    {"handled", "handling"},
		"return":    {"returned", "returning"},
		"keep":      {"kept", "keeping"},
		"use":       {"used", "using"},
		"build":     {"built", "building"},
		"write":     {"wrote", "writing"},
		"run":       {"ran", "running"},
	}

	out := map[string]string{}

	for base, fs := range forms {
		for _, f := range fs {
			out[f] = base
		}
	}

	return out
}()

// subjectMood reports the subject's first word when it is a past, third-person
// or gerund form from [moodForms], read after any conventional prefix.
func subjectMood(in input) []Finding {
	subject := subjectLine(in)
	if subject == "" {
		return nil
	}

	rest := conventionalPrefix.ReplaceAllString(subject, "")

	word := rest
	if i := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) }); i >= 0 {
		word = rest[:i]
	}

	base, ok := moodForms[strings.ToLower(word)]
	if !ok {
		return nil
	}

	want := base
	if unicode.IsUpper([]rune(word)[0]) {
		want = strings.ToUpper(base[:1]) + base[1:]
	}

	return []Finding{{
		Message:      fmt.Sprintf("Open the subject with the imperative: '%s', not '%s'.", want, word),
		Match:        word,
		Line:         1,
		Replacements: []string{want},
	}}
}

// pullRequestSuffix is the " (#123)" a forge appends to a squash-merged
// subject, which its author did not write and cannot shorten.
var pullRequestSuffix = regexp.MustCompile(` \(#\d+\)$`)

func subjectLength(in input) []Finding {
	subject := pullRequestSuffix.ReplaceAllString(subjectLine(in), "")
	if len(subject) <= subjectMaxBytes {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Keep the subject to %d bytes (this one has %d): cut it, or move the detail to the "+
			"body.", subjectMaxBytes, len(subject)),
		Line: 1,
	}}
}

func subjectPeriod(in input) []Finding {
	subject := subjectLine(in)
	if !strings.HasSuffix(subject, ".") || strings.HasSuffix(subject, "..") {
		return nil
	}

	fields := strings.Fields(subject)
	last := fields[len(fields)-1]

	return []Finding{{
		Message:      "Drop the period ending the subject: it is a title.",
		Match:        last,
		Line:         1,
		Replacements: []string{strings.TrimSuffix(last, ".")},
	}}
}

// trailerLine is a "Key: value" line git reads as a trailer, or the footer a
// conventional commit marks a breaking change with.
var trailerLine = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9-]*|BREAKING CHANGE): \S`)

func bodySeparator(in input) []Finding {
	if subjectLine(in) == "" || len(in.source) < 2 || strings.TrimSpace(in.source[1]) == "" {
		return nil
	}

	trailers := true

	for _, ln := range in.source[1:] {
		if strings.TrimSpace(ln) != "" && !trailerLine.MatchString(ln) {
			trailers = false
		}
	}

	if trailers {
		return nil
	}

	return []Finding{{
		Message: "Leave a blank line between the subject and the body: git takes the first paragraph as the subject.",
		Line:    2,
	}}
}
