package annotate

import (
	"io"
	"testing"

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

func TestSanitizeID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"libs/textsearch", "libs-textsearch"},
		{"magus-fail-api build", "magus-fail-api-build"},
		{"keeps_dots.and-dashes", "keeps_dots.and-dashes"},
		{"collapses///runs", "collapses-runs"},
		{"/leading/and/trailing/", "leading-and-trailing"},
		{"", ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, SanitizeID(tc.in))
		})
	}
}

// TestSanitizeIDProducesOnlyAcceptedCharacters pins the constraint that
// motivated the helper: GitLab rejects a section name containing anything
// outside this set, so a project path cannot key a section unsanitised.
func TestSanitizeIDProducesOnlyAcceptedCharacters(t *testing.T) {
	t.Parallel()
	for _, r := range SanitizeID("a/b:c d\\e@f#g") {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-'
		assert.True(t, ok, "character %q is not accepted in a section name", r)
	}
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
