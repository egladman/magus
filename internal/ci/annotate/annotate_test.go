package annotate

import (
	"bytes"
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
// unconditionally: off CI every verb succeeds and writes nothing.
func TestNopIsInertButUsable(t *testing.T) {
	t.Parallel()
	var n Nop
	assert.False(t, n.Active())
	assert.NoError(t, n.StartGroup(Group{Title: "x"}))
	assert.NoError(t, n.EndGroup("x"))
	assert.NoError(t, n.Annotate(Annotation{Message: "x"}))
	assert.Zero(t, n.Concurrency(), "no opinion off CI")
	assert.Equal(t, "::error::untouched", n.Quote("::error::untouched"),
		"with no provider there is nothing to neutralise")
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
	got := SanitizeID("a/b:c d\\e@f#g")
	for _, r := range got {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-'
		assert.True(t, ok, "character %q is not accepted in a section name", r)
	}
}

func TestDetectFallsBackToNop(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	RegisterOpener(nil)
	assert.IsType(t, Nop{}, Detect(io.Discard), "no provider means no annotations")
}

func TestDetectPrefersARegisteredOpener(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true") // a built-in would otherwise match
	t.Cleanup(func() { RegisterOpener(nil) })

	RegisterOpener(func(io.Writer) Annotator { return stubProvider{active: true} })
	assert.IsType(t, stubProvider{}, Detect(io.Discard),
		"a workspace that names a provider explicitly means it")
}

// TestDetectIgnoresAnInactiveOpener keeps a declared-but-dormant spell
// provider from suppressing the built-in that actually applies.
func TestDetectIgnoresAnInactiveOpener(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Cleanup(func() { RegisterOpener(nil) })

	RegisterOpener(func(io.Writer) Annotator { return stubProvider{active: false} })
	assert.IsType(t, &githubActions{}, Detect(io.Discard))
}

func TestDetectToleratesAnOpenerReturningNil(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
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
func (stubProvider) Concurrency() int          { return 0 }
func (stubProvider) Quote(text string) string  { return text }

var _ Annotator = stubProvider{}

func newGitHub(t *testing.T, buf *bytes.Buffer) Annotator {
	t.Helper()
	t.Setenv("GITHUB_ACTIONS", "true")
	return newGitHubActions(buf)
}

func TestGitHubActiveOnlyForTheDocumentedValue(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"", false},
		{"false", false},
		{"1", false}, // Actions sets exactly "true"; anything else is another host
	} {
		t.Run("GITHUB_ACTIONS="+tc.value, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", tc.value)
			assert.Equal(t, tc.want, (&githubActions{}).Active())
		})
	}
}

func TestGitHubCollapsedGroup(t *testing.T) {
	var buf bytes.Buffer
	g := newGitHub(t, &buf)
	require.NoError(t, g.StartGroup(Group{ID: "api", Title: "build api", Collapsed: true}))
	require.NoError(t, g.EndGroup("api"))
	assert.Equal(t, "::group::build api\n::endgroup::\n", buf.String())
}

// TestGitHubExpandedGroupIsNotFolded is the behaviour that matters most:
// Actions has no expanded section, and folding a failure would hide the
// one thing the reader needs. The group is skipped instead.
func TestGitHubExpandedGroupIsNotFolded(t *testing.T) {
	var buf bytes.Buffer
	g := newGitHub(t, &buf)
	require.NoError(t, g.StartGroup(Group{ID: "api", Title: "failed: api", Collapsed: false}))
	assert.Empty(t, buf.String(), "an expanded request must not become a fold")
}

func TestGitHubAnnotationCarriesLocationAndCode(t *testing.T) {
	var buf bytes.Buffer
	g := newGitHub(t, &buf)
	require.NoError(t, g.Annotate(Annotation{
		Level:   LevelError,
		Message: "undefined: Widget",
		Title:   "compile failed",
		Code:    "MGS1002",
		File:    "pkg/api/main.go",
		Line:    12,
		Col:     4,
	}))
	assert.Equal(t,
		"::error file=pkg/api/main.go,line=12,col=4,title=MGS1002 compile failed::undefined: Widget\n",
		buf.String())
}

func TestGitHubAnnotationOmitsUnsetFields(t *testing.T) {
	var buf bytes.Buffer
	g := newGitHub(t, &buf)
	require.NoError(t, g.Annotate(Annotation{Level: LevelWarning, Message: "heads up"}))
	assert.Equal(t, "::warning::heads up\n", buf.String())
}

// TestGitHubEscapesMultiLineMessages guards the case that loses the most
// information: a raw newline ends the workflow command, so the rest of a
// failure's cause would spill into the log as loose text.
func TestGitHubEscapesMultiLineMessages(t *testing.T) {
	var buf bytes.Buffer
	g := newGitHub(t, &buf)
	require.NoError(t, g.Annotate(Annotation{
		Level:   LevelError,
		Message: "compile failed:\nundefined: Widget",
	}))
	got := buf.String()
	assert.Equal(t, "::error::compile failed:%0Aundefined: Widget\n", got)
	assert.Equal(t, 1, bytes.Count([]byte(got), []byte("\n")),
		"exactly one real newline, the one ending the command")
}

// TestGitHubEscapesPropertySeparators keeps a title containing a comma or
// colon from being read as the start of another property.
func TestGitHubEscapesPropertySeparators(t *testing.T) {
	var buf bytes.Buffer
	g := newGitHub(t, &buf)
	require.NoError(t, g.Annotate(Annotation{Level: LevelNotice, Title: "a,b:c", Message: "m"}))
	assert.Contains(t, buf.String(), "title=a%2Cb%3Ac")
}

func TestGitHubConcurrency(t *testing.T) {
	t.Run("hosted runners are small", func(t *testing.T) {
		t.Setenv("RUNNER_ENVIRONMENT", "github-hosted")
		assert.Equal(t, 4, (&githubActions{}).Concurrency())
	})
	t.Run("self-hosted is the user's own hardware", func(t *testing.T) {
		t.Setenv("RUNNER_ENVIRONMENT", "self-hosted")
		assert.Zero(t, (&githubActions{}).Concurrency(), "magus offers no opinion")
	})
}

// TestGitHubQuoteDefusesInjectedCommands is a security property, not
// cosmetics: magus replays captured subprocess output, so a dependency
// printing a workflow command could otherwise forge annotations or mask
// values in someone else's CI.
func TestGitHubQuoteDefusesInjectedCommands(t *testing.T) {
	t.Parallel()
	g := &githubActions{}

	assert.Equal(t, ":::error::forged", g.Quote("::error::forged"))
	assert.Equal(t, "  :::add-mask::secret", g.Quote("  ::add-mask::secret"),
		"indentation is preserved so the line still reads naturally")

	multi := g.Quote("ok\n::error::forged\nstill ok")
	assert.Equal(t, "ok\n:::error::forged\nstill ok", multi)
}

func TestGitHubQuoteLeavesOrdinaryTextAlone(t *testing.T) {
	t.Parallel()
	g := &githubActions{}
	assert.Equal(t, "all good", g.Quote("all good"))
	assert.Equal(t, "http://x/y", g.Quote("http://x/y"), "a mid-line colon pair is not a command")
	assert.Equal(t, "see foo::bar", g.Quote("see foo::bar"))
}
