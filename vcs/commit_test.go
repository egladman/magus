package vcs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const nul = "\x00"

func TestParseCommit(t *testing.T) {
	// Fields in fixed order: id, short, authorName, authorEmail, date, parents, message.
	raw := "abc123def" + nul + "abc123d" + nul + "Alice" + nul + "alice@example.com" + nul +
		"2026-06-04T12:34:56+00:00" + nul + "p1 p2" + nul + "subject line\n\nbody text"
	c := parseCommit(raw)
	assert.Equal(t, "abc123def", c.ID)
	assert.Equal(t, "abc123d", c.Short)
	assert.Equal(t, "Alice", c.Author.Name)
	assert.Equal(t, "alice@example.com", c.Author.Email)
	assert.Equal(t, "subject line", c.Subject)
	assert.Equal(t, "body text", c.Body)
	assert.Equal(t, []string{"p1", "p2"}, c.Parents)
	assert.False(t, c.Date.IsZero())
	assert.Equal(t, 2026, c.Date.Year())
}

func TestParseCommitEmptyAndShort(t *testing.T) {
	// Empty driver output → empty ID (FindCommit turns this into an error).
	assert.Empty(t, parseCommit("").ID, "empty input ID, want empty")
	// A short record (fewer than numCommitFields) must not panic.
	c := parseCommit("onlyid")
	assert.Equal(t, "onlyid", c.ID)
	assert.Empty(t, c.Subject)
}

func TestParents(t *testing.T) {
	null40 := "0000000000000000000000000000000000000000"
	assert.Nil(t, parents(""))
	assert.Equal(t, []string{"a", "b", "c"}, parents("a b c"))
	assert.Equal(t, []string{"a", "b"}, parents("a  b")) // collapse runs of whitespace
	assert.Equal(t, []string{"a"}, parents("a "+null40)) // drop the null p2node sentinel
	assert.Nil(t, parents(null40))                       // all-null → empty
}

func TestSplitMessage(t *testing.T) {
	assertSplit := func(in, wantSubject, wantBody string) {
		t.Helper()
		s, b := splitMessage(in)
		assert.Equalf(t, wantSubject, s, "splitMessage(%q) subject", in)
		assert.Equalf(t, wantBody, b, "splitMessage(%q) body", in)
	}
	assertSplit("", "", "")
	assertSplit("just a subject", "just a subject", "")
	assertSplit("subject\n\nbody", "subject", "body")
	assertSplit("subject\nbody no blank", "subject", "body no blank")
	assertSplit("  trimmed  ", "trimmed", "")
	assertSplit("sub\r\nbody", "sub", "body") // CRLF: the \r is trimmed off the subject
}

func TestParseWhen(t *testing.T) {
	// RFC3339 colon offset (git %cI, hg, jj %:z).
	assert.False(t, parseWhen("2026-06-04T12:34:56+00:00").IsZero())
	// Z.
	assert.False(t, parseWhen("2026-06-04T12:34:56Z").IsZero())
	// No-colon offset — the defensive fallback.
	assert.False(t, parseWhen("2026-06-04T12:34:56+0000").IsZero())
	assert.True(t, parseWhen("").IsZero())
	assert.True(t, parseWhen("not a date").IsZero())
}
