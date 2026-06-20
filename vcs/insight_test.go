package vcs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseChangesByCommit verifies the NUL-delimited `git log --name-only` parse:
// a NUL line opens a commit (hash, author, date); following non-empty lines are files.
func TestParseChangesByCommit(t *testing.T) {
	out := "\x00abc123\x00Ada\x002026-06-20T10:00:00Z\n\napi/main.go\napi/util.go\n" +
		"\x00def456\x00Babbage\x002026-06-19T09:00:00Z\n\nweb/app.ts\n"

	got := parseChangesByCommit(out)
	require.Len(t, got, 2)

	assert.Equal(t, "abc123", got[0].ID)
	assert.Equal(t, "Ada", got[0].Author)
	assert.Equal(t, time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), got[0].Date.UTC())
	assert.Equal(t, []string{"api/main.go", "api/util.go"}, got[0].Files)

	assert.Equal(t, "def456", got[1].ID)
	assert.Equal(t, "Babbage", got[1].Author)
	assert.Equal(t, []string{"web/app.ts"}, got[1].Files)
}

// TestParseChangesByCommitEmpty covers a commit that touched no files and a bad date.
func TestParseChangesByCommitEmpty(t *testing.T) {
	got := parseChangesByCommit("\x00abc123\x00Ada\x00not-a-date\n\n")
	require.Len(t, got, 1)
	assert.Equal(t, "abc123", got[0].ID)
	assert.True(t, got[0].Date.IsZero(), "unparseable date is zero, not an error")
	assert.Empty(t, got[0].Files)
}
