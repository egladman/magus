package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrailSectionLetters(t *testing.T) {
	var letters string
	for _, s := range TrailSections {
		require.NoError(t, s.Validate())
		letters += s.Letter()
	}
	assert.Equal(t, "ABCD", letters, "labels are A refused, B advised, C unguarded, D next-not-taken")
	assert.Empty(t, TrailSection("passed").Letter())
	assert.ErrorContains(t, TrailSection("passed").Validate(), "refused, advised, unguarded, next-not-taken")
}

func TestTrailVerdictValidate(t *testing.T) {
	for _, v := range TrailVerdicts {
		require.NoError(t, v.Validate())
	}
	assert.ErrorContains(t, TrailVerdict("bad").Validate(), "should-deny, should-advise, wrong-deny, fine")
}
