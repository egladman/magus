package proofread

import (
	"strings"
	"testing"
)

func triadMatches(found []Finding) []string {
	var out []string
	for _, f := range found {
		out = append(out, f.Match)
	}

	return out
}

func TestTriadReportsTwoSeriesOfThree(t *testing.T) {
	text := "Read the cache\n\nThe loader reads the cache, the graph, and the log.\n\n" +
		"It also checks size, age and owner before it writes anything.\n"

	got := triadMatches(JudgeText(text, KindChangeDescription, WithOnly(RuleTriad)))
	want := []string{"the cache, the graph, and the log", "size, age and owner"}

	if !containEach(got, want) {
		t.Errorf("triad matched %q, want each to hold %q", got, want)
	}
}

func TestTriadReportsOneSeriesInTheLeadsFirstSentence(t *testing.T) {
	text := "Reviewers can now see the cache, the graph, and the log in one view. The old panel hid all of it.\n"

	for _, kind := range []Kind{KindChangeDescription, KindReviewSummary, KindReviewReply} {
		titled := ""
		if kind == KindChangeDescription {
			titled = "Show the log\n\n"
		}

		got := triadMatches(JudgeText(titled+text, kind, WithOnly(RuleTriad)))
		if len(got) != 1 || !strings.Contains(got[0], "the cache, the graph, and the log") {
			t.Errorf("%s: triad matched %q, want the lead's series", kind, got)
		}
	}
}

func TestTriadReportsThreeParallelSentencesAndBullets(t *testing.T) {
	text := "The cache moved.\n\nIt stops early.\n\nIt reads the cache. It reads the graph. It reads the log.\n\n" +
		"- Faster builds\n- Cleaner logs\n- Fewer flakes\n"

	got := triadMatches(JudgeText(text, KindChangeDescription, WithOnly(RuleTriad)))
	want := []string{"It reads the cache. It reads the graph. It reads the log.", "Faster builds"}

	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("triad matched %q, want %q", got, want)
	}
}

func TestTriadReportsOnCommitMessages(t *testing.T) {
	text := "Read the cache\n\nThe reader keeps the cache, the graph and the log. It then drops stale, old and dead rows.\n"

	if got := triadMatches(JudgeText(text, KindCommitMessage, WithOnly(RuleTriad))); len(got) != 2 {
		t.Errorf("triad matched %q, want 2 series", got)
	}
}

func TestTriadLeavesAlone(t *testing.T) {
	lead := "Tidy the loader\n\nIt stops early.\n\n"
	bullets := "- Faster builds\n- Cleaner logs\n- Fewer flakes\n"
	numbered := "1. Faster builds\n2. Cleaner logs\n3. Fewer flakes\n"
	series := "\nIt reads the cache, the graph, and the log.\n"

	for name, text := range map[string]string{
		"code spans": "Touches `a.go`, `b.go`, and `c.go`.\n\nAlso `x`, `y`, and `z` here.\n",
		"paths":      "Edits libs/a.go, libs/b.go, and libs/c.go.\n\nMoves docs/a.md, docs/b.md and docs/c.md.\n",
		"flags":      "Adds --dry-run, --force, and --quiet.\n\nTakes --a, --b and --c too.\n",
		"all caps":   "Sets HOME, PATH, and SHELL.\n\nAlso reads TERM, LANG and EDITOR.\n",
		"numbers":    "Covers versions 1, 2, and 3.\n\nAlso 4, 5 and 6.\n",
		"inline numbering": "Does (1) read the cache, (2) read the graph, and (3) read the log.\n\n" +
			"Then (1) a, (2) b and (3) c.\n",
		"ordinals":              "It does first this, second that, and third the other.\n\nAnd next one, two and three.\n",
		"numbered list":         "Reads the lot.\n\n" + numbered + series,
		"one mid-paragraph":     "The loader now retries the fetch. It reads the cache, the graph, and the log, then stops.\n",
		"four items":            "It reads the cache, the graph, the log, and the index.\n\nAlso a, b, c, and d.\n",
		"a quoted series":       "The label says \"the cache, the graph, and the log\" and \"red, green and blue\".\n",
		"two sentences":         "It reads the cache. It reads the graph. Then it stops.\n",
		"two bullets":           "- Faster builds\n- Cleaner logs\n",
		"four bullets":          "- Faster builds\n- Cleaner logs\n- Fewer flakes\n- Less noise\n",
		"long bullets":          "- Reads the cache before it touches the network at all\n- Writes\n- Stops\n",
		"bullets with code":     "- Adds `a`\n- Adds `b`\n- Adds `c`\n",
		"a series after a list": "The whole flow.\n\n" + numbered + "\nIt reads the cache, the graph, and the log.\n",
	} {
		if got := triadMatches(JudgeText(lead+text, KindChangeDescription, WithOnly(RuleTriad))); len(got) != 0 {
			t.Errorf("%s: triad matched %q, want nothing", name, got)
		}
	}

	// The same bullets report beside a series; numbered, they do not.
	if got := triadMatches(JudgeText(lead+"Reads the lot.\n\n"+bullets+series, KindChangeDescription, WithOnly(RuleTriad))); len(got) != 2 {
		t.Errorf("bullets and a series matched %q, want 2", got)
	}
}

