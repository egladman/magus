package settle

import (
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobsWriting pins who settling by claims asks about a path: every job writing it, a
// whole-file or glob writer included so it can refuse the settle, but only a path some job
// claims by declaration is asked about at all.
func TestJobsWriting(t *testing.T) {
	rows := []job.Job{
		{ID: "a", Paths: job.Paths{Write: []string{"a.go#A"}}},
		{ID: "glob", Paths: job.Paths{Write: []string{"**/*.go"}}},
		{ID: "other", Paths: job.Paths{Write: []string{"b.go#B", "docs/**"}}},
	}
	writers, claimed := jobsWriting(rows, "a.go")
	assert.True(t, claimed)
	require.Len(t, writers, 2)
	assert.Equal(t, []string{"a", "glob"}, []string{writers[0].ID, writers[1].ID})

	writers, claimed = jobsWriting(rows, "c.go")
	assert.False(t, claimed, "only the glob writes c.go, and it claims no declaration")
	assert.Len(t, writers, 1)
}
