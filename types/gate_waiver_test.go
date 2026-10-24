package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGateWaiverCoversItsCommitAlone(t *testing.T) {
	w := GateWaiver{Commit: "abc1234def5678", Reason: "CI is down"}
	for commit, want := range map[string]bool{
		"abc1234def5678": true,
		"abc1234":        true,
		"abc123":         false,
		"abc1235":        false,
		"":               false,
		"fff1234def5678": false,
	} {
		assert.Equal(t, want, w.Covers(commit), "%q", commit)
	}
	w.Revoked = true
	assert.False(t, w.Covers("abc1234def5678"), "a revoked waiver covers nothing")
	assert.Equal(t, "abc1234def5678", w.Key())
}
