package guard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// TestWithinBudgetReturnsOnTime pins the hook's latency bound: a lookup that cannot be
// cancelled still hands back a non-definitive answer at the budget, not when it finishes.
func TestWithinBudgetReturnsOnTime(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	v, ok := withinBudget(20*time.Millisecond, func() int { <-release; return 1 })
	assert.False(t, ok)
	assert.Zero(t, v)
	assert.Less(t, time.Since(start), time.Second)

	v, ok = withinBudget(time.Second, func() int { return 7 })
	assert.True(t, ok)
	assert.Equal(t, 7, v)
}

func TestIndexCauseNamesWhyAndFix(t *testing.T) {
	env := testHookEnv()
	env.FindRoot = func(string) (string, error) { return "/repo", nil }
	env.IndexCause = func(_ context.Context, root string) types.KnowledgeIndexCause {
		return types.KnowledgeIndexCause{Why: "it was refused in " + root, Hint: "run the sync"}
	}

	assert.Equal(t, "Why: it was refused in /repo. Hint: run the sync.", env.newLookups("").indexCause())
}

func TestIndexCauseIsEmptyOutsideAWorkspace(t *testing.T) {
	assert.Empty(t, testHookEnv().newLookups("").indexCause())
}
