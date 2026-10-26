package proofread

import (
	"cmp"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
)

// The fixture records the 1765 commits at dfbd832c6ef9b719f986f949bcf96f2d24715629:
// git log <revision> --format='-- %H --%n%B'. Keeping the measured inputs here makes
// this library's tests independent of a caller's checkout and available Git history.
func TestCommitRulesAcceptMeasuredHistory(t *testing.T) {
	data, err := os.ReadFile("testdata/commit-history.txtar")
	if err != nil {
		t.Fatal(err)
	}
	messages := txtar.Parse(data).Files
	if len(messages) != 1765 {
		t.Fatalf("history fixture has %d messages, want 1765", len(messages))
	}
	counts := map[commitShape]int{}
	for _, file := range messages {
		text := string(file.Data)
		subject, _, _ := strings.Cut(text, "\n")
		for _, f := range JudgeText(text, KindCommitMessage, denyCommit(RuleSubjectPunct)...) {
			if f.Match == "(" && !strings.Contains(pullRequestSuffix.ReplaceAllString(subject, ""), "(") {
				t.Fatalf("%s: forge trailer reported as punctuation: %s", file.Name, subject)
			}
		}
		for _, f := range JudgeText(text, KindCommitMessage, denyCommit(RuleNoBody)...) {
			if strings.HasPrefix(f.Match, "* ") {
				t.Fatalf("%s: squash list reported as prose: %s", file.Name, f.Match)
			}
		}
		for _, shape := range commitShapes(text) {
			counts[shape]++
		}
	}
	named := map[commitShape]string{
		{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer}: "the forge's trailing PR number",
		{Rule: RuleNoBody, Shape: shapeSquashList}:         "the forge's list of squashed commits",
	}
	for _, shape := range shapesClearing(counts) {
		if named[shape.commitShape] == "" {
			t.Errorf("%s %s holds %d of %d hits (%.2f); name the accepted shape or change the rule",
				shape.Rule, shape.Shape, shape.Count, shape.Hits, shape.Share)
		}
	}
}

// A commit shape is where a hit sits, so a forge trailer and a parenthesis in
// the subject are not one finding. The rules still report the marks. The
// shapes are what the measured history counts.
const (
	shapeTitleCase     = "title-case"
	shapeForgeTrailer  = "forge-trailer"
	shapeInteriorParen = "interior-paren"
	shapeComma         = "comma"
	shapeBang          = "bang"
	shapeQuestion      = "question"
	shapeSpacedHyphen  = "spaced-hyphen"
	shapeSquashList    = "squash-list"
	shapeProse         = "prose"
)

// shapeMinCount and shapeMinShare are the word-class dictionary's gate
// (dictMinCount and dictMinShare): 20 hits, and 97% of that rule's hits.
// Below either number the shape is residue and the rule stays as it is.
const (
	shapeMinCount = 20
	shapeMinShare = 0.97
)

// commitShape is one hit of a commit rule, named by the rule and where the
// hit sits. The forge trailer and the squash list are shapes even when the
// rule leaves them unreported.
type commitShape struct {
	Rule  Rule
	Shape string
}

// shapeTally is one shape's count against every hit of its rule.
type shapeTally struct {
	commitShape
	Count int
	Hits  int
	Share float64
}

// commitShapes reads text as a commit message and returns each hit by where
// it sits. A conventional prefix is not a hit. A trailing " (#123)" is a
// forge trailer; a parenthesis that remains is interior. A body of only "* "
// lines is a squash list, and one prose line makes the body prose.
func commitShapes(text string) []commitShape {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	subject := subjectLine(input{source: raw})
	if subject == "" {
		return nil
	}

	var out []commitShape

	if titleCaseWord.MatchString(subject) {
		out = append(out, commitShape{Rule: RuleSubjectCase, Shape: shapeTitleCase})
	}

	rest := conventionalPrefix.ReplaceAllString(subject, "")
	if pullRequestSuffix.MatchString(rest) {
		out = append(out, commitShape{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer})
		rest = pullRequestSuffix.ReplaceAllString(rest, "")
	}

	for _, mark := range punctMarks {
		if mark.re.MatchString(rest) {
			out = append(out, commitShape{Rule: RuleSubjectPunct, Shape: punctShape(mark)})
		}
	}

	squash, prose := false, false
	for _, ln := range raw[1:] {
		if strings.TrimSpace(ln) == "" || trailerLine.MatchString(ln) {
			continue
		}
		if squashBullet.MatchString(ln) {
			squash = true
			continue
		}
		prose = true
		break
	}

	switch {
	case prose:
		out = append(out, commitShape{Rule: RuleNoBody, Shape: shapeProse})
	case squash:
		out = append(out, commitShape{Rule: RuleNoBody, Shape: shapeSquashList})
	}

	return out
}

