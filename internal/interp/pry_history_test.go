package interp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHistory opens a fresh, uncapped history in a temp dir and appends lines,
// so PrintHistory tests can drive a real *History without touching user state.
func newHistory(t *testing.T, lines ...string) *History {
	t.Helper()
	h, err := Open(filepath.Join(t.TempDir(), "hist"), 0)
	require.NoError(t, err)
	for _, l := range lines {
		h.Append(l)
	}
	return h
}

func TestPrintHistory_NilHistory(t *testing.T) {
	var sb strings.Builder
	PrintHistory(&sb, nil, "")
	assert.Contains(t, sb.String(), "history unavailable")
}

func TestPrintHistory_DefaultListing(t *testing.T) {
	h := newHistory(t, "one", "two", "three")
	var sb strings.Builder
	PrintHistory(&sb, h, "")
	out := sb.String()
	// Numbering counts down from most-recent-distance; every line is present.
	assert.Contains(t, out, "one")
	assert.Contains(t, out, "two")
	assert.Contains(t, out, "three")
}

func TestPrintHistory_LimitLastN(t *testing.T) {
	h := newHistory(t, "a", "b", "c", "d")
	var sb strings.Builder
	PrintHistory(&sb, h, "2")
	out := sb.String()
	// Only the last two lines are shown.
	assert.NotContains(t, out, "a")
	assert.NotContains(t, out, "b")
	assert.Contains(t, out, "c")
	assert.Contains(t, out, "d")
}

func TestPrintHistory_RecallByBang(t *testing.T) {
	h := newHistory(t, "first", "second", "third")
	var sb strings.Builder
	PrintHistory(&sb, h, "!1")
	assert.Equal(t, "third\n", sb.String())
}

func TestPrintHistory_RecallOutOfRange(t *testing.T) {
	h := newHistory(t, "only")
	var sb strings.Builder
	PrintHistory(&sb, h, "!5")
	assert.Contains(t, sb.String(), "out of range")
}

func TestPrintHistory_BangInvalidArg(t *testing.T) {
	h := newHistory(t, "x")
	var sb strings.Builder
	PrintHistory(&sb, h, "!notanumber")
	assert.Contains(t, sb.String(), "usage: .history")
}

func TestPrintHistory_BangZeroIsUsage(t *testing.T) {
	h := newHistory(t, "x")
	var sb strings.Builder
	PrintHistory(&sb, h, "!0")
	assert.Contains(t, sb.String(), "usage: .history")
}

func TestAppendAndLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")

	h, err := Open(path, 5)
	require.NoError(t, err)

	h.Append("one")
	h.Append("two")
	h.Append("two") // duplicate of previous → skipped
	h.Append("three")

	lines := h.Lines()
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"one", "two", "three"}, lines)
}

func TestRecall(t *testing.T) {
	dir := t.TempDir()
	h, err := Open(filepath.Join(dir, "hist"), 0)
	require.NoError(t, err)
	h.Append("first")
	h.Append("second")
	h.Append("third")

	assert.Equal(t, "third", h.Recall(1))
	assert.Equal(t, "first", h.Recall(3))
	assert.Empty(t, h.Recall(99))
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")

	h, _ := Open(path, 0)
	h.Append("alpha")
	h.Append("beta")

	// New History opened against the same file should pick the lines back up.
	h2, err := Open(path, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta"}, h2.Lines())
}

func TestCapOverflowTrims(t *testing.T) {
	dir := t.TempDir()
	h, _ := Open(filepath.Join(dir, "hist"), 3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		h.Append(s)
	}
	assert.Equal(t, []string{"c", "d", "e"}, h.Lines())
}
