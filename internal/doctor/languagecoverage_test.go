package doctor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// TestLanguageCoverageRespectsNoLanguage covers both halves of the opt-out: a declared reason
// exempts a spell-less project, and a project that never declared one is still reported. The
// exempt count is in the OK message on purpose, so an exemption stays visible rather than
// disappearing into a green check.
func TestLanguageCoverageRespectsNoLanguage(t *testing.T) {
	var r *runner

	t.Run("a declared reason exempts", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{
			{Path: "evals", NoLanguage: "polyglot harness; no single pack describes it"},
			{Path: "api", Spell: "go"},
		})
		assert.Equal(t, types.DoctorCheck{
			Name:    "language coverage",
			Status:  types.DoctorOK,
			Message: "every project matched a spell or declared no_language (1 exempt)",
		}, got)
	})

	t.Run("an undeclared gap is still reported", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{
			{Path: "evals", NoLanguage: "polyglot harness; no single pack describes it"},
			{Path: "forgot-the-import"},
		})
		assert.Equal(t, types.DoctorAdvice, got.Status)
		assert.Equal(t, []string{"forgot-the-import"}, got.Details)
	})
}