// punctShape is the shape name of a punctuation mark. The parenthesis mark
// is interior because the forge trailer was peeled off before the marks run.
func punctShape(mark punctMark) string {
	switch mark.re.String() {
	case ",":
		return shapeComma
	case "[()]":
		return shapeInteriorParen
	case "!":
		return shapeBang
	case `\?`:
		return shapeQuestion
	case " - ":
		return shapeSpacedHyphen
	default:
		return mark.re.String()
	}
}

// shapesClearing returns the shapes that are one population: at least
// shapeMinCount hits and shapeMinShare of that rule's hits, and the rule
// has another shape. One shape is the rule. The order is rule, then shape.
func shapesClearing(counts map[commitShape]int) []shapeTally {
	hits := map[Rule]int{}
	kinds := map[Rule]int{}
	for s, n := range counts {
		hits[s.Rule] += n
		kinds[s.Rule]++
	}

	var out []shapeTally
	for s, n := range counts {
		total := hits[s.Rule]
		if kinds[s.Rule] < 2 || total == 0 || n < shapeMinCount {
			continue
		}
		share := float64(n) / float64(total)
		if share < shapeMinShare {
			continue
		}
		out = append(out, shapeTally{commitShape: s, Count: n, Hits: total, Share: share})
	}

	slices.SortFunc(out, func(a, b shapeTally) int {
		return cmp.Or(cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Shape, b.Shape))
	})

	return out
}

func TestCommitShapesNameWhereAHitSits(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []commitShape
	}{
		{
			name: "a forge trailer", text: "fix(harness): close the gaps (#615)",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer}},
		},
		{
			name: "a parenthesis before the trailer", text: "keep the key (stable) (#615)",
			want: []commitShape{
				{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer},
				{Rule: RuleSubjectPunct, Shape: shapeInteriorParen},
			},
		},
		{
			name: "a comma", text: "keep the key, stable",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeComma}},
		},
		{
			name: "a bang", text: "keep the key!",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeBang}},
		},
		{
			name: "a question", text: "keep the key?",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeQuestion}},
		},
		{
			name: "a spaced hyphen", text: "keep the key - stable",
			want: []commitShape{{Rule: RuleSubjectPunct, Shape: shapeSpacedHyphen}},
		},
		{
			name: "title case", text: "Fix the port",
			want: []commitShape{{Rule: RuleSubjectCase, Shape: shapeTitleCase}},
		},
		{name: "a capital after a conventional prefix", text: "feat: Add a hook"},
		{
			name: "a squash list",
			text: "fix(harness): close the gaps (#615)\n\n* close the gaps\n\n* leave the lock",
			want: []commitShape{
				{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer},
				{Rule: RuleNoBody, Shape: shapeSquashList},
			},
		},
		{
			name: "prose",
			text: "keep the key\n\nThe detail belongs in the description.",
			want: []commitShape{{Rule: RuleNoBody, Shape: shapeProse}},
		},
		{
			name: "prose after a squash list",
			text: "keep the key\n\n* close the gaps\n\nThe detail belongs in the description.",
			want: []commitShape{{Rule: RuleNoBody, Shape: shapeProse}},
		},
		{name: "a trailer", text: "keep the key\n\nSigned-off-by: A Person <a@example.com>"},
		{name: "a merge", text: "Merge branch 'main'\nThe merge brought the key."},
		{name: "a scope", text: "feat(cache): keep the key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commitShapes(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("commitShapes = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestShapeGateClearsAtTwentyHitsAndNinetySevenPercent(t *testing.T) {
	trailer := commitShape{Rule: RuleSubjectPunct, Shape: shapeForgeTrailer}
	comma := commitShape{Rule: RuleSubjectPunct, Shape: shapeComma}

	clears := shapesClearing(map[commitShape]int{trailer: 97, comma: 3})
	if len(clears) != 1 || clears[0].Shape != shapeForgeTrailer || clears[0].Share < shapeMinShare {
		t.Errorf("97 of 100 = %+v, want the trailer at the share floor", clears)
	}

	if got := shapesClearing(map[commitShape]int{trailer: 19}); len(got) != 0 {
		t.Errorf("19 of 19 = %+v, want none: the count floor is 20", got)
	}

	if got := shapesClearing(map[commitShape]int{trailer: 96, comma: 4}); len(got) != 0 {
		t.Errorf("96 of 100 = %+v, want none: the share floor is 0.97", got)
	}

	title := commitShape{Rule: RuleSubjectCase, Shape: shapeTitleCase}
	if got := shapesClearing(map[commitShape]int{title: 74}); len(got) != 0 {
		t.Errorf("74 of 74 = %+v, want none: one shape is the rule", got)
	}
}

func TestSubjectMoodReportsAPastThirdPersonOrGerundOpener(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			name: "third person after a scope",
			text: "feat(proofread): adds a vale spell",
			want: []Finding{moodFinding("adds", "add")},
		},
		{
			name: "past tense, capitalized",
			text: "Fixed the port",
			want: []Finding{moodFinding("Fixed", "Fix")},
		},
		{
			name: "gerund after a breaking prefix",
			text: "fix!: making the key stable",
			want: []Finding{moodFinding("making", "make")},
		},
		{name: "an irregular past", text: "wrote the key down", want: []Finding{moodFinding("wrote", "write")}},
		{name: "the imperative", text: "feat: add an opt-in claude-code-mod harness and a harness_settings op (#577)"},
		{name: "an imperative on main", text: "fix: skip forks pending exec in the pipe walk (#574)"},
		{name: "an acronym lead", text: "URL-parse the port"},
		{name: "a noun a verb list leaves out", text: "address the review"},
		{name: "a word that only starts like a verb", text: "fixture the key"},
		{name: "a revert", text: "Revert \"feat: added a vale spell\"\n\nThis reverts commit 4f9939179."},
		{name: "a fixup", text: "fixup! added a vale spell"},
		{name: "a merge", text: "Merge branch 'main' into fixing-the-key"},
		{name: "a leading space on main", text: " Update actions (#50)"},
		{name: "an empty message", text: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleSubjectMood)), tc.want)
		})
	}
}

