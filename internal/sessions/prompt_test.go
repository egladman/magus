package sessions

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type PromptSuite struct{ suite.Suite }

func TestPrompt(t *testing.T) { suite.Run(t, new(PromptSuite)) }

func (s *PromptSuite) TestPromptCacheForCheckoutReadsThisCheckoutsLog() {
	t := s.T()
	cacheDir := t.TempDir()

	assert.Empty(t, PromptCacheForCheckout(t.Context(), cacheDir, time.Now()).Providers,
		"a checkout no agent has run against has no activity to clock")

	recorder.AppendAgentCommand(withSecrets(t.Context()), cacheDir, recorder.AgentCommand{Session: "s1", Tool: "shell.command", Command: "ls", Decision: verdict.Defer})

	clock := PromptCacheForCheckout(t.Context(), cacheDir, time.Now())
	assert.Equal(t, "s1", clock.Session)
	assert.Len(t, clock.Providers, len(PromptCacheProviders))
}

// Every figure in the table was read off a vendor page by a person, so the page is the
// only thing that makes a row re-checkable when the vendor changes it.
func (s *PromptSuite) TestPromptCacheProvidersCiteASource() {
	t := s.T()
	for _, p := range PromptCacheProviders {
		assert.NotEmpty(t, p.Provider, "a provider row with no name")
		assert.Truef(t, strings.HasPrefix(p.Source, "https://"), "%s: source %q is not a URL", p.Provider, p.Source)
		assert.Truef(t, len(p.Windows) > 0 || p.Note != "",
			"%s: a row with neither a window nor a note says nothing, and reads as a window of zero", p.Provider)
		for _, w := range p.Windows {
			assert.NotEmptyf(t, w.Window, "%s: an unnamed window", p.Provider)
			assert.Positivef(t, w.Min, "%s/%s: a window of zero", p.Provider, w.Window)
			assert.GreaterOrEqualf(t, w.Max, w.Min, "%s/%s: published bounds are inverted", p.Provider, w.Window)
		}
	}
}

func (s *PromptSuite) TestPromptCacheResolvesEveryWindow() {
	t := s.T()
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	clock := promptCacheAt("s1", last, last.Add(6*time.Minute))

	require.Len(t, clock.Providers, len(PromptCacheProviders))
	assert.Equal(t, "6m", clock.SinceText())
	assert.Equal(t, PromptCacheClock{
		Session:   "s1", // the clock is a fact about the session it was computed from
		LastMs:    last.UnixMilli(),
		SinceMs:   (6 * time.Minute).Milliseconds(),
		Providers: clock.Providers, // asserted below
	}, clock)

	anthropic := clock.Providers[0]
	require.Len(t, anthropic.Windows, 2)
	assert.True(t, anthropic.Windows[0].Closed, "the 5m window is behind a 6m gap")
	assert.False(t, anthropic.Windows[1].Closed)
	assert.Equal(t, last.Add(5*time.Minute).UnixMilli(), anthropic.Windows[0].ClosesByMs)

	gemini := clock.Providers[len(clock.Providers)-1]
	assert.Empty(t, gemini.Windows)
	assert.NotEmpty(t, gemini.Note)
}

// A session nothing has observed has no age, and the clock says so by being empty
// rather than by dating every window to the epoch.
func (s *PromptSuite) TestPromptCacheWithoutActivity() {
	t := s.T()
	assert.Empty(t, promptCacheAt("s1", time.Time{}, time.Now()).Providers)
}

// describedAt is how w reads at after a session last active at a fixed time.
func describedAt(w PromptCacheWindow, at time.Duration) string {
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := last.Add(at)
	return w.statusAt(last, now).Describe(now)
}

func (s *PromptSuite) TestPromptCacheWindowDescribeOfAnExactWindow() {
	exact := PromptCacheWindow{Window: "default", Min: 5 * time.Minute, Max: 5 * time.Minute}
	s.Equal("closes in 4m", describedAt(exact, 30*time.Second))
	s.Equal("closed 15m ago", describedAt(exact, 20*time.Minute))
}

func (s *PromptSuite) TestPromptCacheWindowDescribeOfARange() {
	span := PromptCacheWindow{Window: "in-memory retention", Min: 5 * time.Minute, Max: time.Hour}
	s.Equal("closes in 4m to 59m", describedAt(span, time.Minute), "wholly ahead")
	s.Equal("closed 5m ago at the earliest, closes in 50m at the latest", describedAt(span, 10*time.Minute), "straddling")
	s.Equal("closed 1h25m to 30m ago", describedAt(span, 90*time.Minute), "wholly behind")
}

// The vocabulary rule, pinned rather than trusted to review: magus sees a clock and
// never the provider's cache, so no output it feeds may call a window expired.
func (s *PromptSuite) TestPromptCacheDescriptionsNeverSayExpired() {
	t := s.T()
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, gap := range []time.Duration{0, time.Minute, 7 * time.Minute, 2 * time.Hour, 48 * time.Hour} {
		now := last.Add(gap)
		for _, p := range promptCacheAt("s1", last, now).Providers {
			for _, w := range p.Windows {
				assert.NotContains(t, w.Describe(now), "expire", "%s/%s at %s", p.Provider, w.Window, gap)
			}
		}
	}
}

func (s *PromptSuite) TestRenderSpanRoundsUpToASecond() {
	s.Equal("0s", renderSpan(-time.Second))
	s.Equal("1s", renderSpan(500*time.Millisecond))
	s.Equal("59s", renderSpan(59*time.Second))
}

func (s *PromptSuite) TestRenderSpanDropsAZeroLowerUnit() {
	s.Equal("1m", renderSpan(time.Minute))
	s.Equal("1h30m", renderSpan(90*time.Minute))
	s.Equal("2h", renderSpan(2*time.Hour))
	s.Equal("25h", renderSpan(24*time.Hour+time.Hour), "hours never roll into days")
	s.Equal("23h59m", renderSpan(23*time.Hour+59*time.Minute))
}

func (s *PromptSuite) TestRenderPromptCacheShowsEveryProvider() {
	t := s.T()
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := last.Add(7 * time.Minute)
	var out strings.Builder
	renderPromptCache(&out, promptCacheAt("s1", last, now), now)
	text := out.String()

	assert.Contains(t, text, "last tool call here 7m ago, session s1")
	for _, want := range []string{"Anthropic", "OpenAI", "Google Gemini"} {
		assert.Containsf(t, text, want, "a reader cannot tell which provider they are on, so every row has to be here")
	}
	assert.Contains(t, text, "closed 2m ago", "the 5m window is behind a 7m gap")
	assert.Contains(t, text, "closes in 53m")
	// The word magus has no standing to use: it sees a clock, never the provider's cache.
	assert.NotContains(t, text, "expire")
}
