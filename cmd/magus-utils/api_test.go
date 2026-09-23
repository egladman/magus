package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunAPIEmitsASortedLock: the .lock's whole value is that its diff reads as
// added/removed API elements, which only holds while the lines stay sorted.
func TestRunAPIEmitsASortedLock(t *testing.T) {
	got := readGenerated(t, runAPI, func(out string) []string { return []string{"-out", out} })

	require.True(t, strings.HasSuffix(got, "\n"))
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	require.NotEmpty(t, lines)
	assert.True(t, slices.IsSorted(lines), "the lock must be sorted or its diff is unreadable")
	assert.NotContains(t, lines, "", "a blank line is an element with no name")
}

func TestRunAPIRejectsAnUnknownFlag(t *testing.T) {
	assert.Error(t, runAPI([]string{"-nosuchflag"}))
}