func moodFinding(word, want string) Finding {
	return Finding{
		Rule:         RuleSubjectMood,
		Message:      fmt.Sprintf("Open the subject with the imperative: '%s', not '%s'.", want, word),
		Match:        word,
		Line:         1,
		Replacements: []string{want},
	}
}

func TestSubjectMoodPointsAtTheWordAfterAPrefix(t *testing.T) {
	got := JudgeText("feat(proofread): adds a vale spell", KindCommitMessage, WithOnly(RuleSubjectMood))
	if len(got) != 1 || got[0].Column != 18 || got[0].EndColumn != 22 {
		t.Errorf("got %+v, want columns 18 to 22", got)
	}
}

func TestSubjectLengthCapsTheSubjectAt100Bytes(t *testing.T) {
	at := "fix: " + strings.Repeat("x", 95)
	over := at + "y"

	lengthFinding := func(n int) []Finding {
		return []Finding{{
			Rule: RuleSubjectLength,
			Message: fmt.Sprintf("Keep the subject to 100 bytes (this one has %d): cut it, or move the detail to "+
				"the body.", n),
			Line: 1,
		}}
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"100 bytes", at, nil},
		{"101 bytes", over, lengthFinding(101)},
		{"a forge's pull request number is not counted", over[:len(over)-1] + " (#546)", nil},
		{"the number does not rescue a longer subject", over + " (#546)", lengthFinding(101)},
		{"the body is not the subject", "fix: add the key\n\n" + strings.Repeat("word ", 40), nil},
		{"a revert keeps the subject it reverts", "Revert \"" + over + "\"", nil},
		{
			"a real subject over the cap, before the hook",
			"split the daemon into magus broker and magus server; tie claims and service references to a connection",
			lengthFinding(len("split the daemon into magus broker and magus server; tie claims and service references to a connection")),
		},
		{"a real subject under it", "fix: drop forwarded args at a cross-project dependency (#546)", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleSubjectLength)), tc.want)
		})
	}
}

