package proofread

import (
	"slices"
	"strings"
	"testing"
)

// entryKind is the declared kind ADR 0033 shows.
func entryKind(t *testing.T) KindSpec {
	t.Helper()

	minimum := 90

	specs, err := DeclareKinds(map[string]KindDecl{"entry": {
		Extends: KindReleaseNotes,
		Reader:  &Reader{Format: FormatLines},
		Rules:   map[string]string{"tense": "deny", "claim": "deny", "buzzword": "deny"},
		Minimum: &minimum,
		Terms:   map[string]string{"utilize": "use", "spearheaded": "led"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	return specs[0]
}

func declareError(t *testing.T, decl KindDecl) string {
	t.Helper()

	_, err := DeclareKinds(map[string]KindDecl{"entry": decl})
	if err == nil {
		t.Fatal("DeclareKinds accepted the declaration")
	}

	return err.Error()
}

func rulesOf(found []Finding) []Rule {
	out := make([]Rule, len(found))
	for i, f := range found {
		out[i] = f.Rule
	}

	return out
}

func TestADeclaredKindStartsFromItsBase(t *testing.T) {
	s := entryKind(t)

	if s.Extends != KindReleaseNotes || s.Reader.Format != FormatLines {
		t.Fatalf("entry extends %q, reads %q", s.Extends, s.Reader.Format)
	}

	for rule, want := range map[Rule]Decision{
		RuleTense: DecisionDeny, RuleClaim: DecisionDeny, RuleFiller: DecisionAdvise, RuleTerms: DecisionDeny,
	} {
		if got := s.Rules[rule]; got != want {
			t.Errorf("entry %s = %s, want %s", rule, got, want)
		}
	}
}

func TestADeclaredKindJudgesEachLineByItsRules(t *testing.T) {
	found := JudgeText("Spearheaded the cache rewrite\nThis will utilize the new index", "entry", WithKinds(entryKind(t)))

	var replaced []string

	for _, f := range found {
		if f.Rule == RuleTerms {
			replaced = append(replaced, f.Match+"->"+f.Replacements[0])
		}
	}

	if want := []string{"Spearheaded->Led", "utilize->use"}; !slices.Equal(replaced, want) {
		t.Errorf("terms replaced %q, want %q", replaced, want)
	}

	if !slices.Contains(rulesOf(found), RuleTense) {
		t.Errorf("findings %v hold no tense finding for 'will'", rulesOf(found))
	}
}

func TestADeclaredKindKeepsTheBuiltInGlossary(t *testing.T) {
	found := JudgeText("Hand the work to a sub-agent", "entry", WithKinds(entryKind(t)))

	if !slices.ContainsFunc(found, func(f Finding) bool { return f.Rule == RuleTerms && f.Match == "sub-agent" }) {
		t.Errorf("findings %v do not replace sub-agent", rulesOf(found))
	}
}

func TestADeclaredKindIsGradedAtItsMinimum(t *testing.T) {
	text := strings.Repeat("Basically the cache really works\n", 6)

	found := JudgeText(text, "entry", WithKinds(entryKind(t)))
	if !slices.Contains(rulesOf(found), RuleGrade) {
		t.Errorf("findings %v hold no grade finding under the declared minimum of 90", rulesOf(found))
	}
}

func TestASetWeightIsMarkedAsSet(t *testing.T) {
	minimum := 100

	specs, err := DeclareKinds(map[string]KindDecl{"page": {
		Extends: KindReference, Weights: map[string]float64{"filler": 0.5}, Minimum: &minimum,
	}})
	if err != nil {
		t.Fatal(err)
	}

	found := JudgeText("It basically works.\n", "page", WithKinds(specs...))

	i := slices.IndexFunc(found, func(f Finding) bool { return f.Rule == RuleFiller })
	if i < 0 || found[i].Weight != 0.5 || !found[i].WeightSet {
		t.Fatalf("findings %+v, want a filler finding weighing a set 0.5", found)
	}

	g := slices.IndexFunc(found, func(f Finding) bool { return f.Rule == RuleGrade })
	if g < 0 || !strings.Contains(found[g].Message, "(weight set, not measured)") {
		t.Errorf("findings %+v: no grade finding marking filler's weight as set", found)
	}
}

func TestAMeasuredWeightIsNotMarkedAsSet(t *testing.T) {
	for _, f := range JudgeText("It basically works.\n", KindReference) {
		if f.WeightSet {
			t.Errorf("%s on a built-in kind is marked set", f.Rule)
		}
	}
}

func TestADeclaredDecisionRunsAnUnmeasuredRule(t *testing.T) {
	if !unmeasured[RuleCompactMarkup] {
		t.Skip("compact-markup is measured now; pick another unmeasured rule")
	}

	specs, err := DeclareKinds(map[string]KindDecl{"notes": {
		Extends: KindAgentCompact, Rules: map[string]string{"compact-markup": "advise"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(rulesOf(JudgeText("run **build** first\n", "notes", WithKinds(specs...))), RuleCompactMarkup) {
		t.Error("a declared advise on compact-markup did not run it")
	}
}

func TestDeclareKindsRefusesAnUnknownBase(t *testing.T) {
	got := declareError(t, KindDecl{Extends: "release"})
	if !strings.HasPrefix(got, `kinds.entry.extends: unknown kind "release"`) {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesAnUnknownRule(t *testing.T) {
	got := declareError(t, KindDecl{Extends: KindReference, Rules: map[string]string{"tone": "deny"}})
	if !strings.HasPrefix(got, "kinds.entry.rules.tone: unknown rule") {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesAnUnknownDecision(t *testing.T) {
	got := declareError(t, KindDecl{Extends: KindReference, Rules: map[string]string{"tense": "warn"}})
	if got != `kinds.entry.rules.tense: unknown decision "warn": want off, advise or deny` {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesARuleItsFormatCannotFeed(t *testing.T) {
	got := declareError(t, KindDecl{Extends: KindReference, Rules: map[string]string{"message-length": "deny"}})
	if got != "kinds.entry.rules.message-length: rule message-length reads message, not the markdown format" {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesABaseRuleTheNewFormatCannotFeed(t *testing.T) {
	got := declareError(t, KindDecl{Extends: KindStylesheet, Reader: &Reader{Format: FormatMarkdown}})
	if !strings.HasPrefix(got, "kinds.entry.extends: rule comment-block reads doc-comment, stylesheet") {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesABuiltInName(t *testing.T) {
	_, err := DeclareKinds(map[string]KindDecl{"changelog": {Extends: KindReference}})
	if err == nil || !strings.HasPrefix(err.Error(), `kinds.changelog.name: "changelog" is a built-in kind`) {
		t.Errorf("error = %v", err)
	}
}

func TestDeclareKindsRefusesAKindWithNoReader(t *testing.T) {
	if got := declareError(t, KindDecl{}); got != "kinds.entry.reader: set a reader, or extend a kind for its reader" {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesAnUnknownFormat(t *testing.T) {
	got := declareError(t, KindDecl{Reader: &Reader{Format: "rst"}})
	if !strings.HasPrefix(got, `kinds.entry.reader.format: unknown format "rst"`) {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesAMarkdownPropertyOnAnotherFormat(t *testing.T) {
	got := declareError(t, KindDecl{Reader: &Reader{Format: FormatLines, Titled: true}})
	if got != "kinds.entry.reader.titled: the lines format reads no Markdown, so it takes no titled" {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesAWeightOutOfRange(t *testing.T) {
	got := declareError(t, KindDecl{Extends: KindReference, Weights: map[string]float64{"filler": 2}})
	if got != "kinds.entry.weights.filler: want a weight from 0 to 1, not 2" {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesAWeightOnARuleTheKindDoesNotJudge(t *testing.T) {
	got := declareError(t, KindDecl{Extends: KindReference, Weights: map[string]float64{"subject-mood": 0.5}})
	if !strings.HasPrefix(got, "kinds.entry.weights.subject-mood: the kind does not judge subject-mood") {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesAMinimumOutOfRange(t *testing.T) {
	minimum := 101

	got := declareError(t, KindDecl{Extends: KindReference, Minimum: &minimum})
	if got != "kinds.entry.minimum: want a grade from 0 to 100, not 101" {
		t.Errorf("error = %q", got)
	}
}

func TestDeclareKindsRefusesTermsWithTheTermsRuleOff(t *testing.T) {
	got := declareError(t, KindDecl{
		Extends: KindReference, Rules: map[string]string{"terms": "off"}, Terms: map[string]string{"utilize": "use"},
	})
	if got != "kinds.entry.terms: set, but rules.terms is off, so nothing would report them" {
		t.Errorf("error = %q", got)
	}
}

func TestTheLinesFormatReadsEachLineAsAUnit(t *testing.T) {
	_, prose := readLines([]string{"# Not a heading", "first entry", "- second entry", "", "third entry"})

	for _, ln := range prose {
		if !ln.opens || !ln.paragraph || ln.heading {
			t.Errorf("line %d %q: opens %t, paragraph %t, heading %t", ln.line, ln.body(), ln.opens, ln.paragraph, ln.heading)
		}
	}

	if len(prose) != 4 {
		t.Errorf("read %d lines, want 4", len(prose))
	}
}