func TestTriadJudgesNoOtherKind(t *testing.T) {
	text := "It reads the cache, the graph, and the log.\n\nAlso size, age and owner.\n"

	for _, kind := range []Kind{KindGuide, KindReference, KindDocComment} {
		if got := triadMatches(JudgeText(text, kind, WithOnly(RuleTriad))); len(got) != 0 {
			t.Errorf("%s: triad matched %q, want nothing", kind, got)
		}
	}
}

func TestStackedColonReportsTheSecondClauseColon(t *testing.T) {
	for name, text := range map[string]string{
		"two":             "The old loop retried forever: it hid the failure, and the reason is simple: nobody capped it.\n",
		"after the label": "Cut: the old retry loop: it hid failures; the new cap: ten tries.\n",
	} {
		got := triadMatches(JudgeText(text, KindChangeDescription, WithOnly(RuleStackedColon)))
		if len(got) != 1 {
			t.Errorf("%s: stacked-colon matched %q, want one finding", name, got)
		}
	}

	got := triadMatches(JudgeText("The old loop retried forever: it hid the failure, and the reason is simple: nobody capped it.\n",
		KindReviewInline, WithOnly(RuleStackedColon)))
	if len(got) != 1 || got[0] != "simple:" {
		t.Errorf("stacked-colon matched %q, want simple:", got)
	}
}

func TestStackedColonLeavesAlone(t *testing.T) {
	for name, text := range map[string]string{
		"one colon":       "The old loop retried forever: it hid the failure.\n",
		"a leading label": "Not done: the retry cap, because it needs a config key.\n",
		"a label and one": "Cut: the old retry loop, which hid the failure and so cost an hour: nobody noticed.\n",
		"a time":          "The job runs at 10:30 and 11:45 daily, so the old loop retried forever: nobody noticed.\n",
		"code and a url": "The old loop retried forever: see `a: b` and https://example.dev/p: q for the " +
			"details of how it ran.\n",
		"a quote":        "The old loop retried forever: the log says \"cap: none\" for it.\n",
		"separate lines": "The old loop retried forever: it hid the failure.\n\nThe cap is simple: ten tries.\n",
		"a bold label":   "**Cut:** the old retry loop. **Why:** it hid the failure.\n",
	} {
		if got := triadMatches(JudgeText(text, KindChangeDescription, WithOnly(RuleStackedColon))); len(got) != 0 {
			t.Errorf("%s: stacked-colon matched %q, want nothing", name, got)
		}
	}
}

func TestRestatesDiffReportsAVerbAndWhatItTouched(t *testing.T) {
	text := "Makes the cache honest.\n\n- Updates `foo.go`\n- Adds X to Y\n- Renamed libs/proofread/slop.go.\n"

	got := triadMatches(JudgeText(text, KindChangeDescription, WithOnly(RuleRestatesDiff)))
	want := []string{"Updates `foo.go`", "Adds X to Y", "Renamed libs/proofread/slop.go."}

	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("restates-diff matched %q, want %q", got, want)
	}
}

func TestRestatesDiffLeavesAlone(t *testing.T) {
	for name, text := range map[string]string{
		"a reason":           "- Updates `foo.go` so the cache key includes the tenant.\n",
		"a colon":            "- Updates `foo.go`: the key was wrong.\n",
		"a purpose":          "- Adds `retry` to avoid dropped writes.\n",
		"a content word":     "- Adds retry to the fetch loop.\n",
		"code and a content": "- Adds `retry` to the fetch loop\n",
		"no code":            "- Removes the old flag.\n",
		"a plain sentence":   "The cache now survives a restart.\n",
		"two sentences":      "Updates `foo.go`. It was stale.\n",
		"a long sentence":    "Updates `a.go`, `b.go`, `c.go`, `d.go`, `e.go`, `f.go`, `g.go`, `h.go`, `i.go`, `j.go`, `k.go`, `l.go`, `m.go`, `n.go`.\n",
	} {
		if got := triadMatches(JudgeText(text, KindChangeDescription, WithOnly(RuleRestatesDiff))); len(got) != 0 {
			t.Errorf("%s: restates-diff matched %q, want nothing", name, got)
		}
	}
}

func TestTellsShipOff(t *testing.T) {
	shipEvidence(t)

	text := "Updates `foo.go`\n\nIt reads the cache, the graph, and the log: it keeps them: all.\n"

	for _, rule := range []Rule{RuleTriad, RuleStackedColon, RuleRestatesDiff} {
		if !shippedUnmeasured[rule] {
			t.Errorf("%s is not unmeasured", rule)
		}

		if got := JudgeText(text, KindChangeDescription, WithOnly(rule)); len(got) != 0 {
			t.Errorf("%s ships on: %v", rule, got)
		}
	}
}

func TestContrastJudgesReviewText(t *testing.T) {
	text := "This isn't a cache problem, it's a lock problem.\n"

	for _, kind := range []Kind{KindReviewSummary, KindReviewInline, KindReviewReply} {
		got := JudgeText(text, kind, WithOnly(RuleContrast))
		if len(got) != 1 || got[0].Decision != DecisionAdvise {
			t.Errorf("%s: contrast found %v, want one advise finding", kind, got)
		}
	}
}

func containEach(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}

	for i := range got {
		if !strings.Contains(got[i], want[i]) {
			return false
		}
	}

	return true
}
