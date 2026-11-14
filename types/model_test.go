package types

import (
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An outcome recorded before attempts were numbered reads as a first attempt, so the history
// it sits in needs no rewrite, and a retryable ending writes no retry member.
func TestJobOutcomeOmitsAnUnnumberedAttemptAndARetryableEnding(t *testing.T) {
	t.Parallel()

	members := func(o JobOutcome) map[string]any {
		raw, err := json.Marshal(o)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		return m
	}
	first := members(JobOutcome{Job: "a", Kind: JobKindCheck, State: StatePass})
	assert.NotContains(t, first, "attempt")
	assert.NotContains(t, first, "retry")

	again := members(JobOutcome{Job: "a", Kind: JobKindCheck, State: StateFail, Attempt: 2, Retry: RetryAfterWidening})
	assert.Equal(t, float64(2), again["attempt"])
	assert.Equal(t, "widening", again["retry"])
}

func TestJobKindVocabularyCloses(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"check", "symbol", "paths", "script", "prose"}, JobKind("").Values())
}
