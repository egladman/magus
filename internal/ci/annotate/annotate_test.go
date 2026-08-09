package annotate

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevelString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "notice", LevelNotice.String())
	assert.Equal(t, "warning", LevelWarning.String())
	assert.Equal(t, "error", LevelError.String())
}

// TestNopIsInertButUsable is what lets core call the annotator
// unconditionally: with no provider wired, every verb succeeds and does
// nothing.
func TestNopIsInertButUsable(t *testing.T) {
	t.Parallel()
	var n Nop
	assert.False(t, n.Active())
	assert.NoError(t, n.StartGroup(Group{Title: "x"}))
	assert.NoError(t, n.EndGroup("x"))
	assert.NoError(t, n.Annotate(Annotation{Message: "x"}))
	assert.Equal(t, "::error::untouched", n.Quote("::error::untouched"),
		"with no provider there is no syntax to neutralise")
}

// TestDetectIsNopWithoutAProvider is the shipped default: magus carries no
// CI syntax of its own, so a workspace that names no provider gets none.
func TestDetectIsNopWithoutAProvider(t *testing.T) {
	RegisterOpener(nil)
	assert.IsType(t, Nop{}, Detect(io.Discard))
}

func TestDetectUsesAnActiveProvider(t *testing.T) {
	t.Cleanup(func() { RegisterOpener(nil) })
	RegisterOpener(func(io.Writer) Annotator { return stubProvider{active: true} })
	assert.IsType(t, stubProvider{}, Detect(io.Discard))
}

// TestDetectIgnoresAnInactiveProvider covers the common wiring: a spell is
// declared unconditionally in the magusfile and reports itself inactive
// off its own CI system, which must cost nothing.
func TestDetectIgnoresAnInactiveProvider(t *testing.T) {
	t.Cleanup(func() { RegisterOpener(nil) })
	RegisterOpener(func(io.Writer) Annotator { return stubProvider{active: false} })
	assert.IsType(t, Nop{}, Detect(io.Discard))
}

func TestDetectToleratesAProviderReturningNil(t *testing.T) {
	t.Cleanup(func() { RegisterOpener(nil) })
	RegisterOpener(func(io.Writer) Annotator { return nil })
	require.NotPanics(t, func() { _ = Detect(io.Discard) })
	assert.IsType(t, Nop{}, Detect(io.Discard))
}

// stubProvider stands in for a spell-backed provider.
type stubProvider struct{ active bool }

func (s stubProvider) Active() bool            { return s.active }
func (stubProvider) StartGroup(Group) error    { return nil }
func (stubProvider) EndGroup(string) error     { return nil }
func (stubProvider) Annotate(Annotation) error { return nil }
func (stubProvider) Quote(text string) string  { return text }

var _ Annotator = stubProvider{}

// TestQuoteWithDefusesInjectedCommands is a security property, not
// cosmetics: magus replays captured subprocess output, so a dependency
// printing a workflow command could otherwise forge annotations or close
// a section magus opened.
func TestQuoteWithDefusesInjectedCommands(t *testing.T) {
	t.Parallel()
	gh := []string{"::"}

	assert.Equal(t, ":error::forged", QuoteWith("::error::forged", gh),
		"dropping the prefix's first character leaves readable text that is no longer a command")
	assert.Equal(t, "  :add-mask::secret", QuoteWith("  ::add-mask::secret", gh),
		"indentation is preserved so the line still reads as it was written")
	assert.Equal(t, "ok\n:error::forged\nstill ok",
		QuoteWith("ok\n::error::forged\nstill ok", gh))
}

func TestQuoteWithLeavesOrdinaryTextAlone(t *testing.T) {
	t.Parallel()
	gh := []string{"::"}
	assert.Equal(t, "all good", QuoteWith("all good", gh))
	assert.Equal(t, "http://x/y", QuoteWith("http://x/y", gh),
		"a mid-line colon pair does not start a command")
	assert.Equal(t, "see foo::bar", QuoteWith("see foo::bar", gh))
}

// TestQuoteWithHandlesAMultiBytePrefix covers GitLab, whose marker is
// introduced by an escape byte: the first character must be dropped
// whole, never split.
func TestQuoteWithHandlesAMultiBytePrefix(t *testing.T) {
	t.Parallel()
	gl := []string{"\x1b[0Ksection_"}
	assert.Equal(t, "[0Ksection_end:1:x",
		QuoteWith("\x1b[0Ksection_end:1:x", gl),
		"the escape is dropped, so GitLab reads the rest as ordinary text")
}

