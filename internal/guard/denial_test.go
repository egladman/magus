package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/hint"
)

// TestShapeDenyFallsBackToTheFullReason pins every arm that must never shorten: a repeat
// with nowhere to store the verdict would cite a ref that resolves to nothing, and a rule
// the catalog does not list has no summary line or page to cite.
func TestShapeDenyFallsBackToTheFullReason(t *testing.T) {
	const see = "\nsee: " + ruleDocsBase + "whole-tree/"
	const note = "\nnothing ran (2 commands)"

	noStore := hint.NewGate("", "s1")
	for range 2 {
		got, ref := shapeDeny(t.Context(), noStore, string(denyRuleWholeTree), "why", note)
		assert.Equal(t, "why"+note+see, got, "without a cache dir every firing is the first")
		assert.Empty(t, ref)
	}

	gate := hint.NewGate(t.TempDir(), "s1")
	for _, rule := range []string{"a-workspace-rule", string(advisoryPushGate)} {
		for range 2 {
			got, ref := shapeDeny(t.Context(), gate, rule, "why", note)
			assert.Equal(t, "why", got, "%s is not a catalogued deny, so its reason is untouched", rule)
			assert.Empty(t, ref)
		}
	}

	got, _ := shapeDeny(t.Context(), gate, string(denyRuleWholeTree), "why"+note, note)
	assert.Equal(t, "why"+note+see, got, "a reason already carrying the note does not repeat it")
}
