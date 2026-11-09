package trail

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrailSectionLetters(t *testing.T) {
	var letters string
	for _, s := range Sections {
		require.NoError(t, s.Validate())
		letters += s.Letter()
	}
	assert.Equal(t, "ABCD", letters, "labels are A refused, B advised, C unguarded, D next-not-taken")
	assert.Empty(t, Section("passed").Letter())
	assert.ErrorContains(t, Section("passed").Validate(), "refused, advised, unguarded, next-not-taken")
}

func TestTrailVerdictValidate(t *testing.T) {
	for _, v := range Verdicts {
		require.NoError(t, v.Validate())
	}
	assert.ErrorContains(t, Verdict("bad").Validate(), "should-deny, should-advise, wrong-deny, fine")
}
