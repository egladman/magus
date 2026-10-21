package proofread

import (
	"maps"
	"os"
	"testing"
)

// shippedUnmeasured is unmeasured as it ships. TestMain clears the table, so
// every other test in the package judges a rule's logic at its declared
// decision; the tests here put it back.
var shippedUnmeasured map[Rule]bool

func TestMain(m *testing.M) {
	shippedUnmeasured = maps.Clone(unmeasured)
	clear(unmeasured)

	os.Exit(m.Run())
}

// shipEvidence restores the shipped table for one test.
func shipEvidence(t *testing.T) {
	t.Helper()
	maps.Copy(unmeasured, shippedUnmeasured)
	t.Cleanup(func() { clear(unmeasured) })
}

// declared is each rule's strictest declared decision over the kinds it
// judges, for the rules a check runs.
func declared() map[Rule]Decision {
	out := map[Rule]Decision{}

	for _, c := range checks {
		for _, k := range c.on {
			if d := c.declaredDecision(k); out[c.rule] != DecisionDeny && d != DecisionOff {
				out[c.rule] = d
			}
		}

		if _, ok := out[c.rule]; !ok {
			out[c.rule] = DecisionOff
		}
	}

	return out
}

// An entry that changes nothing is stale: it would hide the day its rule is
// measured or declared again without evidence.
func TestEvidenceTablesChangeEachRuleTheyName(t *testing.T) {
	strictest := declared()

	for r := range shippedUnmeasured {
		switch d, ok := strictest[r]; {
		case !ok:
			t.Errorf("unmeasured names %q, which no check runs", r)
		case d == DecisionOff:
			t.Errorf("unmeasured names %q, which its check already ships off", r)
		case weights[r] != 0:
			t.Errorf("unmeasured names %q, which weighs %v: a weight means a round measured it", r, weights[r])
		}
	}

	for r := range weights {
		if _, ok := strictest[r]; !ok {
			t.Errorf("weights names %q, which no check runs", r)
		}
	}
}

func TestCatalogShipsTheEvidence(t *testing.T) {
	shipEvidence(t)

	for _, doc := range Catalog() {
		for k, d := range doc.Decisions {
			if shippedUnmeasured[doc.Name] && d != DecisionOff {
				t.Errorf("%s ships %s on %s, but no round measured it", doc.Name, d, k)
			}
		}

		if doc.Weight != weights[doc.Name] {
			t.Errorf("%s: the catalog gives weight %v, the table %v", doc.Name, doc.Weight, weights[doc.Name])
		}
	}

	var blame []Finding

	for _, f := range JudgeText("This is dumb.\n", KindReviewReply) {
		if f.Rule == RuleBlame {
			blame = append(blame, f)
		}
	}

	if len(blame) != 1 || blame[0].Decision != DecisionAdvise || blame[0].Weight != weights[RuleBlame] {
		t.Errorf("blame ships as %v, want one advise finding weighing %v", blame, weights[RuleBlame])
	}
}
