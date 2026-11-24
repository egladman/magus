package proofread

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func judgeGo(src string, opts ...Option) []Finding {
	return JudgeText(src, KindCodeComment, append([]Option{WithLanguage(LanguageGo)}, opts...)...)
}

func distinctRules(found []Finding) []Rule {
	var out []Rule

	for _, f := range found {
		if !slices.Contains(out, f.Rule) {
			out = append(out, f.Rule)
		}
	}

	return out
}

func assertRules(t *testing.T, found []Finding, want ...Rule) {
	t.Helper()

	if got := distinctRules(found); !slices.Equal(got, want) {
		t.Errorf("rules = %v, want %v; findings %#v", got, want, found)
	}
}

// inBody wraps comment, one line per entry, inside a function body.
func inBody(comment ...string) string {
	return "package p\n\nfunc f() {\n\t// " + strings.Join(comment, "\n\t// ") + "\n\tx()\n}\n"
}

// The reference fixture: these blocks are the slop the sweep removes, and
// the rest are comments it keeps.
func TestCodeCommentJudgesTheWatchdogFixture(t *testing.T) {
	src, err := os.ReadFile("testdata/code_comment/watchdog.go.txt")
	if err != nil {
		t.Fatal(err)
	}

	f := readCode(string(src), LanguageGo)
	blockOf := func(line int) int {
		for _, c := range f.comments {
			if line >= c.start && line < c.start+strings.Count(c.text, "\n")+1 {
				return c.start
			}
		}

		return 0
	}

	flagged := map[int]bool{}
	for _, found := range judgeGo(string(src)) {
		flagged[blockOf(found.Line)] = true
	}

	docs := []int{16, 28, 44, 61, 143, 150, 157, 265}
	inline := []int{113, 127, 132, 197, 214}

	for _, c := range f.comments {
		want := slices.Contains(docs, c.start) || slices.Contains(inline, c.start)
		if flagged[c.start] != want {
			t.Errorf("block at :%d (%s) flagged %v, want %v", c.start, c.place, flagged[c.start], want)
		}
	}

	for _, at := range inline {
		if i := slices.IndexFunc(f.comments, func(c codeComment) bool { return c.start == at }); i < 0 || f.comments[i].place != codeInline {
			t.Errorf("block at :%d is not read as inline", at)
		}
	}
}

func TestCodeCommentBudgetCountsProseLinesOnly(t *testing.T) {
	assertRules(t, judgeGo(inBody("one.", "two.", "three.", "four.")), RuleCodeCommentBudget)

	spaced := inBody("one.", "", "two.", "", "three.", "", "https://example.com/a")
	assertRules(t, judgeGo(spaced))
}

func TestCodeCommentBudgetCountsWords(t *testing.T) {
	long := strings.Repeat("word ", 12) + "end."
	assertRules(t, judgeGo("package p\n\nfunc f() {\n\tx() // "+long+"\n}\n"), RuleCodeCommentBudget)
	assertRules(t, judgeGo("package p\n\nfunc f() {\n\tx() // a short one\n}\n"))
}

func TestCodeCommentBudgetsTighten(t *testing.T) {
	b, err := ParseCommentBudgets([]byte(`{"inline": {"lines": 1}}`))
	if err != nil {
		t.Fatal(err)
	}

	assertRules(t, judgeGo(inBody("one.", "two."), WithCommentBudgets(b)), RuleCodeCommentBudget)

	if _, err := ParseCommentBudgets([]byte(`{"inline": {"words": 31}}`)); err == nil {
		t.Error("a loosened budget parsed")
	}

	if _, err := ParseCommentBudgets([]byte(`{"inlin": {}}`)); err == nil {
		t.Error("an unknown key parsed")
	}
}

func TestCodeCommentSentenceOverBudget(t *testing.T) {
	long := strings.Repeat("word ", 25) + "end."
	assertRules(t, judgeGo("package p\n\n// Exported "+long+"\nfunc Exported() {}\n"), RuleCodeCommentSentence)
}

func TestCodeCommentProvenance(t *testing.T) {
	assertRules(t, judgeGo(inBody("Added on 2026-09-04.")), RuleProvenance)
	assertRules(t, judgeGo(inBody("See #812 for the fix.")), RuleProvenance)
	assertRules(t, judgeGo(inBody("Landed in 3f9a2c1d.")), RuleProvenance)
	assertRules(t, judgeGo(inBody("The incident left locks held.")), RuleProvenance)
	assertRules(t, judgeGo(inBody("Measured 1765 commits; 502 end in a trailer.")))
}

func TestCodeCommentDocPath(t *testing.T) {
	assertRules(t, judgeGo(inBody("Per docs/design/principles/x.md.")), RuleDocPath)
	assertRules(t, judgeGo(inBody("ADR 0055 rules it.")), RuleDocPath)
	assertRules(t, judgeGo(inBody("It calls [Judge] first.")))
}

func TestCodeCommentCaps(t *testing.T) {
	assertRules(t, judgeGo(inBody("This is NOT safe.")), RuleCommentCaps)
	assertRules(t, judgeGo(inBody("Keep it SILENT.")), RuleCommentCaps)
	assertRules(t, judgeGo(inBody("JSON over HTTP, then MGS3012 and EX_TEMPFAIL.")))
}

func TestCodeCommentCapsPassesAnIdentifierTheFileSpells(t *testing.T) {
	src := "package p\n\nconst POLICY = 1\n\nfunc f() {\n\t// POLICY is read once.\n\tx()\n}\n"
	assertRules(t, judgeGo(src))
}

func TestCodeCommentFirstPerson(t *testing.T) {
	assertRules(t, judgeGo(inBody("We retry here.")), RuleCommentFirstPerson)
	assertRules(t, judgeGo(inBody("Blocks on I/O.")))
}

func TestCodeCommentBanner(t *testing.T) {
	assertRules(t, judgeGo(inBody("---- setup ----")), RuleBanner)
}

func TestCodeCommentHistory(t *testing.T) {
	assertRules(t, judgeGo(inBody("This no longer reads the cache.")), RuleCommentHistory)
	assertRules(t, judgeGo(inBody("It used to sign twice.")), RuleCommentHistory)
}

// restates is house style, as it is on a stylesheet, so a table turns it on.
func TestCodeCommentRestates(t *testing.T) {
	on := WithDecisions(map[Rule]Decision{RuleRestates: DecisionDeny})

	src := "package p\n\nfunc f() {\n\t// get the user\n\tuser := getUser()\n}\n"
	assertRules(t, judgeGo(src, on), RuleRestates)
	assertRules(t, judgeGo(src))

	why := "package p\n\nfunc f() {\n\t// the cache may be cold\n\tuser := getUser()\n}\n"
	assertRules(t, judgeGo(why, on))
}

func TestCodeCommentMarkerParagraphIsNeverAFinding(t *testing.T) {
	src := inBody("TODO: this is NOT done, see #12 and 2026-09-04; we will fix it later and so on and so on.")
	assertRules(t, judgeGo(src))

	mixed := inBody("TODO: later.", "", "We retry.")
	assertRules(t, judgeGo(mixed), RuleCommentFirstPerson)
}

func TestCodeCommentJudgesNothingWithoutALanguage(t *testing.T) {
	if got := JudgeText(inBody("We retry."), KindCodeComment); len(got) != 0 {
		t.Errorf("JudgeText with no language = %#v, want none", got)
	}
}
