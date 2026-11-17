package impact

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// TestImpactCountsFindingsNotSections pins the headline against the retraction shape every
// real run produces: eleven advisors publish, one has something to say, and ten publish an
// empty body to withdraw whatever they said last time.
func TestImpactCountsFindingsNotSections(t *testing.T) {
	lines := advisorLines([]review.AdviceSection{
		{Name: "unclaimed", Title: "Files no project claims"},
		{Name: "conformance", Title: "A target name diverges", Body: "rename it\n"},
		{Name: "skip-cache", Title: "A target opted out of the cache"},
	}, nil, nil)

	assert.Equal(t, []string{
		"ADVISORS: 1 finding",
		"      A target name diverges",
		"        rename it",
		"      2 ran and found nothing: unclaimed, skip-cache",
	}, lines)
}

// TestImpactBaseSeparatesOldFromAbsent pins the distinction the line exists for.
//
// A base nobody fetched and a base that is merely stale produce the same silent advisors, and
// only one of them is a finding about the code. Collapsing them would make an unfetched clone
// read as a clean review.
func TestImpactBaseSeparatesOldFromAbsent(t *testing.T) {
	stale := advisorBaseLine(&advisorBase{
		Ref: "origin/main",
		Tip: time.Now().Add(-9 * 24 * time.Hour).Format(time.RFC3339),
	})
	assert.Contains(t, stale, "tip 9 days old")
	assert.Contains(t, stale, "`git fetch origin main`")

	absent := advisorBaseLine(&advisorBase{Ref: "origin/main"})
	assert.Contains(t, absent, "is not in this clone")
	assert.NotContains(t, absent, "tip ")

	// Nil is neither: a backend that cannot date a revision has not measured the base, and
	// inventing "fresh" for it is the one answer that would mislead.
	assert.Empty(t, advisorBaseLine(nil))
}

// TestImpactBaseQualifiesAnEmptyAdvisorSet is the case the caveat matters most in: no
// findings against a week-old base is not the same news as no findings against today's.
func TestImpactBaseQualifiesAnEmptyAdvisorSet(t *testing.T) {
	lines := advisorLines(nil, nil, &advisorBase{Ref: "origin/main"})
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "origin/main")
	assert.Equal(t, "ADVISORS: nothing to report", lines[1])
}

// TestImpactAgeKeepsOneUnit pins the rounding. A reader is deciding whether to fetch, and
// the order of magnitude is the whole decision.
func TestImpactAgeKeepsOneUnit(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "seconds"},
		{time.Minute, "1 minute"},
		{90 * time.Minute, "1 hour"},
		{25 * time.Hour, "1 day"},
		{50 * time.Hour, "2 days"},
	} {
		assert.Equal(t, tc.want, ageText(tc.d), tc.d.String())
	}
}
