package proofread

import (
	"maps"
	"os"
	"testing"
)

// shippedCeiling is evidenceCeiling as it ships. TestMain clears the table, so
// every other test in the package judges a rule's logic at its declared
// decision; the tests here put it back to judge what ships.
var shippedCeiling map[Rule]Decision

func TestMain(m *testing.M) {
	shippedCeiling = maps.Clone(evidenceCeiling)
	clear(evidenceCeiling)

	os.Exit(m.Run())
}

// shipCeiling restores the shipped ceiling for one test.
func shipCeiling(t *testing.T) {
	t.Helper()
	maps.Copy(evidenceCeiling, shippedCeiling)
	t.Cleanup(func() { clear(evidenceCeiling) })
}

// An entry that caps nothing is stale: its rule already ships at or below the
// ceiling, so the entry would hide the day its rule is raised without evidence.
func TestEvidenceCeilingLowersEachRuleItNames(t *testing.T) {
	declared := map[Rule]Decision{}

	for _, c := range checks {
		for _, k := range c.on {
			if d := c.declaredDecision(k); strictness(d) > strictness(declared[c.rule]) {
				declared[c.rule] = d
			}
		}
	}

	for r, ceiling := range shippedCeiling {
		d, ok := declared[r]
		if !ok {
			t.Errorf("evidenceCeiling names %q, which no check runs", r)
		} else if strictness(d) <= strictness(ceiling) {
			t.Errorf("evidenceCeiling caps %q at %s, but it is declared %s: delete the entry", r, ceiling, d)
		}
	}
}

func TestCatalogShipsTheCeiling(t *testing.T) {
	shipCeiling(t)

	for _, doc := range Catalog() {
		ceiling, ok := evidenceCeiling[doc.Name]
		if !ok {
			continue
		}

		for k, d := range doc.Decisions {
			if strictness(d) > strictness(ceiling) {
				t.Errorf("%s ships %s on %s past its ceiling %s", doc.Name, d, k, ceiling)
			}
		}
	}

	if got := JudgeText("Simply ask.\n", KindReference); len(got) != 1 || got[0].Decision != DecisionAdvise {
		t.Errorf("filler ships as %v, want one advise finding", got)
	}
}
