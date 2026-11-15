package impact

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
)

// fixture is a changeset every lens had something to say about: the populated case
// each section's render is pinned against.
func fixture() Report {
	return Report{
		Reach: &reachSummary{
			Seeds:    1,
			Rebuilds: 2,
			Projects: []reachedProject{
				{Path: "root", Seed: true, Files: 3},
				{Path: "docs"},
			},
		},
		Ownership: []projectOwner{
			{Project: "root", Primary: "alice", PrimaryShare: 72, Authors: 4},
			{Project: "docs", Primary: "bob", PrimaryShare: 100, Authors: 1, BusFactor1: true},
		},
		Cost: &costEstimate{
			TotalMs: 260_000,
			Projects: []projectCost{
				{Project: "root", Target: "ci", Ms: 200_000, Samples: 18, HitRate: 0.4},
				{Project: "docs", Target: "test", Ms: 60_000, Samples: 1},
			},
		},
		Advisors: []AdviceSection{
			{Name: "exported-api", Title: "A public symbol changed", Body: "diff.Diff is exported.\nBump the minor."},
			// A retraction sits in the fixture because every real run has several: it must not
			// read as a finding, and it must not read as an advisor that failed either.
			{Name: "retracted", Title: "Nothing to retract"},
		},
		// A real advisor filename, in the shape collectAdvice stamps. An invented one
		// ("coverage") read as evidence that a coverage advisor exists; none does, and a
		// planning pass spent a work item on teaching it to find a profile.
		AdvisorNotes: []string{"could not run: doctor.buzz: main() returned 1"},
		// Relative to now, not a fixed date: the rendered age is computed at read time, so a
		// literal tip would make the expected line drift by a day every day.
		AdvisorBase: &advisorBase{
			Ref: "origin/main",
			Tip: time.Now().Add(-50 * time.Hour).Format(time.RFC3339),
		},
		Anchors: []review.AnchorHit{
			{Note: "cache-invalidation-pairs", Kind: "file", Target: "internal/cache/cache.go"},
			{Note: "secret-value-type", Kind: "symbol", Target: "m types/Secret#", Drift: "drifted-anchor"},
		},
		LeftBehind: &leftBehind{
			Markers: []addedMarker{
				{Path: "internal/cache/signing.go", Line: 32, Marker: "compat(until:", Text: "no store still serves ed25519 envelopes"},
			},
			Unreferenced:      []string{"signEd25519 (internal/cache/signing.go:40)"},
			UnreferencedKnown: true,
		},
		Evidence: []trail.Consultation{
			{Verb: "explain", Subject: "internal/cache", Count: 2},
			{Verb: "refs", Subject: "m types/Secret#", Count: 1},
		},
		Review: &reviewSummary{
			Files: 8, Read: 5,
			Stale:  []string{"internal/cache/cache.go"},
			Unread: []string{"types/diff.go", "std/magus.go"},
		},
	}
}

// TestImpactRendersEverySection pins the sections a disposer reads before landing.
//
// Asserted line by line rather than by substring: these sentences are the whole product of
// this report, and a section that silently stopped rendering its list would still pass a
// header-only check.
func TestImpactRendersEverySection(t *testing.T) {
	assert.Equal(t, []string{
		"IMPACT - the blast radius of landing this",
		"",
		"REACH: 1 project edited, 2 projects rebuild",
		"      root - edited, 3 files",
		"      docs - rebuilds because it depends on one that was",
		"",
		"OWNERSHIP: who has been changing the projects in reach",
		"      root mostly alice (72%), 4 authors",
		"      docs mostly bob (100%), 1 author - BUS FACTOR 1",
		"",
		"COST: ~4m20s to rebuild the reach (history-based estimate: the p75 of past runs, discounted by the cache hit rate they recorded)",
		"      root ci ~3m20s (18 runs), 40% cache hits",
		"      docs test ~1m0s (1 run)",
		"",
		"BASE: origin/main, tip 2 days old - a local run stays off the network, so anything merged since is outside what the advisors saw; `git fetch origin main` brings it forward",
		"ADVISORS: 1 finding",
		"      A public symbol changed",
		"        diff.Diff is exported.",
		"        Bump the minor.",
		"      1 ran and found nothing: retracted",
		"      could not run: doctor.buzz: main() returned 1",
		"",
		"ANCHORS: 2 notes anchored to what you changed",
		"      note cache-invalidation-pairs anchors file:internal/cache/cache.go",
		"      note secret-value-type anchors symbol:m types/Secret# [drifted-anchor]",
		"",
		"LEFT BEHIND: 1 compat(until:) or Deprecated: marker added",
		"      internal/cache/signing.go:32 compat(until: no store still serves ed25519 envelopes",
		"      1 symbol lost the last use here and is still defined",
		"        signEd25519 (internal/cache/signing.go:40)",
		"",
		"EVIDENCE: 2 questions the authors asked magus while writing this",
		"      explain internal/cache (x2)",
		"      refs m types/Secret#",
		"",
		"REVIEW: 1 file(s) changed after you read them",
		"      internal/cache/cache.go",
		"      2 file(s) you have not opened, widest blast radius first",
		"        types/diff.go",
		"        std/magus.go",
		"      record what you read, wherever you read it: magus diff --ack <path>...",
		"      or step through them here: magus diff",
	}, Lines(fixture()))
}

// TestImpactEmptyFormsSayNobodyLooked is the half that matters.
//
// Every one of these sections is empty for two completely different reasons (nothing found,
// or nothing measured), and only one of them is good news. A silent section, or a cost of
// zero, would report an unmeasured workspace as a cheap safe change.
func TestImpactEmptyFormsSayNobodyLooked(t *testing.T) {
	lines := Lines(Report{})

	assert.Equal(t, []string{
		"IMPACT - the blast radius of landing this",
		"",
		"REACH: no project contains a changed file, so nothing rebuilds",
		"",
		"OWNERSHIP: no commit history in the window, so no owner is named",
		"",
		"COST: no run history yet, so there is nothing to estimate from",
		"      Run `magus affected ci` once and the next impact can price this.",
		"",
		"ADVISORS: nothing to report",
		"",
		"ANCHORS: no note anchors a changed file or symbol",
		"",
		"LEFT BEHIND: not measured",
		"",
		"EVIDENCE: no agent activity recorded in this checkout, so what the authors consulted is unknown",
		"      This record is per checkout, and wiring it is `magus agent install`.",
		"",
		"REVIEW: read receipts unavailable; step a file through in `magus diff` to earn one",
	}, lines)

	// The one number that must never appear: a reach nobody has ever timed is not free.
	assert.NotContains(t, strings.Join(lines, "\n"), "~0s")
}
