package sessions

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every figure in the table was read off a vendor page by a person, so the page is the
// only thing that makes a row re-checkable when the vendor changes it.
func TestPromptCacheProvidersCiteASource(t *testing.T) {
	for _, p := range PromptCacheProviders {
		assert.NotEmpty(t, p.Name, "a provider row with no name")
		assert.Truef(t, strings.HasPrefix(p.Source, "https://"), "%s: source %q is not a URL", p.Name, p.Source)
		assert.Truef(t, len(p.Windows) > 0 || p.Note != "",
			"%s: a row with neither a window nor a note says nothing, and reads as a window of zero", p.Name)
		for _, w := range p.Windows {
			assert.NotEmptyf(t, w.Name, "%s: an unnamed window", p.Name)
			assert.Positivef(t, w.Min, "%s/%s: a window of zero", p.Name, w.Name)
			assert.GreaterOrEqualf(t, w.Max, w.Min, "%s/%s: published bounds are inverted", p.Name, w.Name)
		}
	}
}

func TestPromptCacheResolvesEveryWindow(t *testing.T) {
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	clock := PromptCacheAt("s1", last, last.Add(6*time.Minute))

	require.Len(t, clock.Providers, len(PromptCacheProviders))
	assert.Equal(t, last.UnixMilli(), clock.LastMs)
	assert.Equal(t, "6m", clock.SinceText())
	assert.Equal(t, "s1", clock.Session, "the clock is a fact about the session it was computed from")

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
func TestPromptCacheWithoutActivity(t *testing.T) {
	assert.Empty(t, PromptCacheAt("s1", time.Time{}, time.Now()).Providers)
}

func TestPromptCacheWindowDescribe(t *testing.T) {
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	exact := PromptCacheWindow{Name: "default", Min: 5 * time.Minute, Max: 5 * time.Minute}
	span := PromptCacheWindow{Name: "in-memory retention", Min: 5 * time.Minute, Max: time.Hour}

	for _, tc := range []struct {
		name   string
		window PromptCacheWindow
		at     time.Duration
		want   string
	}{
		{"exact, still open", exact, 30 * time.Second, "closes in 4m"},
		{"exact, behind", exact, 20 * time.Minute, "closed 15m ago"},
		{"range, wholly ahead", span, time.Minute, "closes in 4m to 59m"},
		{"range, straddling", span, 10 * time.Minute, "closed 5m ago at the earliest, closes in 50m at the latest"},
		{"range, wholly behind", span, 90 * time.Minute, "closed 1h25m to 30m ago"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := last.Add(tc.at)
			assert.Equal(t, tc.want, tc.window.statusAt(last, now).Describe(now))
		})
	}
}

// The vocabulary rule, pinned rather than trusted to review: magus sees a clock and
// never the provider's cache, so no surface it feeds may call a window expired.
func TestPromptCacheDescriptionsNeverSayExpired(t *testing.T) {
	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, gap := range []time.Duration{0, time.Minute, 7 * time.Minute, 2 * time.Hour, 48 * time.Hour} {
		now := last.Add(gap)
		for _, p := range PromptCacheAt("s1", last, now).Providers {
			for _, w := range p.Windows {
				assert.NotContains(t, w.Describe(now), "expire", "%s/%s at %s", p.Provider, w.Window, gap)
			}
		}
	}
}

func TestIdleFor(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:                  "0s",
		500 * time.Millisecond:        "1s",
		59 * time.Second:              "59s",
		time.Minute:                   "1m",
		90 * time.Minute:              "1h30m",
		2 * time.Hour:                 "2h",
		24*time.Hour + time.Hour:      "25h",
		23*time.Hour + 59*time.Minute: "23h59m",
	} {
		assert.Equal(t, want, idleFor(d), "idleFor(%s)", d)
	}
}
