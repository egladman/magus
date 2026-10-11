package prose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update is set by CSS_GOLDEN_UPDATE=1, an environment variable rather than a
// flag so that `go test ./...` never hands an unknown flag to another package.
var update = os.Getenv("CSS_GOLDEN_UPDATE") == "1"

// cssLines renders findings one per line as `line rule match`, the golden form.
func cssLines(found []Finding) string {
	var b strings.Builder

	for _, f := range found {
		fmt.Fprintf(&b, "%d %s %q\n", f.Line, f.Rule, f.Match)
	}

	return b.String()
}

func TestCSSFixtures(t *testing.T) {
	accepts, err := filepath.Glob("testdata/css_*_accept.css")
	if err != nil || len(accepts) == 0 {
		t.Fatalf("no accept fixtures: %v", err)
	}

	for _, path := range accepts {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if got := cssLines(JudgeCSS(string(src))); got != "" {
				t.Errorf("an accepted sheet drew findings:\n%s", got)
			}
		})
	}

	rejects, err := filepath.Glob("testdata/css_*_reject.css")
	if err != nil || len(rejects) == 0 {
		t.Fatalf("no reject fixtures: %v", err)
	}

	for _, path := range rejects {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			golden := strings.TrimSuffix(path, ".css") + ".golden"
			got := cssLines(JudgeCSS(string(src)))

			if update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}

			if got != string(want) {
				t.Errorf("findings:\ngot\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestReadCSSPlacesEachComment(t *testing.T) {
	src := `/* header */

/* above a rule */
.a {
  /* above a declaration */
  color: red; /* after a declaration */

  /* alone in the block */
}

/* between rules */

@media (min-width: 40rem) {
  /* above a nested rule */
  .b { color: blue; } /* after a rule */
}
`

	got := map[string]cssPlace{}
	for _, c := range readCSS(src).comments {
		got[strings.TrimSpace(c.text)] = c.place
	}

	want := map[string]cssPlace{
		"header":              placeFile,
		"above a rule":        placeRule,
		"above a declaration": placeDeclaration,
		"after a declaration": placeTrailing,
		"alone in the block":  placeInline,
		"between rules":       placeSection,
		"above a nested rule": placeRule,
		"after a rule":        placeTrailing,
	}

	for text, place := range want {
		if got[text] != place {
			t.Errorf("%q placed %q, want %q", text, got[text], place)
		}
	}
}

func TestReadCSSAttributesACommentToTheCodeItSpeaksOf(t *testing.T) {
	src := "/* a */\n.divider:hover,\n.divider:active {\n  /* b */\n  border-radius: 0;\n  color: red; /* c */\n}\n"

	type pair struct{ target, owner string }

	got := map[string]pair{}
	for _, c := range readCSS(src).comments {
		got[strings.TrimSpace(c.text)] = pair{c.target, c.owner}
	}

	want := map[string]pair{
		"a": {".divider:hover, .divider:active", ""},
		"b": {"border-radius: 0", ".divider:hover, .divider:active"},
		"c": {"color: red", ".divider:hover, .divider:active"},
	}

	for text, p := range want {
		if got[text] != p {
			t.Errorf("%q attributed to %+v, want %+v", text, got[text], p)
		}
	}
}

func TestReadCSSSkipsWhatIsNotAComment(t *testing.T) {
	src := "a { content: \"/* not a comment */\"; background: url(data:image/svg+xml;utf8,<svg/>/*x*/); }\n" +
		"/* real */\nb { color: red; }\n"

	got := readCSS(src).comments
	if len(got) != 1 || strings.TrimSpace(got[0].text) != "real" || got[0].start != 2 {
		t.Errorf("comments = %+v, want the one real comment on line 2", got)
	}
}

func TestReadCSSLeavesToolDirectivesAlone(t *testing.T) {
	src := "/* stylelint-disable */\n/*! kept by the minifier */\n/*# sourceMappingURL=a.css.map */\n/* ===== */\na { color: red; }\n"

	if got := readCSS(src).comments; len(got) != 0 {
		t.Errorf("comments = %+v, want none: a directive and a rule line are not prose", got)
	}
}

func TestJudgeCSSMapsFindingsToSourceLines(t *testing.T) {
	src := ".a { color: red; }\n\n/* Fine.\n   It simply works. */\n.b { color: blue; }\n"

	got := JudgeCSS(src)
	if len(got) != 1 || got[0].Rule != RuleFiller || got[0].Line != 4 {
		t.Errorf("findings = %#v, want one filler finding on line 4", got)
	}
}

func TestCommentBudgetByPlace(t *testing.T) {
	text := func(n int) string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = "Line of a comment that says why."
		}

		return strings.Join(lines, "\n   ")
	}

	cases := []struct {
		name string
		src  string
		over int
	}{
		{"a header", "/* " + text(8) + " */\n\n.a { color: red; }\n", 8},
		{"above a rule", "/* " + text(4) + " */\n.a { color: red; }\n", 4},
		{"above a declaration", ".a {\n  /* " + text(3) + " */\n  color: red;\n}\n", 3},
		{"between rules", ".a { color: red; }\n\n/* " + text(3) + " */\n\n.b { color: red; }\n", 3},
		{"inside a block", ".a {\n  color: red;\n\n  /* " + text(3) + " */\n}\n", 3},
		{"after a declaration", ".a {\n  color: red; /* " + text(2) + " */\n}\n", 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rulesOf(JudgeCSS(tc.src), RuleCommentBudget); len(got) != 0 {
				t.Errorf("%d lines drew %v, want none", tc.over, got)
			}

			more := strings.Replace(tc.src, "Line of a comment that says why.", "Line of a comment that says why.\n   One more.", 1)
			if got := rulesOf(JudgeCSS(more), RuleCommentBudget); len(got) != 1 {
				t.Errorf("%d lines plus one drew %v, want one comment-budget finding", tc.over, got)
			}
		})
	}
}

