package impact

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImpactEvidenceLines(t *testing.T) {
	t.Run("one line per subject, and no answers", func(t *testing.T) {
		lines := evidenceLines([]trail.Consultation{
			{Verb: "explain", Subject: "internal/trail", Count: 2},
			{Verb: "refs", Subject: "AppendAgentCommand", Count: 1},
		}, trail.ConsultGapNone)
		assert.Contains(t, lines[0], "2 questions the authors asked magus")
		assert.Equal(t, "      explain internal/trail (x2)", lines[1])
		// A count of one is the common case, and printing "(x1)" on every line is noise.
		assert.Equal(t, "      refs AppendAgentCommand", lines[2])
	})

	t.Run("a verb asked with no subject leaves no trailing space", func(t *testing.T) {
		lines := evidenceLines([]trail.Consultation{{Verb: "query", Count: 1}}, trail.ConsultGapNone)
		assert.Equal(t, "      query", lines[1])
	})

	t.Run("the list is capped", func(t *testing.T) {
		var hits []trail.Consultation
		for i := range listCap + 4 {
			hits = append(hits, trail.Consultation{Verb: "query", Subject: fmt.Sprintf("kind=op %d", i), Count: 1})
		}
		lines := evidenceLines(hits, trail.ConsultGapNone)
		assert.Equal(t, "      and 4 more", lines[len(lines)-1])
	})
}

// TestImpactEvidenceGapsReadDifferently is the half that matters, for the reason the whole
// empty-forms test exists: three of these four silences are facts about the RECORD, and one
// sentence covering all four reports a fresh clone as work nobody researched.
func TestImpactEvidenceGapsReadDifferently(t *testing.T) {
	seen := map[string]trail.ConsultGap{}
	for _, gap := range []trail.ConsultGap{
		trail.ConsultGapUnobserved,
		trail.ConsultGapUnleased,
		trail.ConsultGapUnmatched,
		trail.ConsultGapNoQuestions,
	} {
		lines := evidenceLines(nil, gap)
		require.NotEmpty(t, lines, gap)
		assert.True(t, strings.HasPrefix(lines[0], "EVIDENCE: "), gap)
		prior, dup := seen[lines[0]]
		assert.False(t, dup, "%s and %s render the same headline", gap, prior)
		seen[lines[0]] = gap
	}

	// The distinctions each headline has to carry, named rather than left to the count above.
	assert.Contains(t, evidenceLines(nil, trail.ConsultGapUnobserved)[1], "per checkout")
	assert.Contains(t, evidenceLines(nil, trail.ConsultGapUnleased)[1], "magus.lease")
	assert.Contains(t, evidenceLines(nil, trail.ConsultGapUnmatched)[1], "another checkout")
	// The one gap that is a fact about the change says so without hedging it.
	assert.Equal(t, []string{"EVIDENCE: the authors of this changeset ran no magus read verb"},
		evidenceLines(nil, trail.ConsultGapNoQuestions))
}
