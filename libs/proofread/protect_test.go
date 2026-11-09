package proofread

import (
	"slices"
	"strings"
	"testing"
)

func spanTexts(spans []Protected) []string {
	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.Text
	}

	return out
}

func TestProtectedSpansFindANumberWithItsUnit(t *testing.T) {
	got := spanTexts(protectedSpans([]string{"The build dropped from 410ms to 260 ms, and 70% of runs hit the cache."}))

	if !slices.Equal(got, []string{"410ms", "260 ms", "70%"}) {
		t.Errorf("spans = %q, want the three measured numbers", got)
	}
}

func TestProtectedSpansFindAMomentTheAuthorSaw(t *testing.T) {
	got := protectedSpans([]string{"It only failed when the busiest runner picked it up, so I added a lock."})

	if len(got) != 1 || got[0].Text != "when the busiest runner picked it up" || got[0].Reason != reasonAnecdote {
		t.Errorf("spans = %+v, want the when-clause", got)
	}
}

func TestProtectedSpansFindANameMidSentence(t *testing.T) {
	got := spanTexts(protectedSpans([]string{"A user in Sydney sent a clear example."}))

	if !slices.Equal(got, []string{"Sydney"}) {
		t.Errorf("spans = %q, want the name", got)
	}
}

func TestProtectedSpansSkipTitleCaseLabelsAndCode(t *testing.T) {
	got := protectedSpans([]string{"**Escalation Path:** see the Policy Impact notes and run `sleep 10s` first."})

	if len(got) != 0 {
		t.Errorf("spans = %+v, want none in a label, a Title Case run or a code span", got)
	}
}

const measuredSeries = "fix(cache): read the key once\n\n" +
	"The loader reads the cache, the graph and the log. We moved the docs, rewrote the parser for Sydney " +
	"and shipped the fix.\n"

func TestProtectKeepsASpecificAnEconomyFindingWouldCut(t *testing.T) {
	got := JudgeText(measuredSeries, KindChangeDescription, WithOnly(RuleTriad),
		WithDecisions(map[Rule]Decision{RuleTriad: DecisionDeny}))

	i := slices.IndexFunc(got, func(f Finding) bool { return strings.Contains(f.Match, "Sydney") })
	if i < 0 {
		t.Fatalf("triad = %+v, want a finding over the verb series", got)
	}

	f := got[i]
	if f.Decision != DecisionAdvise || len(f.Protected) != 1 || !strings.Contains(f.Message, "Keep 'Sydney', a name") {
		t.Errorf("finding = %+v, want advice that names the protected span", f)
	}

	if other := got[1-i]; other.Decision != DecisionDeny || len(other.Protected) != 0 {
		t.Errorf("the series with no specific = %+v, want it to stay a deny", other)
	}
}

func TestProtectLeavesFindingsOfOtherDimensions(t *testing.T) {
	got := JudgeText("Done, it is 100% faster now.\n", KindReviewReply, WithOnly(RuleClaim),
		WithDecisions(map[Rule]Decision{RuleClaim: DecisionDeny}))

	for _, f := range got {
		if len(f.Protected) != 0 || f.Decision != DecisionDeny {
			t.Errorf("claim finding = %+v, want it unprotected and still a deny", f)
		}
	}
}