func rulesOf(found []Finding, rule Rule) []Finding {
	var out []Finding

	for _, f := range found {
		if f.Rule == rule {
			out = append(out, f)
		}
	}

	return out
}

func TestBlockBudgetGrowsWithTheBlock(t *testing.T) {
	block := func(decls, comments int) string {
		var b strings.Builder

		b.WriteString(".a {\n")

		for i := 0; i < comments; i++ {
			b.WriteString("  /* Why this one. */\n  color: red;\n")
		}

		for i := 0; i < decls; i++ {
			b.WriteString("  margin: 0;\n")
		}

		b.WriteString("}\n")

		return b.String()
	}

	// 7 statements earn 6 + 7/5 = 7 lines.
	if got := rulesOf(JudgeCSS(block(0, 7)), RuleBlockBudget); len(got) != 0 {
		t.Errorf("7 comment lines on a 7-statement block drew %v", got)
	}

	if got := rulesOf(JudgeCSS(block(0, 8)), RuleBlockBudget); len(got) != 1 {
		t.Errorf("8 comment lines on an 8-statement block drew %v, want one", got)
	}

	// 10 comments and 20 plain declarations are 30 statements: 6 + 30/5 = 12.
	if got := rulesOf(JudgeCSS(block(20, 10)), RuleBlockBudget); len(got) != 0 {
		t.Errorf("10 comment lines on a 30-statement block drew %v", got)
	}

	if got := rulesOf(JudgeCSS(block(0, 10)), RuleBlockBudget); len(got) != 1 {
		t.Errorf("10 comment lines on a 10-statement block drew %v, want one", got)
	}
}

func TestReadCSSSurvivesOddSheets(t *testing.T) {
	cases := map[string]string{
		"an unterminated comment":    ".a { color: red; }\n/* never closed\n",
		"a block with no semicolon":  ".a { color: red }\n/* after */\n.b { color: blue }\n",
		"an unterminated string":     ".a { content: \"open\n}\n/* x */\n",
		"nested at-rules":            "@media (a) { @supports (b) { .a { color: red; /* in */ } } }\n",
		"a stray close":              "} /* alone */\n.a { color: red; }\n",
		"an empty file":              "",
		"a comment and nothing else": "/* only */",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("JudgeCSS panicked: %v", r)
				}
			}()

			JudgeCSS(src)
		})
	}
}

func TestReadCSSKeepsBracesInsideStringsAndUrlsOutOfTheStructure(t *testing.T) {
	src := ".a { content: \"}\"; background: url(a}b.png); }\n/* after */\n.b { color: red; }\n"

	got := readCSS(src).comments
	if len(got) != 1 || got[0].place != placeRule || got[0].target != ".b" {
		t.Errorf("comments = %+v, want one comment above .b", got)
	}
}

func TestJudgeTextRoutesCSS(t *testing.T) {
	src := "/* Simply put. */\na { color: red; }\n"

	if got := JudgeText(src, KindCSS); len(got) != len(JudgeCSS(src)) || len(got) == 0 {
		t.Errorf("JudgeText(KindCSS) = %#v, want JudgeCSS's findings", got)
	}
}
