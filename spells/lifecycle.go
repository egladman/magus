package spells

import (
	"strings"
	"time"
)

// ListLifecyclesContract is the reserved contract-function name a lifecycle-provider spell
// exports: it answers when each release cycle of a tool reaches its end of life. magus asks
// it for the distinct [Tool.Lifecycle] values the workspace's spells declare, in ONE call,
// the way publish_review takes every draft at once:
//
//	fun list_lifecycles(target: Target, cb: fun(any)) > [Lifecycle]
//
// cb yields {keys: [str]}. A key the provider does not know is left out of the answer
// rather than thrown for; magus reads its rows as unknown.
//
// A workspace wires ONE provider (magus\lifecycle.provider), because Tool.Lifecycle is one
// vocabulary: a second provider would read "go" as a different product. Nothing it answers
// can fail a build. It fills a column of `magus describe tools` and a doctor line, which
// is why it is a contract and not a gate.
const ListLifecyclesContract = "list_lifecycles"

// Lifecycle is one product's release cycles as its provider read them upstream. Buzz sees
// it as `object Lifecycle` in magus/spell; a provider constructs and returns it.
//
// It reports facts, not verdicts. Which cycle a probed version belongs to, and whether that
// cycle is past its end of life, is decided in Go ([Lifecycle.FindCycle],
// [ReleaseCycle.SupportOn]), so "past EOL" has one definition however many providers exist.
//
// The json tags are the provider cache's wire form. A rename is a wire change: regenerate
// the mirror and bump lifecycleCacheVersion.
type Lifecycle struct {
	// Key is the Tool.Lifecycle value this answers. It must be one magus asked for.
	Key string `json:"key"`
	// Source is the URL the provider read, printed and cited so a reader can check it.
	Source string `json:"source"`
	// AsOf is when upstream last changed the data, RFC 3339. The provider reports what
	// upstream says and never the clock: a fetch of stale data is still stale.
	AsOf string `json:"as_of"`
	// Cycles lists the release cycles, in any order.
	Cycles []ReleaseCycle `json:"cycles"`
}

// ReleaseCycle is one line of releases sharing a support window: go "1.26", nodejs "22".
type ReleaseCycle struct {
	// Cycle is the version prefix every release in the line carries.
	Cycle string `json:"cycle"`
	// Released is the cycle's first release date, YYYY-MM-DD.
	Released string `json:"released"`
	// EOL is the date support ends, YYYY-MM-DD. Empty means upstream has not announced one.
	EOL string `json:"eol"`
	// LTS marks a long-term-support cycle.
	LTS bool `json:"lts"`
	// Latest is the newest release in the cycle.
	Latest string `json:"latest"`
}

// Support is where a cycle stands on a given day. The values are the `support` column of
// `magus describe tools` and the SUPPORT_* enum the console reads.
type Support string

const (
	SupportSupported   Support = "supported"
	SupportEOL         Support = "eol"
	SupportUnannounced Support = "unannounced" // upstream has named no end date
	SupportUnknown     Support = "unknown"     // no data, or no cycle matched the version
)

// FindCycle returns the longest cycle that prefixes version at a component boundary: 1.25.3
// belongs to "1.25", never to "1.2". A leading "v" is ignored, so a probed "v22.3.0" and a
// window's "22" both match nodejs "22". Reports false when no cycle matches.
func (l Lifecycle) FindCycle(version string) (ReleaseCycle, bool) {
	v := strings.TrimPrefix(version, "v")
	var best ReleaseCycle
	found := false
	for _, c := range l.Cycles {
		if c.Cycle == "" || (v != c.Cycle && !strings.HasPrefix(v, c.Cycle+".")) {
			continue
		}
		if !found || len(c.Cycle) > len(best.Cycle) {
			best, found = c, true
		}
	}
	return best, found
}

// PlaceVersion places version in its cycle and reports where that cycle stands on day. A
// version no cycle carries is SupportUnknown, with a zero cycle.
func (l Lifecycle) PlaceVersion(version string, day time.Time) (ReleaseCycle, Support) {
	c, ok := l.FindCycle(version)
	if !ok {
		return ReleaseCycle{}, SupportUnknown
	}
	return c, c.SupportOn(day)
}

// SupportOn reports whether the cycle is supported on day. A cycle is past its end of life
// ON its EOL date, which is how endoflife.date's eolFrom reads. An EOL that is not a date
// is unknown rather than guessed.
func (c ReleaseCycle) SupportOn(day time.Time) Support {
	if c.EOL == "" {
		return SupportUnannounced
	}
	eol, err := time.Parse(time.DateOnly, c.EOL)
	if err != nil {
		return SupportUnknown
	}
	y, m, d := day.UTC().Date()
	if time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Before(eol) {
		return SupportSupported
	}
	return SupportEOL
}