func TestSubjectPeriodReportsATrailingFullStop(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			name: "a period",
			text: "Fix the port.",
			want: []Finding{{
				Rule: RuleSubjectPeriod, Message: "Drop the period ending the subject: it is a title.",
				Match: "port.", Line: 1, Replacements: []string{"port"},
			}},
		},
		{name: "an ellipsis", text: "wait for the daemon..."},
		{name: "a version", text: "bump go to 1.25"},
		{name: "a period in the body", text: "bump go to 1.25\n\nThe module needs it."},
		{name: "a dotted name in the middle", text: "rename magus.lock to magus.sum"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleSubjectPeriod)), tc.want)
		})
	}
}

func TestBodySeparatorWantsABlankLineAfterTheSubject(t *testing.T) {
	separator := []Finding{{
		Rule:    RuleBodySeparator,
		Message: "Leave a blank line between the subject and the body: git takes the first paragraph as the subject.",
		Line:    2,
	}}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a body with no gap", "fix: add the key\nThe key was missing.", separator},
		{"a gap", "fix: add the key\n\nThe key was missing.", nil},
		{"a subject alone", "fix: add the key", nil},
		{"a subject and a trailer", "fix: add the key\nCo-Authored-By: A Person <a@example.com>", nil},
		{"a subject and trailers", "fix: add the key\nFixes: #12\nSigned-off-by: A Person <a@example.com>", nil},
		{"a breaking footer", "fix!: add the key\nBREAKING CHANGE: the old key is gone", nil},
		{"a trailer after prose with no gap", "fix: add the key\nThe key was missing.\nFixes: #12", separator},
		{"a merge", "Merge branch 'main'\nline", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleBodySeparator)), tc.want)
		})
	}
}

// A commit meets the word rules of a written text, but not the two a pull
// request description alone is held to.
func TestCommitMessageMeetsTheWordRulesButNotTheDescriptionShape(t *testing.T) {
	const msg = "make the agent guard simply fire\n\n" +
		"The hook never fired after a rename, because the matcher kept the old key."

	got := JudgeText(msg, KindCommitMessage)

	var rules []Rule
	for _, f := range got {
		rules = append(rules, f.Rule)
	}

	if want := []Rule{RuleFiller, RuleAbsolute}; !slices.Equal(rules, want) {
		t.Errorf("rules = %v, want %v: %+v", rules, want, got)
	}

	for _, f := range got {
		if f.Rule == RuleLeadContext || f.Rule == RuleCredit {
			t.Errorf("a commit is held to %s: %+v", f.Rule, f)
		}
	}

	if got[0].Match != "simply" || got[0].Line != 1 || got[0].Decision != DecisionAdvise {
		t.Errorf("filler: %+v", got[0])
	}

	if got[1].Match != "never fired" || got[1].Line != 3 || got[1].Decision != DecisionAdvise {
		t.Errorf("absolute: %+v", got[1])
	}
}

