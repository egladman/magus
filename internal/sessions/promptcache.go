package sessions

import (
	"fmt"
	"time"
)

// The published prompt-cache windows a session's last activity is measured against.
//
// magus never sees an API request. It sees hook invocations, one per tool call, so
// the newest hook timestamp is the only proxy it has for when a session last issued
// one. What that proxy is WORTH depends on the provider's cache window, and those
// windows are published prose rather than anything magus can derive, so they are
// written down here with the page each figure was read from.
//
// Nothing here reports a cache as expired, and nothing here picks a provider. Which
// window a harness bought is invisible from this side (it is a field on a request
// magus never sees), and so is which provider it called, so every published window
// is shown and the reader picks the row that applies to them.
//
// This is a TABLE and deliberately not a signed registry row: internal/registry
// exists for the hundreds of third-party rows a machine-readable feed produces, and
// three hand-read figures are not that. Move it there if a provider ever publishes
// these numbers as a feed.

// PromptCacheWindow is one published window: how long a cached prefix stays
// reusable after the request that last touched it.
//
// Min and Max are the published bounds, equal when a provider publishes one number.
// A provider that publishes a range does so because the real window moves with load,
// and collapsing it here would invent precision the provider refused to claim.
type PromptCacheWindow struct {
	Name string
	Min  time.Duration
	Max  time.Duration
	// RefreshedOnHit records that a cache hit restarts the window. It is what makes
	// "time since the last request" the right thing to measure at all: without it the
	// window would run from the first request of the session, which no hook can see.
	RefreshedOnHit bool
}

// PromptCacheProvider is one provider's windows and the page they were read from.
//
// Note carries what a provider publishes INSTEAD of a window. A provider with
// neither windows nor a note would be a row that says nothing, which is worse than
// an absent row: it reads as a window of zero.
type PromptCacheProvider struct {
	Name    string
	Windows []PromptCacheWindow
	Source  string
	Note    string
}

// PromptCacheProviders is the table, read off each Source on 2026-09-10.
var PromptCacheProviders = []PromptCacheProvider{
	{
		Name:   "Anthropic",
		Source: "https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching",
		Windows: []PromptCacheWindow{
			{Name: "default", Min: 5 * time.Minute, Max: 5 * time.Minute, RefreshedOnHit: true},
			{Name: "1h opt-in", Min: time.Hour, Max: time.Hour, RefreshedOnHit: true},
		},
	},
	{
		Name:   "OpenAI",
		Source: "https://platform.openai.com/docs/guides/prompt-caching",
		Windows: []PromptCacheWindow{
			// Published as ranges: around 5 to 10 minutes of inactivity and up to one
			// hour for in-memory retention, around 30 minutes and up to 24 hours for
			// extended retention. The wide bound is what lands in Max, because the
			// narrow one is what the page calls typical rather than guaranteed.
			{Name: "in-memory retention", Min: 5 * time.Minute, Max: time.Hour, RefreshedOnHit: true},
			{Name: "extended retention", Min: 30 * time.Minute, Max: 24 * time.Hour, RefreshedOnHit: true},
		},
	},
	{
		Name:   "Google Gemini",
		Source: "https://ai.google.dev/gemini-api/docs/caching",
		Note:   "explicit caches live for a TTL the caller sets; implicit caching publishes no window",
	},
}

// PromptCacheClock is where one session stands against every published window.
type PromptCacheClock struct {
	// LastMs is the activity the clock was computed from, in unix milliseconds.
	LastMs int64 `json:"last_ms"`
	// SinceMs is how long ago that was, in milliseconds. It is clamped at zero: a
	// stamp ahead of now means two clocks disagree, not that a session acted in the
	// future, and a negative age would render as one.
	SinceMs   int64               `json:"since_ms"`
	Providers []PromptCacheStatus `json:"providers"`
}

// PromptCacheStatus is one provider's rows, resolved against a session's last activity.
type PromptCacheStatus struct {
	Provider string                    `json:"provider"`
	Source   string                    `json:"source"`
	Note     string                    `json:"note,omitempty"`
	Windows  []PromptCacheWindowStatus `json:"windows,omitempty"`
}

// PromptCacheWindowStatus is when one window closes for this session.
//
// Two instants rather than one, equal for an exactly published window: a range
// closes somewhere between them, and reporting either end alone would be a claim the
// provider does not make.
type PromptCacheWindowStatus struct {
	Window     string `json:"window"`
	ClosesAtMs int64  `json:"closes_at_ms"`
	ClosesByMs int64  `json:"closes_by_ms"`
	// Closed is set once the LATEST instant has passed, which is the only point the
	// window is certainly behind the session.
	Closed         bool `json:"closed"`
	RefreshedOnHit bool `json:"refreshed_on_hit,omitempty"`
}

// PromptCache resolves the table against a session's last activity.
//
// last is the newest observation of the session; now is the instant to judge it
// against, passed rather than read so a caller renders one stable clock and a test
// can pin one. A zero last yields a zero clock with no providers: a session nothing
// has observed has no age to report, and computing one anyway would date every
// window to the epoch.
func PromptCache(last, now time.Time) PromptCacheClock {
	if last.IsZero() {
		return PromptCacheClock{}
	}
	clock := PromptCacheClock{LastMs: last.UnixMilli(), SinceMs: max(now.Sub(last).Milliseconds(), 0)}
	for _, p := range PromptCacheProviders {
		status := PromptCacheStatus{Provider: p.Name, Source: p.Source, Note: p.Note}
		for _, w := range p.Windows {
			status.Windows = append(status.Windows, w.statusAt(last, now))
		}
		clock.Providers = append(clock.Providers, status)
	}
	return clock
}

func (w PromptCacheWindow) statusAt(last, now time.Time) PromptCacheWindowStatus {
	by := last.Add(w.Max)
	return PromptCacheWindowStatus{
		Window:         w.Name,
		ClosesAtMs:     last.Add(w.Min).UnixMilli(),
		ClosesByMs:     by.UnixMilli(),
		Closed:         !now.Before(by),
		RefreshedOnHit: w.RefreshedOnHit,
	}
}

// Phrase says where now stands against this window, for a text surface.
//
// The tense is the whole point: a window that has passed CLOSED, one still ahead
// CLOSES. "Expired" is never the word, here or in a caller, because magus cannot see
// the provider's cache at all. It can see a clock, and a claim about the cache is one
// it has no standing to make.
func (w PromptCacheWindowStatus) Phrase(now time.Time) string {
	at, by := time.UnixMilli(w.ClosesAtMs), time.UnixMilli(w.ClosesByMs)
	if at.Equal(by) {
		if w.Closed {
			return "closed " + shortDuration(now.Sub(by)) + " ago"
		}
		return "closes in " + shortDuration(by.Sub(now))
	}
	switch {
	case w.Closed:
		return "closed " + shortDuration(now.Sub(at)) + " to " + shortDuration(now.Sub(by)) + " ago"
	case now.Before(at):
		return "closes in " + shortDuration(at.Sub(now)) + " to " + shortDuration(by.Sub(now))
	default:
		return "closed " + shortDuration(now.Sub(at)) + " ago at the earliest, closes in " + shortDuration(by.Sub(now)) + " at the latest"
	}
}

// Since renders how long ago the clock's activity was.
func (c PromptCacheClock) Since() string {
	return shortDuration(time.Duration(c.SinceMs) * time.Millisecond)
}

// shortDuration renders a span at the precision a reader of this clock acts on.
// Seconds matter inside a minute and are noise past one, which is what separates it
// from time.Duration.String and its "55m0s".
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
