package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFeedbackSectionLetters(t *testing.T) {
	var letters string
	for _, s := range FeedbackSections {
		assert.NoError(t, s.Validate())
		letters += s.Letter()
	}
	assert.Equal(t, "ABCD", letters, "labels are A refused, B advised, C unguarded, D next-not-taken")
	assert.Empty(t, FeedbackSection("passed").Letter())
	assert.ErrorContains(t, FeedbackSection("passed").Validate(), "refused, advised, unguarded, next-not-taken")
}

func TestFeedbackVerdictValidate(t *testing.T) {
	for _, v := range FeedbackVerdicts {
		assert.NoError(t, v.Validate())
	}
	assert.ErrorContains(t, FeedbackVerdict("bad").Validate(), "should-deny, should-advise, wrong-deny, fine")
}