func TestCommitMessageSkipsPullRequestRules(t *testing.T) {
	for _, r := range []Rule{RuleLeadContext, RuleCredit, RuleReplyOpener, RuleLongThread} {
		for _, k := range KindRules(KindCommitMessage) {
			if k == r {
				t.Errorf("%s judges a commit message", r)
			}
		}
	}

	// A subject such as "drop X" is a removal, which credit asks to explain.
	got := JudgeText("drop the v1 key", KindCommitMessage)
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func denyCommit(rule Rule) []Option {
	return []Option{WithOnly(rule), WithDecisions(map[Rule]Decision{rule: DecisionDeny})}
}

func TestSubjectCaseReportsTitleCaseAndShipsOff(t *testing.T) {
	want := []Finding{{
		Rule: RuleSubjectCase, Message: "Open the subject in lowercase; an acronym may lead: 'fix', not 'Fix'.",
		Match: "Fix", Line: 1, Replacements: []string{"fix"},
	}}

	assertFindings(t, JudgeText("Fix the port", KindCommitMessage, denyCommit(RuleSubjectCase)...), want)

	for _, text := range []string{"fix the port", "URL-parse the port", "A hook for commits", "feat: Add a hook"} {
		if got := JudgeText(text, KindCommitMessage, denyCommit(RuleSubjectCase)...); len(got) != 0 {
			t.Errorf("%q: got %+v, want none", text, got)
		}
	}

	if got := JudgeText("Fix the port", KindCommitMessage); len(got) != 0 {
		t.Errorf("ships off: got %+v", got)
	}
}

func TestSubjectPunctReportsTheMarksAndSkipsAConventionalPrefix(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			name: "a comma", text: "keep the key, stable",
			want: []Finding{{
				Rule: RuleSubjectPunct, Message: "Join clauses with a semicolon, not a comma.",
				Match: ",", Line: 1, Replacements: []string{";"},
			}},
		},
		{
			name: "parentheses", text: "keep the key (stable)",
			want: []Finding{{
				Rule: RuleSubjectPunct, Message: "Drop the parentheses. Join clauses with a semicolon.",
				Match: "(", Line: 1,
			}},
		},
		{
			name: "an exclamation mark", text: "keep the key!",
			want: []Finding{{
				Rule: RuleSubjectPunct, Message: "Drop the exclamation mark.",
				Match: "!", Line: 1,
			}},
		},
		{
			name: "a question mark", text: "keep the key?",
			want: []Finding{{
				Rule: RuleSubjectPunct, Message: "Drop the question mark. A subject states what the commit does.",
				Match: "?", Line: 1,
			}},
		},
		{
			name: "a spaced hyphen", text: "keep the key - stable",
			want: []Finding{{
				Rule: RuleSubjectPunct, Message: "Join clauses with a semicolon. A spaced hyphen is a dash.",
				Match: " - ", Line: 1, Replacements: []string{"; "},
			}},
		},
		{name: "a scope", text: "feat(cache): keep the key"},
		{name: "a breaking prefix", text: "feat!: drop the flag"},
		{name: "a scoped breaking prefix", text: "feat(cache)!: drop the flag"},
		{name: "a semicolon", text: "keep the key; leave the lock"},
		{name: "a forge trailer", text: "fix(harness): close the gaps (#615)"},
		{
			name: "parentheses before a forge trailer", text: "keep the key (stable) (#615)",
			want: []Finding{{
				Rule: RuleSubjectPunct, Message: "Drop the parentheses. Join clauses with a semicolon.",
				Match: "(", Line: 1,
			}},
		},
		{
			name: "a bang after the prefix", text: "feat: drop the flag!",
			want: []Finding{{
				Rule: RuleSubjectPunct, Message: "Drop the exclamation mark.",
				Match: "!", Line: 1,
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, denyCommit(RuleSubjectPunct)...), tc.want)
		})
	}

	if got := JudgeText("keep the key, stable", KindCommitMessage); len(got) != 0 {
		t.Errorf("ships off: got %+v", got)
	}
}

func TestNoBodyReportsProseAndLeavesATrailer(t *testing.T) {
	message := "Keep the commit to one subject line. Detail belongs in the pull request description."
	prose := []Finding{{
		Rule: RuleNoBody, Message: message, Match: "The detail belongs in the description.", Line: 3,
	}}
	tight := []Finding{{
		Rule: RuleNoBody, Message: message, Match: "The detail belongs in the description.", Line: 2,
	}}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a body", "keep the key\n\nThe detail belongs in the description.", prose},
		{"a body with no gap", "keep the key\nThe detail belongs in the description.", tight},
		{"a subject alone", "keep the key", nil},
		{"a blank line only", "keep the key\n\n", nil},
		{"a trailer", "keep the key\nSigned-off-by: A Person <a@example.com>", nil},
		{"a trailer after a blank line", "keep the key\n\nCo-Authored-By: A Person <a@example.com>", nil},
		{"a breaking footer", "fix!: add the key\nBREAKING CHANGE: the old key is gone", nil},
		{"a merge", "Merge branch 'main'\nThe merge brought the key.", nil},
		{"a squash list", "fix(harness): close the gaps (#615)\n\n* close the gaps\n\n* leave the lock", nil},
		{"prose after a squash list", "keep the key\n\n* close the gaps\n\nThe detail belongs in the description.", []Finding{{
			Rule: RuleNoBody, Message: message, Match: "The detail belongs in the description.", Line: 5,
		}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, denyCommit(RuleNoBody)...), tc.want)
		})
	}

	body := "keep the key\n\nThe detail belongs in the description."
	if got := JudgeText(body, KindCommitMessage); len(got) != 0 {
		t.Errorf("ships off: got %+v", got)
	}
}