func TestQuoteWithNoPrefixesIsIdentity(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "::error::x", QuoteWith("::error::x", nil),
		"a provider that declares no command syntax quotes nothing")
	assert.Equal(t, "::error::x", QuoteWith("::error::x", []string{""}),
		"an empty prefix matches nothing rather than everything")
}

// TestSanitizeStripsControlCharacters is a security property. An
// annotation's message is a failing process's own output, so it can carry
// escape sequences that would re-take control of the terminal or the job
// log when a provider echoes it.
func TestSanitizeStripsControlCharacters(t *testing.T) {
	t.Parallel()
	got := Sanitize(Annotation{Message: "before\x1b[2Jafter\x00\x07"})
	assert.Equal(t, "before[2Jafter", got.Message,
		"escape and other control bytes are removed, visible text survives")
	assert.NotContains(t, got.Message, "\x1b")
}

func TestSanitizeKeepsTabAndNewline(t *testing.T) {
	t.Parallel()
	got := Sanitize(Annotation{Message: "line one\n\tindented"})
	assert.Equal(t, "line one\n\tindented", got.Message,
		"compiler output is full of tabs, and providers encode newlines themselves")
}

// TestSanitizeBoundsMessageLength keeps a process that printed megabytes
// from making a provider's string handling a denial-of-service vector.
func TestSanitizeBoundsMessageLength(t *testing.T) {
	t.Parallel()
	got := Sanitize(Annotation{Message: strings.Repeat("x", maxMessageLen*2)})
	assert.LessOrEqual(t, len(got.Message), maxMessageLen+len(" ... (truncated)"))
	assert.Contains(t, got.Message, "(truncated)", "a bounded message says so")
}

func TestSanitizeBoundsShortFields(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("y", maxFieldLen*2)
	got := Sanitize(Annotation{Title: long, Code: long, File: long})
	for _, f := range []string{got.Title, got.Code, got.File} {
		assert.LessOrEqual(t, len(f), maxFieldLen+len(" ... (truncated)"))
	}
}

func TestSanitizeNeverSplitsARune(t *testing.T) {
	t.Parallel()
	got := Sanitize(Annotation{Title: strings.Repeat("日", maxFieldLen)})
	assert.True(t, utf8.ValidString(got.Title), "truncation must land on a rune boundary")
}

func TestSanitizeGroupBoundsItsFields(t *testing.T) {
	t.Parallel()
	got := SanitizeGroup(Group{ID: strings.Repeat("a", maxFieldLen*2), Title: "t\x1b[2J"})
	assert.LessOrEqual(t, len(got.ID), maxFieldLen+len(" ... (truncated)"))
	assert.NotContains(t, got.Title, "\x1b")
}

// TestClampPrefixesRejectsAMatchEverythingPrefix is the sharpest of these:
// an empty prefix would make QuoteWith rewrite the first character of
// every line of replayed output.
func TestClampPrefixesRejectsAMatchEverythingPrefix(t *testing.T) {
	t.Parallel()
	assert.Empty(t, ClampPrefixes([]string{""}))
	assert.Equal(t, []string{"::"}, ClampPrefixes([]string{"", "::", ""}))
}

// TestClampPrefixesBoundsTheList keeps a provider from making magus match
// an unbounded list against every line; QuoteWith is O(lines x prefixes).
func TestClampPrefixesBoundsTheList(t *testing.T) {
	t.Parallel()
	many := make([]string, 100)
	for i := range many {
		many[i] = "p"
	}
	assert.Len(t, ClampPrefixes(many), maxQuotePrefixes)
}

func TestClampPrefixesDropsOverlongEntries(t *testing.T) {
	t.Parallel()
	assert.Empty(t, ClampPrefixes([]string{strings.Repeat("x", maxFieldLen+1)}))
}

// An unknown level must not render as the quietest token. This function decides how
// loudly a finding is reported, so a level added upstream, or an int a spell handed in,
// silently becoming "notice" is a downgrade nobody asked for and nobody would see.
func TestLevelStringDoesNotDowngradeAnUnknownLevel(t *testing.T) {
	for _, l := range []Level{Level(99), Level(-1)} {
		if got := l.String(); got == "notice" {
			t.Errorf("Level(%d).String() = %q; an unenumerated level must not read as the least severe one", l, got)
		}
	}
	// The three real levels still render as their provider tokens.
	if got := LevelNotice.String(); got != "notice" {
		t.Errorf("LevelNotice.String() = %q, want notice", got)
	}
}
