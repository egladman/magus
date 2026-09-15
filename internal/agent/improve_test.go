package agent

import (
	"testing"

	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
)

func TestImprovementCandidateRemainsAProposal(t *testing.T) {
	candidate := improvementCandidateFor(trail.GuardFeedback{
		Rule: "raw-tool", Surface: "shell.command", Denied: 3, Sessions: 1,
		FollowedSessions: 1, Evidence: []string{"test-host:session-1 (3 denials)"},
	})
	assert.Equal(t, destinationHarness, candidate.Destination)
	assert.Equal(t, confidenceHigh, candidate.Confidence)
	assert.Contains(t, improveDefinition, "cannot prove execution or success")
}
