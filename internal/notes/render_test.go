package notes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFirstNonEmptyTreatsBlankAsEmpty(t *testing.T) {
	assert.Equal(t, "a", FirstNonEmpty("a", "b"))
	assert.Equal(t, "b", FirstNonEmpty("", "b"))
	assert.Equal(t, "b", FirstNonEmpty("   \n", "b"))
	assert.Equal(t, "", FirstNonEmpty("", ""))
}
