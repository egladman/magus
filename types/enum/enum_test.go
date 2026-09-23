package enum

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type color string

var colors = Set[color]{"red", "green"}

func TestSet(t *testing.T) {
	assert.True(t, colors.Valid("red"))
	assert.True(t, colors.Valid(""), "the zero value means unset")
	assert.False(t, colors.Valid("blue"))

	got := colors.Strings()
	assert.Equal(t, []string{"red", "green"}, got)
	got[0] = "mutated"
	assert.Equal(t, []string{"red", "green"}, colors.Strings(), "a caller's copy cannot reach the set")
}

func TestString(t *testing.T) {
	assert.Equal(t, "unset", String(color("")))
	assert.Equal(t, "red", String(color("red")))
}
