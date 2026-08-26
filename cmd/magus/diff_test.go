package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/ci/forecast"
	session "github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/interactive/diff"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
)

// The viewer is the DEFAULT at a terminal, so this table is about FALLBACK rather than
// refusal, and that distinction is the whole point of it.
//
// Each row used to be a usage error, correctly: the flag was opt-in, so asking for the viewer
// somewhere it cannot draw was a mistake worth naming. Now nobody asked. A script running
// `magus diff -o json`, a patch file, a watch loop are ordinary invocations, and any one of
// them erroring because a default changed would be the worst kind of regression - it breaks
// callers that were never using the feature.
//
// The terminal is an ARGUMENT rather than a probe, which is what makes this testable with no pty.
func TestWantsTUIFallsBackRatherThanRefusing(t *testing.T) {
	workingTree := diffInput{kind: inputWorkingTree, label: "the working tree"}
	stdin := diffInput{kind: inputStdin, label: "a patch on stdin"}
	patchFile := diffInput{kind: inputFile, path: "x.patch", label: "the patch in x.patch"}
	revRange := diffInput{kind: inputRevRange, base: "main", head: "topic", label: "the range main...topic"}
	atTerminal := diffTUITerm{Reads: true, Paints: true}

	tests := []struct {
		name    string
		flags   gen.DiffFlags
		src     diffInput
		format  Format
		term    diffTUITerm
		enabled bool
		want    bool
	}{
		{
			name:    "a person at a terminal gets the viewer",
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
			want:    true,
		},
		{
			name:    "--generated composes: it only sets the initial fold",
			flags:   gen.DiffFlags{Generated: true},
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
			want:    true,
		},
		{
			// The whole point of --rev. A range is a tree state magus can address, so it keeps the
			// viewer where a handed-over patch cannot; without this the branch review that --rev
			// exists for would degrade to the one-shot report it was built to replace.
			name:    "a revision range keeps the viewer",
			src:     revRange,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
			want:    true,
		},
		{
			name:    "--no-tui is the one explicit answer",
			flags:   gen.DiffFlags{NoTui: true},
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:   "config can turn it off for every run",
			src:    workingTree,
			format: outputText,
			term:   atTerminal,
		},
		{
			name:    "a patch file has no working tree to coordinate a session over",
			src:     patchFile,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "stdin, same reason - this is the git-pager path",
			src:     stdin,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "--watch drives the terminal itself",
			flags:   gen.DiffFlags{Watch: true},
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "a machine-readable format is a script, and scripts get the report",
			src:     workingTree,
			format:  outputJSON,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "--impact asked for a report, which the viewer has nowhere to put",
			flags:   gen.DiffFlags{Impact: true},
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "--ack records and returns",
			flags:   gen.DiffFlags{Ack: true},
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "no terminal at all: CI, an agent, a pipe",
			src:     workingTree,
			format:  outputText,
			enabled: true,
		},
		{
			// `magus diff > file`. stdin is still a keyboard, so the stdout probe is the only
			// thing that can catch this one.
			name:    "a redirected stdout is caught here, not deep inside the viewer",
			src:     workingTree,
			format:  outputText,
			term:    diffTUITerm{Reads: true},
			enabled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := tc.flags
			assert.Equal(t, tc.want, wantsTUI(&flags, tc.src, tc.format, tc.term, tc.enabled))
		})
	}
}

// TestDiffTUIFilesJoinKeepsTheAnnotationOrder guards the one thing this join must not do:
// re-derive an order. The patch arrives in whatever order the VCS emitted and the
// annotations arrive in reading order, so following the patch would quietly undo
// SortForReading and put a lockfile back at the top.
func TestDiffTUIFilesJoinKeepsTheAnnotationOrder(t *testing.T) {
	reach := 12
	rev := types.Diff{Files: []types.DiffFile{
		{Path: "core.go", Role: types.DiffRoleSource, Surface: types.DiffSurfaceInternal, Reach: &reach, Project: "root"},
		{Path: "gen/out.json", Role: types.DiffRoleOutput, Surface: types.DiffSurfaceUnknown},
	}}
	patch := "diff --git a/gen/out.json b/gen/out.json\n" +
		"@@ -1 +1 @@\n" +
		"-{}\n" +
		"+{\"a\":1}\n" +
		"diff --git a/core.go b/core.go\n" +
		"@@ -3 +3 @@\n" +
		"+func F() {}\n" +
		"@@ -9 +9 @@\n" +
		"+func G() {}\n"

	files := diffTUIFiles(rev, session.ParseHunks(patch))
	require.Len(t, files, 2)
	assert.Equal(t, "core.go", files[0].Path)
	assert.False(t, files[0].Generated)
	assert.Equal(t, []string{"12 files reference its widest changed symbol", "in root"}, files[0].Facts)
	require.Len(t, files[0].Hunks, 2)
	assert.Equal(t, "@@ -3 +3 @@", files[0].Hunks[0].Header)
	assert.NotEmpty(t, files[0].Hunks[0].Digest, "the viewed set is keyed by this")
	// The patch coordinate a comment is anchored by, carried rather than re-derived: dropping
	// it here would hand the viewer a second hunk numbered zero.
	assert.Equal(t, 1, files[0].Hunks[1].Index)

	assert.Equal(t, "gen/out.json", files[1].Path)
	assert.True(t, files[1].Generated)
	require.Len(t, files[1].Hunks, 1)
}

func TestDiffCountsLineIsWhatTheReaderIsLeftWith(t *testing.T) {
	rev := types.Diff{
		Files: []types.DiffFile{
			{Path: "a.go", Role: types.DiffRoleSource},
			{Path: "b.go", Role: types.DiffRoleSource},
			{Path: "gen/out.json", Role: types.DiffRoleOutput},
		},
		SeedProjects:     []string{"root"},
		AffectedProjects: []types.ImpactProject{{Path: "root"}, {Path: "docs"}},
	}
	assert.Equal(t, "2 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild",
		diffCountsLine(rev, false))
	// Under --generated the same files are printed right below this line, so calling them
	// folded contradicts the page it introduces.
	assert.Equal(t, "2 files to read, 1 generated shown; 1 projects edited, 2 projects rebuild",
		diffCountsLine(rev, true))
	assert.Equal(t, "0 files to read", diffCountsLine(types.Diff{}, false))
	assert.Equal(t, "0 files to read", diffCountsLine(types.Diff{}, true),
		"with nothing generated there is no clause either way")
}

// impactFixture is a changeset every lens had something to say about: the populated case
// each section's render is pinned against.
func impactFixture() diffImpact {
	return diffImpact{
		Reach: &impactReach{
			Seeds:    1,
			Rebuilds: 2,
			Projects: []impactProject{
				{Path: "root", Seed: true, Files: 3},
				{Path: "docs"},
			},
		},
		Ownership: []impactOwner{
			{Project: "root", Primary: "alice", PrimaryShare: 72, Authors: 4},
			{Project: "docs", Primary: "bob", PrimaryShare: 100, Authors: 1, BusFactor1: true},
		},
		Cost: &impactCost{
			TotalMs: 260_000,
			Projects: []impactCostProject{
				{Project: "root", Target: "ci", Ms: 200_000, Samples: 18, HitRate: 0.4},
				{Project: "docs", Target: "test", Ms: 60_000, Samples: 1},
			},
		},
		Advisors: []adviceSection{
			{Name: "public-surface", Title: "A public symbol changed", Body: "types.Diff is exported.\nBump the minor."},
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
		AdvisorBase: &impactAdvisorBase{
			Ref: "origin/main",
			Tip: time.Now().Add(-50 * time.Hour).Format(time.RFC3339),
		},
		Anchors: []anchorHit{
			{Note: "cache-invalidation-pairs", Kind: "file", Target: "internal/cache/cache.go"},
			{Note: "secret-value-type", Kind: "symbol", Target: "m types/Secret#", Drift: "drifted-anchor"},
		},
		Rationale: []rationaleHit{
			{Path: "internal/cache/signing.go", Line: 32, Until: "no store still serves ed25519 envelopes"},
		},
		Review: &impactReview{
			Files: 8, Read: 5,
			Stale:  []string{"internal/cache/cache.go"},
			Unread: []string{"types/diff.go", "std/magus.go"},
		},
	}
}

// TestImpactRendersEverySection pins the five sections a disposer reads before landing.
//
// Asserted line by line rather than by substring: these sentences are the whole product of
// this surface, and a section that silently stopped rendering its list would still pass a
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
		"        types.Diff is exported.",
		"        Bump the minor.",
		"      1 ran and found nothing: retracted",
		"      could not run: doctor.buzz: main() returned 1",
		"",
		"ANCHORS: 2 notes anchored to what you changed",
		"      note cache-invalidation-pairs anchors file:internal/cache/cache.go",
		"      note secret-value-type anchors symbol:m types/Secret# [drifted-anchor]",
		"",
		"RATIONALE: 1 compat(until:) marker in files you changed - each names why the code stays",
		"      internal/cache/signing.go:32 until no store still serves ed25519 envelopes",
		"",
		"REVIEW: 1 file(s) changed after you read them",
		"      internal/cache/cache.go",
		"      2 file(s) you have not opened, widest blast radius first",
		"        types/diff.go",
		"        std/magus.go",
		"      record what you read, wherever you read it: magus diff --ack <path>...",
		"      or step through them here: magus diff",
	}, impactLines(impactFixture()))
}

// TestImpactEmptyFormsSayNobodyLooked is the half that matters.
//
// Every one of these sections is empty for two completely different reasons - nothing found,
// or nothing measured - and only one of them is good news. A silent section, or a cost of
// zero, would report an unmeasured workspace as a cheap safe change.
func TestImpactEmptyFormsSayNobodyLooked(t *testing.T) {
	lines := impactLines(diffImpact{})

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
		"RATIONALE: no compat(until:) marker in the files you changed",
		"",
		"REVIEW: read receipts unavailable; step a file through in `magus diff` to earn one",
	}, lines)

	// The one number that must never appear: a reach nobody has ever timed is not free.
	assert.NotContains(t, strings.Join(lines, "\n"), "~0s")
}

// TestImpactCountsFindingsNotSections pins the headline against the retraction shape every
// real run produces: eleven advisors publish, one has something to say, and ten publish an
// empty body to withdraw whatever they said last time.
func TestImpactCountsFindingsNotSections(t *testing.T) {
	lines := impactAdvisorLines([]adviceSection{
		{Name: "unclaimed", Title: "Files no project claims"},
		{Name: "conformance", Title: "A target name diverges", Body: "rename it\n"},
		{Name: "skip-cache", Title: "A target opted out of the cache"},
	}, nil, nil)

	assert.Equal(t, []string{
		"ADVISORS: 1 finding",
		"      A target name diverges",
		"        rename it",
		"      2 ran and found nothing: unclaimed, skip-cache",
	}, lines)
}

// TestImpactBaseSeparatesOldFromAbsent pins the distinction the line exists for.
//
// A base nobody fetched and a base that is merely stale produce the same silent advisors, and
// only one of them is a finding about the code. Collapsing them would make an unfetched clone
// read as a clean review.
func TestImpactBaseSeparatesOldFromAbsent(t *testing.T) {
	stale := impactAdvisorBaseLine(&impactAdvisorBase{
		Ref: "origin/main",
		Tip: time.Now().Add(-9 * 24 * time.Hour).Format(time.RFC3339),
	})
	assert.Contains(t, stale, "tip 9 days old")
	assert.Contains(t, stale, "`git fetch origin main`")

	absent := impactAdvisorBaseLine(&impactAdvisorBase{Ref: "origin/main"})
	assert.Contains(t, absent, "is not in this clone")
	assert.NotContains(t, absent, "tip ")

	// Nil is neither: a backend that cannot date a revision has not measured the base, and
	// inventing "fresh" for it is the one answer that would mislead.
	assert.Empty(t, impactAdvisorBaseLine(nil))
}

// TestImpactBaseQualifiesAnEmptyAdvisorSet is the case the caveat matters most in: no
// findings against a week-old base is not the same news as no findings against today's.
func TestImpactBaseQualifiesAnEmptyAdvisorSet(t *testing.T) {
	lines := impactAdvisorLines(nil, nil, &impactAdvisorBase{Ref: "origin/main"})
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "origin/main")
	assert.Equal(t, "ADVISORS: nothing to report", lines[1])
}

// TestImpactAgeKeepsOneUnit pins the rounding. A reader is deciding whether to fetch, and
// the order of magnitude is the whole decision.
func TestImpactAgeKeepsOneUnit(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "seconds"},
		{time.Minute, "1 minute"},
		{90 * time.Minute, "1 hour"},
		{25 * time.Hour, "1 day"},
		{50 * time.Hour, "2 days"},
	} {
		assert.Equal(t, tc.want, impactAge(tc.d), tc.d.String())
	}
}

// TestImpactListsReportWhatTheyLeftOff guards the bound on every section list. A reach of
// sixty projects is a real answer and an unreadable one, and a list that stops without saying
// so reads as the whole answer.
func TestImpactListsReportWhatTheyLeftOff(t *testing.T) {
	r := &impactReach{Seeds: 1, Rebuilds: 14}
	for i := 0; i < 14; i++ {
		r.Projects = append(r.Projects, impactProject{Path: fmt.Sprintf("p%d", i)})
	}
	lines := impactReachLines(r)
	require.Len(t, lines, 1+impactListCap+1)
	assert.Equal(t, "      and 4 more", lines[len(lines)-1])
}

// TestPrintDiffTextWithoutImpactIsUnchanged is the additive guarantee.
//
// There is no golden file for this surface, so the whole report is spelled out here: any
// impact line leaking into the default rendering fails this, and so does a stray blank
// line, which a substring check for the section headers would not catch.
func TestPrintDiffTextWithoutImpactIsUnchanged(t *testing.T) {
	reach := 12
	rev := types.Diff{
		Files: []types.DiffFile{
			{Path: "a.go", Role: types.DiffRoleSource, Reach: &reach, Project: "root"},
			{Path: "gen/out.json", Role: types.DiffRoleOutput},
		},
		SeedProjects:     []string{"root"},
		AffectedProjects: []types.ImpactProject{{Path: "root"}, {Path: "docs"}},
	}
	identity := func(p string) string { return p }

	out := captureStdout(t, func() {
		require.NoError(t, printDiffText(rev, false, identity, nil))
	})

	assert.Equal(t, "1 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild\n"+
		"\n"+
		"  a.go\n"+
		"      12 files reference its widest changed symbol\n"+
		"      in root\n"+
		"\n"+
		"1 generated files folded. They are declared target outputs: reading one is\n"+
		"reading a machine's restatement of a change made elsewhere. Show them with --generated,\n"+
		"or ask why one is folded with `magus describe file <path>`.\n", out)

	// Named separately from the byte comparison so a failure says WHICH promise broke.
	for _, header := range []string{"IMPACT", "REACH:", "OWNERSHIP:", "COST:", "ADVISORS:", "ANCHORS:"} {
		assert.NotContains(t, out, header)
	}
}

// TestPrintDiffTextAppendsImpactAboveTheConsoleLink pins where the report lands: after the
// file list, so the ordering of everything a reader already knows how to scan is untouched.
func TestPrintDiffTextAppendsImpactAboveTheConsoleLink(t *testing.T) {
	rev := types.Diff{Files: []types.DiffFile{{Path: "a.go", Role: types.DiffRoleSource}}}
	pre := impactFixture()

	out := captureStdout(t, func() {
		require.NoError(t, printDiffText(rev, false, func(p string) string { return p }, &pre))
	})

	assert.True(t, strings.HasPrefix(out, "1 files to read\n"), "the counts line still opens the report")
	assert.Contains(t, out, "\nIMPACT - the blast radius of landing this\n")
	assert.Less(t, strings.Index(out, "  a.go"), strings.Index(out, "IMPACT"),
		"the changeset is what a reader came for; the impact follows it")
}

// TestImpactCostRefusesToPriceAnUnmeasuredReach pins the one number this section must never
// print.
//
// forecast always answers PredictDuration - it falls back to a workspace percentile and then to
// a compiled-in constant - so a total is computable for a workspace nobody has ever timed. That
// total is a fabrication wearing a duration, and quoting it would teach a reader to trust every
// later one.
func TestImpactCostRefusesToPriceAnUnmeasuredReach(t *testing.T) {
	affected := []types.ImpactProject{{Path: "root", Seed: true, Files: []string{"a.go"}}, {Path: "docs"}}

	t.Run("no history at all", func(t *testing.T) {
		assert.Nil(t, impactCostOf(&forecast.History{}, affected))
	})

	t.Run("a project below the sample floor is not a measurement", func(t *testing.T) {
		h := &forecast.History{Projects: map[string]map[string]forecast.Stats{
			"root": {"ci": {P75Ms: 90_000, Samples: impactMinSamples - 1}},
		}}
		assert.Nil(t, impactCostOf(h, affected))
	})

	t.Run("only the measured projects are counted, and the rest are not guessed at", func(t *testing.T) {
		h := &forecast.History{Projects: map[string]map[string]forecast.Stats{
			"root": {"ci": {P75Ms: 90_000, Samples: 12}},
			"docs": {"lint": {P75Ms: 5_000, Samples: 40}},
		}}
		c := impactCostOf(h, affected)
		require.NotNil(t, c)
		require.Len(t, c.Projects, 1, "docs declares no ci or test target the history has timed")
		assert.Equal(t, "root", c.Projects[0].Project)
		assert.Equal(t, "ci", c.Projects[0].Target)
		assert.Equal(t, int64(90_000), c.TotalMs)
		assert.Equal(t, 12, c.Projects[0].Samples)
	})

	t.Run("ci outranks test as the estimate of a whole rebuild", func(t *testing.T) {
		h := &forecast.History{Projects: map[string]map[string]forecast.Stats{
			"root": {
				"ci":   {P75Ms: 90_000, Samples: 12},
				"test": {P75Ms: 30_000, Samples: 99},
			},
		}}
		c := impactCostOf(h, affected)
		require.NotNil(t, c)
		assert.Equal(t, "ci", c.Projects[0].Target)
	})
}

// TestImpactOwnersJoinOnlyTheReach pins both halves of the join: a project outside the
// blast radius is not this changeset's problem, and a project the lens cannot name an owner
// for is dropped rather than listed with a blank one, which reads as a lookup that failed.
func TestImpactOwnersJoinOnlyTheReach(t *testing.T) {
	own := types.OwnershipOutput{Projects: []types.OwnershipEntry{
		{Path: "root", Primary: "alice", PrimaryShare: 72, Authors: 4},
		{Path: "unrelated", Primary: "carol", PrimaryShare: 51, Authors: 9},
		{Path: "docs", Authors: 0},
	}}
	affected := []types.ImpactProject{{Path: "root"}, {Path: "docs"}, {Path: "never-committed"}}

	got := impactOwnersOf(own, affected)
	require.Len(t, got, 1)
	assert.Equal(t, "root", got[0].Project)
	assert.Equal(t, "alice", got[0].Primary)
}

// TestImpactReachRendersWhatTheDiffAlreadyKnew guards against the reach section growing its
// own idea of the blast radius: types.Diff has carried these two fields all along and this
// section only prints them.
func TestImpactReachRendersWhatTheDiffAlreadyKnew(t *testing.T) {
	assert.Nil(t, impactReachOf(types.Diff{}), "an empty closure is a state, not a lookup failure")

	r := impactReachOf(types.Diff{
		SeedProjects:     []string{"root"},
		AffectedProjects: []types.ImpactProject{{Path: "root", Seed: true, Files: []string{"a.go", "b.go"}}, {Path: "docs"}},
	})
	require.NotNil(t, r)
	assert.Equal(t, 1, r.Seeds)
	assert.Equal(t, 2, r.Rebuilds)
	assert.Equal(t, []impactProject{{Path: "root", Seed: true, Files: 2}, {Path: "docs"}}, r.Projects)
}

// TestDiffSymbolIDsAreWhatASymbolAnchorNames pins the second half of the anchors query. A note
// anchors a symbol by its index id, so passing labels or paths would match nothing and the
// section would report a clean tree it never checked.
func TestDiffSymbolIDsAreWhatASymbolAnchorNames(t *testing.T) {
	rev := types.Diff{Files: []types.DiffFile{
		{Path: "a.go", Symbols: []types.DiffSymbol{{ID: "m types/Diff#", Label: "Diff"}, {ID: "", Label: "unindexed"}}},
		{Path: "b.go", Symbols: []types.DiffSymbol{{ID: "m types/Diff#", Label: "Diff"}}},
	}}
	assert.Equal(t, []string{"a.go", "b.go"}, diffPaths(rev))
	assert.Equal(t, []string{"m types/Diff#"}, diffSymbolIDs(rev), "deduplicated, and an unindexed symbol is not an id")
}

// stubDiffSession is the daemon's /api/v1/diff/session route, holding every request until the
// test lets go.
//
// The holding is what makes the queue observable at all: a daemon that answers empties the
// queue as fast as a keyboard can fill it, and every decision in diffBridge is about the case
// where one has stopped.
type stubDiffSession struct {
	srv     *httptest.Server
	started chan struct{} // closed once the first request has reached the handler
	release chan struct{} // closed to let every held request return

	startOnce   sync.Once
	releaseOnce sync.Once

	mu  sync.Mutex
	got []diffSessionOp
}

func newStubDiffSession(t *testing.T) *stubDiffSession {
	t.Helper()
	s := &stubDiffSession{started: make(chan struct{}), release: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op diffSessionOp
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.got = append(s.got, op)
		s.mu.Unlock()
		s.startOnce.Do(func() { close(s.started) })
		<-s.release
	}))
	// Released before the server is shut down whatever the test did: Close waits for the
	// handlers, so a test that never unblocks would hang here rather than fail.
	t.Cleanup(func() {
		s.unblock()
		s.srv.Close()
	})
	return s
}

// unblock lets every held request return. Safe to call twice, because the cleanup calls it
// again after a test already has.
func (s *stubDiffSession) unblock() { s.releaseOnce.Do(func() { close(s.release) }) }

// bridge is a diffBridge pointed at the stub, skipping the attach round trip the queue has no
// part in.
func (s *stubDiffSession) bridge() *diffBridge {
	b := &diffBridge{addr: strings.TrimPrefix(s.srv.URL, "http://"), token: "stub"}
	b.start()
	return b
}

func (s *stubDiffSession) ops() []diffSessionOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]diffSessionOp(nil), s.got...)
}

func cursorHunks(ops []diffSessionOp) []int {
	var out []int
	for _, op := range ops {
		if op.Op == "cursor" {
			out = append(out, op.Hunk)
		}
	}
	return out
}

func viewedDigests(ops []diffSessionOp) []string {
	var out []string
	for _, op := range ops {
		if op.Op == "viewed" {
			out = append(out, op.Digest)
		}
	}
	return out
}

// TestDiffBridgeQueueKeepsTheNewestCursor pins which end of a full queue is dropped.
//
// A backlog means the daemon stopped keeping up, and the only cursor still worth sending is the
// last one - every entry behind it names somewhere the reader has already walked past. A plain
// non-blocking send drops the arriving op, which keeps exactly the stale ones and throws away
// the only true one.
func TestDiffBridgeQueueKeepsTheNewestCursor(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	// The first op is taken by the sender and held in the handler, which is what makes the rest
	// deterministic: nothing is receiving from the queue while they are pushed.
	b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: 0})
	<-stub.started

	const pushed = 20
	for i := 1; i <= pushed; i++ {
		b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: i})
	}
	stub.unblock()
	b.close()

	want := []int{0}
	for i := pushed - diffBridgeQueue + 1; i <= pushed; i++ {
		want = append(want, i)
	}
	assert.Equal(t, want, cursorHunks(stub.ops()),
		"the one in flight, then the newest diffBridgeQueue: the stale head is what gets evicted")
}

// TestDiffBridgeNeverDropsAReadMark pins the asymmetry the two queues exist for.
//
// A cursor is replaceable by the next one. A read mark is replaceable by nothing: the bridge is
// the only writer of it on this path, so a dropped `viewed` is a hunk the reader read that no
// client will ever be told about. Sharing one evicting queue made a fast reader's marks
// collateral damage of their own scrolling.
func TestDiffBridgeNeverDropsAReadMark(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: 0})
	<-stub.started

	// Far more cursor moves than the cursor queue holds, with marks interleaved: the overflow
	// that evicts one must not be able to reach the other.
	var want []string
	for i := 0; i < 40; i++ {
		b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: i + 1})
		if i%8 == 0 {
			d := fmt.Sprintf("digest-%d", i)
			want = append(want, d)
			b.SetViewed(d, true)
		}
	}
	stub.unblock()
	b.close()

	assert.Equal(t, want, viewedDigests(stub.ops()),
		"every mark, in order, however far behind the sender fell")
}

// TestDiffBridgeCloseDrainsAndTheSenderExits pins the quit path: a mark made on the last
// keypress leaves, and the goroutine that carried it is gone by the time close returns.
func TestDiffBridgeCloseDrainsAndTheSenderExits(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	b.SetViewed("d0", true)
	<-stub.started
	b.SetViewed("d1", true)
	b.SetViewed("d2", false)
	stub.unblock()

	b.close()
	select {
	case <-b.done:
	default:
		t.Fatal("the sender goroutine is still running after close returned")
	}
	assert.Equal(t, []string{"d0", "d1", "d2"}, viewedDigests(stub.ops()),
		"a mark made on the last keypress is not lost to the process exiting")
}

// TestDiffBridgeCloseGivesUpOnAWedgedDaemon is the other half of the same bargain: quitting is
// bounded whether or not anything answers, and the sender still exits.
//
// Nothing releases the handler here, so the post is on the wire for the whole test. Cancelling
// it is the only thing that ends the goroutine; abandoning it left one talking to the daemon
// about a session the reader had walked away from, with nobody to receive the answer.
func TestDiffBridgeCloseGivesUpOnAWedgedDaemon(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	b.SetViewed("d0", true)
	<-stub.started
	b.SetViewed("d1", true)

	start := time.Now()
	b.close()
	assert.Less(t, time.Since(start), diffBridgeClose+2*diffBridgeWrite,
		"a wedged daemon must not hold the shell")

	select {
	case <-b.done:
	case <-time.After(2 * diffBridgeWrite):
		t.Fatal("the sender goroutine outlived close")
	}
}

// TestDiffBridgeSendAfterCloseIsSafe pins why stop is closed instead of the queues: closing a
// channel the key loop can still send on turns a late keypress into a panic.
func TestDiffBridgeSendAfterCloseIsSafe(t *testing.T) {
	stub := newStubDiffSession(t)
	stub.unblock() // an answering daemon; this is about the queue, not the wire
	b := stub.bridge()

	b.SetViewed("d0", true)
	b.close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: 1})
		b.SetViewed("d1", true)
	}()
	select {
	case <-done:
	case <-time.After(2 * diffBridgeWrite):
		t.Fatal("a send after close blocked")
	}
}

// TestDiffNextStepLinesTeachTheWorkflow pins the pointers at the end of the report.
//
// This report is what a reader meets when the viewer stood aside - piped, redirected, in CI,
// or asked for with --no-tui - and it is the surface with the most readers. It used to name
// the console and nothing else, so every other way of reading a changeset existed only in
// `-h` prose and the man page: the best teaching in the product sat furthest from the door.
//
// It no longer teaches --tui, because there is no such flag to teach: at a terminal the
// viewer is what already happened.
func TestDiffNextStepLinesTeachTheWorkflow(t *testing.T) {
	got := strings.Join(diffNextStepLines(3), "\n")
	assert.Contains(t, got, "magus diff --impact")
	assert.Contains(t, got, "magus diff --no-tui")
	assert.NotContains(t, got, "--tui ", "there is no opt-in flag left to name")

	// Nothing to read is nothing to suggest: a clean tree or an all-generated changeset has
	// no reading to offer, and a pointer there suggests doing nothing.
	assert.Nil(t, diffNextStepLines(0))
}

// Wiring magus as GIT_EXTERNAL_DIFF used to dead-end on "takes at most one patch argument, got
// 7" - accurate, and useless to the person who just tried the integration. The refusal has to
// name the working setting, because reaching it means they are configuring git right now.
func TestGitExternalDiffIsRefusedWithTheSettingThatWorks(t *testing.T) {
	t.Setenv("GIT_DIFF_PATH_TOTAL", "2")
	seven := []string{"f.txt", "/tmp/blob/f.txt", "abc", "100644", "f.txt", "def", "100644"}

	_, err := scopeFromArgs(seven)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pager.diff")
	assert.Contains(t, err.Error(), "magus diff -")
	assert.Contains(t, err.Error(), "f.txt", "the file git was asking about")

	// Seven positionals with no git around are seven paths, which is now a legitimate scope.
	// Inventing a git explanation for them would send an ordinary caller to the wrong page.
	t.Setenv("GIT_DIFF_PATH_TOTAL", "")
	got, err := scopeFromArgs(seven)
	require.NoError(t, err)
	assert.Equal(t, seven, got)
}

// A VCS colorizes when it believes it is writing to a terminal, which is precisely the case
// when magus is its pager - so the first patch a reader ever hands over through the wiring in
// docs/guides/integrations/git.md is a colorized one. Its headers sit behind an escape
// sequence and no longer begin a line, so nothing parses, and "no headers magus can read" is
// true but sends them looking in the wrong place.
func TestAColorizedPatchNamesColorAsTheCause(t *testing.T) {
	colorized := "\x1b[0;1mdiff -r abc123 f.txt\x1b[0m\n" +
		"\x1b[0;31;1m--- a/f.txt\x1b[0m\n" +
		"\x1b[0;32;1m+++ b/f.txt\x1b[0m\n"
	assert.Empty(t, changedPathsFromPatch(colorized), "escape sequences hide the headers")
	assert.Contains(t, colorized, "\x1b[", "the detection this refusal keys on")
}

// recordingSync is the inner sync earnedSync wraps, so a test can assert the wrapper still
// forwards every mark: the daemon and the console read the reader's progress through it, and
// a wrapper that swallowed marks would desynchronize them silently.
type recordingSync struct {
	marks  []string
	closed bool
}

func (r *recordingSync) SetCursor(types.DiffCursor)       {}
func (r *recordingSync) SetViewed(digest string, on bool) { r.marks = append(r.marks, digest) }
func (r *recordingSync) close()                           { r.closed = true }

func earnedFixture(t *testing.T, files map[string]string) (root, cache string, tui []diff.File) {
	t.Helper()
	root, cache = t.TempDir(), t.TempDir()
	for path, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(body), 0o644))
	}
	return root, cache, tui
}

func tuiFile(path string, generated bool, digests ...string) diff.File {
	f := diff.File{Path: path, Generated: generated}
	for _, d := range digests {
		f.Hunks = append(f.Hunks, diff.Hunk{Digest: d})
	}
	return f
}

func TestEarnedSyncMintsWhenEveryHunkIsMarked(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	inner := &recordingSync{}
	e := newEarnedSync(inner, reviewedContent{root: root}, cache, []diff.File{tuiFile("a.go", false, "h1", "h2")}, nil)

	e.SetViewed("h1", true)
	e.close()
	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store, "one of two hunks read is not a file read")

	e = newEarnedSync(&recordingSync{}, reviewedContent{root: root}, cache, []diff.File{tuiFile("a.go", false, "h1", "h2")}, nil)
	e.SetViewed("h1", true)
	e.SetViewed("h2", true)
	e.close()
	store, err = review.Load(cache)
	require.NoError(t, err)
	assert.True(t, store.Covers("a.go", review.DigestFile(filepath.Join(root, "a.go"))))
}

// THE trust property. The seeded viewed set comes from an unauthenticated JSON file whose
// hunk digests are computable from `magus diff` output, so anything with write access can
// forge a complete reading. Without the live-mark rule, opening the viewer once would launder
// that forgery into durable receipts.
func TestEarnedSyncRefusesToMintFromASeededSetAlone(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	// Every hunk already "read", exactly as a forged store would present them.
	e := newEarnedSync(&recordingSync{}, reviewedContent{root: root}, cache,
		[]diff.File{tuiFile("a.go", false, "h1", "h2")}, []string{"h1", "h2"})
	e.close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store, "a seeded set with no live mark must mint nothing")
}

// ...but the seed still does its real job: a reader who finishes a file across two sittings
// earns it on the mark that completes it.
func TestEarnedSyncLetsASeededSetFinishALiveReading(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	e := newEarnedSync(&recordingSync{}, reviewedContent{root: root}, cache,
		[]diff.File{tuiFile("a.go", false, "h1", "h2")}, []string{"h1"})

	e.SetViewed("h2", true)
	e.close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.True(t, store.Covers("a.go", review.DigestFile(filepath.Join(root, "a.go"))),
		"the last hunk was marked in this session, so the reading was earned")
}

// Reading a machine's restatement of an edit made elsewhere is not the review, which is why
// the file list folds generated output away by default too.
func TestEarnedSyncIgnoresGeneratedFiles(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"gen.json": "{}\n"})
	e := newEarnedSync(&recordingSync{}, reviewedContent{root: root}, cache, []diff.File{tuiFile("gen.json", true, "g1")}, nil)

	e.SetViewed("g1", true)
	e.close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store)
}

// The wrapper is a side channel, not a replacement: the daemon and the console read the
// reader's progress through the inner sync, and swallowing a mark would desynchronize them.
func TestEarnedSyncForwardsEveryMarkAndClosesTheInnerSync(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	inner := &recordingSync{}
	e := newEarnedSync(inner, reviewedContent{root: root}, cache, []diff.File{tuiFile("a.go", false, "h1")}, nil)

	e.SetViewed("h1", true)
	e.SetViewed("unknown-digest", true)
	e.close()

	assert.Equal(t, []string{"h1", "unknown-digest"}, inner.marks)
	assert.True(t, inner.closed, "the wrapped sync must still be shut down")
}

// A file that cannot be fingerprinted records nothing rather than a receipt against no
// content, which Covers would then satisfy for every unreadable file forever.
func TestEarnedSyncMintsNothingForAFileItCannotRead(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	e := newEarnedSync(&recordingSync{}, reviewedContent{root: root}, cache, []diff.File{tuiFile("gone.go", false, "h1")}, nil)
	e.now = func() time.Time { return time.Unix(0, 0) }

	e.SetViewed("h1", true)
	e.close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store)
}

func TestCompatUntil(t *testing.T) {
	cases := []struct{ in, want string }{
		{"compat(until: no store still serves ed25519 envelopes): sigAlg is", "no store still serves ed25519 envelopes"},
		{"compat(until: console reads the new keys)", "console reads the new keys"},
		// A condition that wraps onto the next line is truncated rather than dropped: half
		// of it still says what KIND of thing would retire the code.
		{"compat(until: no install still carries the pre-rename file - the", "no install still carries the pre-rename file - the"},
		{"compat(until: )", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, compatUntil(c.in), c.in)
	}
}

func TestCollectRationale(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	write("a.go", "package a\n\n// compat(until: no store holds v1 descriptors): keep the branch.\nfunc F() {}\n")
	write("b.go", "package b\n\nfunc G() {}\n")
	write("gen/c.go", "package gen\n\n// compat(until: whatever): generated.\n")

	rev := types.Diff{Files: []types.DiffFile{
		{Path: "a.go", Role: types.DiffRoleSource},
		{Path: "b.go", Role: types.DiffRoleSource},
		{Path: "gen/c.go", Role: types.DiffRoleOutput},
	}}

	got := collectRationale(root, rev)
	require.Len(t, got, 1)
	assert.Equal(t, "a.go", got[0].Path)
	assert.Equal(t, 3, got[0].Line)
	assert.Equal(t, "no store holds v1 descriptors", got[0].Until)
}

func TestImpactRationaleLines(t *testing.T) {
	t.Run("empty says nothing was marked, not nothing was checked", func(t *testing.T) {
		lines := impactRationaleLines(nil)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "no compat(until:) marker")
	})

	t.Run("names the file, the line, and the condition", func(t *testing.T) {
		lines := impactRationaleLines([]rationaleHit{{Path: "a.go", Line: 12, Until: "no store holds v1"}})
		assert.Contains(t, lines[0], "1 compat(until:) marker")
		assert.Equal(t, "      a.go:12 until no store holds v1", lines[1])
	})

	// The marker appears as a bare token in this tool's own source, in doc comments naming
	// the convention, and in lint patterns. Matching those would put the reporting code at
	// the top of its own report.
	t.Run("a bare mention of the marker is not a decision", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"),
			[]byte("package a\n\nconst marker = \"compat(until:\"\n// the compat(until:) convention\n"), 0o644))

		got := collectRationale(root, types.Diff{
			Files: []types.DiffFile{{Path: "a.go", Role: types.DiffRoleSource}},
		})
		assert.Empty(t, got)
	})

	t.Run("the list is capped", func(t *testing.T) {
		var hits []rationaleHit
		for i := range rationaleShown + 3 {
			hits = append(hits, rationaleHit{Path: "a.go", Line: i, Until: "x"})
		}
		lines := impactRationaleLines(hits)
		assert.Contains(t, lines[rationaleShown+1], "and 3 more")
	})
}

// The scanner used to report its own constant - `const compatMarker = "compat(until: "` -
// as a decision governing the reader's change, alongside its own test fixtures, rendering
// a line whose condition was a bare quote character. The file comment claimed the marker's
// trailing space prevented this; it cannot, because the constant contains that space too.
func TestAMarkerInAStringLiteralIsNotADecision(t *testing.T) {
	assert.False(t, inAComment(`const compatMarker = "`), "the scanner's own constant")
	assert.False(t, inAComment(`		{"`), "a table-driven fixture")

	assert.True(t, inAComment("\t// "), "a line comment")
	assert.True(t, inAComment("// "), "a line comment at column 0")
	assert.True(t, inAComment("  * "), "a block comment continuation")
	assert.True(t, inAComment("# "), "a shell or yaml comment")
}

// stubAdviceDir builds an advisor directory holding the REAL advice.buzz plus the given
// stubs, so a test exercises the shipped collect sink rather than a re-implementation of
// it. The stubs stand in for the advisors themselves, whose findings depend on a git
// history and a loaded workspace.
func stubAdviceDir(t *testing.T, stubs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("..", "..", adviceDirRel, "advice.buzz"))
	if err != nil {
		t.Fatalf("read advice.buzz: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "advice.buzz"), src, 0o600); err != nil {
		t.Fatalf("write advice.buzz: %v", err)
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// echoAdvisor publishes the local env contract back out, so a test can assert what
// reached the advisor rather than what the driver believes it sent.
const echoAdvisor = `import "advice";
import "std";

fun main() > void !> any {
    std\print("echo: looked at the tree");
    advice\publish(advice\env("REPO"), pr: advice\env("PR_NUMBER"), name: "echo",
        title: advice\env("PR_HEAD_SHA"), body: advice\env("PR_BASE"));
}
`

// warningAdvisor trips BZZ3002 (string rebuilt in a loop) and still publishes, the shape
// of every shipped advisor that lints imperfectly but runs.
const warningAdvisor = `import "advice";

fun main() > void !> any {
    var text = "";
    foreach (part in ["still", "standing"]) {
        text = text + part;
    }
    advice\publish("", pr: "", name: "warned", title: "Ran anyway", body: text);
}
`

const retractingAdvisor = `import "advice";

fun main() > void !> any {
    advice\publish("", pr: "", name: "quiet", title: "Nothing to report", body: "");
}
`

// rangeAdvisor publishes the rev spec diffRange handed it. Only the driver can put an
// advisor into local mode (advice.buzz decides on os\env), so the local half of the range
// contract is only reachable from here.
const rangeAdvisor = `import "advice";

fun main() > void !> any {
    advice\publish("", pr: "", name: "range", title: "Range",
        body: advice\diffRange(advice\env("PR_BASE"), head: advice\env("PR_HEAD_SHA")));
}
`

const brokenAdvisor = `fun main() > void !> any {
    throw "broken on purpose";
}
`

// staleBaseAdvisor exercises the half of the local-base contract no Buzz test can reach:
// advice.buzz decides on os\env, and os\with_env does not touch this process's own
// environment, so only a driver that really sets the variable can put an advisor into
// local mode.
//
// The base is one no remote can resolve. In CI mode fetchBase runs the refspec fetch,
// git fails to find the ref, and fetchBase throws - so merely reaching publish is the
// proof that the local-mode gate returned before any git ran.
const staleBaseAdvisor = `import "advice";

fun main() > void !> any {
    advice\fetchBase("magus-advice-no-such-base", refspec: true);
    advice\publish("", pr: "", name: "stale", title: "Stale base", body: advice\env("PR_BASE"));
}
`

func TestCollectAdviceEmitsSectionsAndSkipsTheForge(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"echo.buzz": echoAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"echo.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 0 {
		// Compile warnings are notes too, so a note here may be a BZZ diagnostic raised by
		// the shipped advice.buzz rather than anything the stub did.
		t.Fatalf("notes = %v, want none", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	got := sections[0]
	if got.Name != "echo" {
		t.Errorf("Name = %q, want %q", got.Name, "echo")
	}
	// The driver's base reaches the advisor as PR_BASE, which is the whole point of the
	// shared env helper's local mode.
	if got.Body != "main" {
		t.Errorf("Body = %q, want the base %q", got.Body, "main")
	}
	// PR_HEAD_SHA is supplied rather than left empty; an empty one makes every advisor
	// read the run as "not a pull request" and say nothing at all.
	if got.Title == "" {
		t.Error("Title is empty: PR_HEAD_SHA was not supplied to the advisor")
	}
}

func TestCollectAdviceKeepsARetraction(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"quiet.buzz": retractingAdvisor})

	sections, _, err := collectAdvice(context.Background(), dir, []string{"quiet.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(sections) != 1 || sections[0].Name != "quiet" || sections[0].Body != "" {
		t.Fatalf("sections = %+v, want one empty-bodied \"quiet\" section", sections)
	}
}

func TestCollectAdviceSurvivesABrokenAdvisor(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{
		"echo.buzz":   echoAdvisor,
		"broken.buzz": brokenAdvisor,
		"quiet.buzz":  retractingAdvisor,
	})
	// Broken in the MIDDLE: a failure that stops the run would take the third advisor
	// with it and look identical to one that had nothing to say.
	order := []string{"echo.buzz", "broken.buzz", "quiet.buzz"}

	sections, notes, err := collectAdvice(context.Background(), dir, order, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one", notes)
	}
	if !strings.Contains(notes[0], "broken.buzz") || !strings.Contains(notes[0], "broken on purpose") {
		t.Errorf("note = %q, want it to name the advisor and the error", notes[0])
	}
	// The renderer prints notes verbatim, so an unstamped failure would read as a
	// warning from an advisor that ran.
	if !strings.HasPrefix(notes[0], "could not run: ") {
		t.Errorf("note = %q, want the could-not-run stamp on a failure", notes[0])
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %+v, want the two working advisors", sections)
	}
	if sections[0].Name != "echo" || sections[1].Name != "quiet" {
		t.Errorf("sections = %+v, want them in the order they were listed", sections)
	}
}

// A warning from an advisor that RAN is not the reader's business.
//
// These are lint diagnostics about the advisor's own source - magus's shipped scripts, not
// anything in the changeset. Printing them unconditionally is what made a one-line docs fix
// draw ~40 lines of BZZ3001/BZZ3002 about merge-conflict.buzz and doctor.buzz; four
// personas hit it independently and the drive-by contributor named it as the point they
// nearly abandoned the repo, assuming they had broken something.
func TestCollectAdviceDropsWarningsFromAnAdvisorThatRan(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"warned.buzz": warningAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"warned.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want the advisor's finding: it ran", sections)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none: the advisor ran, so its own lint is not the reader's business", notes)
	}
}

// ...but when the advisor CRASHED, its warnings are kept and ordered above the failure: a
// BZZ3001 over a crash is usually the explanation for it, which is the whole reason they
// were ever collected.
func TestCollectAdviceKeepsWarningsFromAnAdvisorThatFailed(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"broken.buzz": brokenAdvisor})

	_, notes, err := collectAdvice(context.Background(), dir, []string{"broken.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) == 0 {
		t.Fatal("want the failure surfaced as a note")
	}
	last := notes[len(notes)-1]
	if !strings.HasPrefix(last, "could not run: ") {
		t.Errorf("last note = %q, want the could-not-run stamp last, after any warnings", last)
	}
}

// TestLocalModeDiffsTheWorkingTree pins the half of the range contract advice.buzz cannot
// reach: only a driver that really sets the mode variable puts an advisor into local mode.
//
// The assertion is the REV the advisors are handed, not the diff it produces, because the
// behaviour being bought is git's: `git diff <commit>` with no second rev compares the
// WORKING TREE against that commit, where `base...head` stops at the last commit. Choosing
// the merge base is this code's decision and the only part worth pinning.
func TestLocalModeDiffsTheWorkingTree(t *testing.T) {
	// The advisors run git against the process working directory, which under `go test` is
	// this package inside magus's own repository - a real clone with a real history.
	out, err := exec.Command("git", "merge-base", "origin/main", "HEAD").Output()
	if err != nil {
		t.Skip("origin/main is not in this clone, which is the fallback path below, not this one")
	}
	want := strings.TrimSpace(string(out))

	dir := stubAdviceDir(t, map[string]string{"range.buzz": rangeAdvisor})
	sections, notes, err := collectAdvice(context.Background(), dir, []string{"range.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	if got := sections[0].Body; got != want {
		t.Errorf("range = %q, want the merge base %q: a three-dot range would stop at the "+
			"last commit and report the uncommitted change as absent", got, want)
	}
}

// TestLocalModeReadsAStaleBaseWithoutFetching pins the rule that a local advice run stays
// off the network. `magus diff` is a read-only report on a working tree - it may run
// offline, and under --watch it re-fires on every save - so fetching, and writing
// refs/remotes/ while doing it, is a report mutating what it reports on.
func TestLocalModeReadsAStaleBaseWithoutFetching(t *testing.T) {
	dir := stubAdviceDir(t, map[string]string{"stale.buzz": staleBaseAdvisor})

	sections, notes, err := collectAdvice(context.Background(), dir, []string{"stale.buzz"}, "main")
	if err != nil {
		t.Fatalf("collectAdvice: %v", err)
	}
	// A note here IS the defect: fetchBase reached git, git could not resolve the refspec,
	// and it threw. On the correct path no git runs at all.
	if len(notes) != 0 {
		t.Fatalf("notes = %v, want none: fetchBase went to the network during a local run", notes)
	}
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one", sections)
	}
	// The advisor saw the driver's base rather than a pull request's. Saying that the base
	// went unfetched is the DRIVER's line, once for the whole set - see
	// TestImpactBaseSeparatesOldFromAbsent - so no section carries the disclaimer.
	if body := sections[0].Body; body != "main" {
		t.Errorf("Body = %q, want the local base the driver supplied", body)
	}
}

func TestParseAdviceSectionsDropsProgressLines(t *testing.T) {
	out := "unclaimed: advised on a, b\n" +
		`{"name":"unclaimed","title":"Files no project claims (2)","body":"a\nb"}` + "\n" +
		"not json at all\n" +
		`{"title":"no name","body":"x"}` + "\n"

	got := parseAdviceSections(out)
	if len(got) != 1 {
		t.Fatalf("got %+v, want only the one named section", got)
	}
	if got[0].Body != "a\nb" {
		t.Errorf("Body = %q, want the escaped newline decoded", got[0].Body)
	}
}

func TestSetAdviceEnvRestoresAbsence(t *testing.T) {
	t.Setenv(adviceModeEnv, "")
	if err := os.Unsetenv(adviceModeEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	restore, err := setAdviceEnv("main")
	if err != nil {
		t.Fatalf("setAdviceEnv: %v", err)
	}
	if got := os.Getenv(adviceModeEnv); got != adviceModeLocal {
		t.Fatalf("%s = %q, want %q", adviceModeEnv, got, adviceModeLocal)
	}
	restore()
	// Absent, not empty: advice.buzz tells the two apart, so restoring to "" would leave
	// a later reader looking at a variable this call invented.
	if _, ok := os.LookupEnv(adviceModeEnv); ok {
		t.Errorf("%s is still set after restore", adviceModeEnv)
	}
}

// adviceLocalExclusions are read-only advisors action.yml runs that `magus diff`
// deliberately does not. The value is the reason, and carrying one is the point: leaving
// an advisor out is a decision, and a decision with no reason recorded is indistinguishable
// from having forgotten it.
var adviceLocalExclusions = map[string]string{
	"first-contribution.buzz": "reads the pull request's author through its own gh call " +
		"rather than through advice.buzz, so local mode cannot intercept it, and a working " +
		"tree has no first-time contributor to welcome",
}

// adviceStep is one step of the advice composite action, reduced to the two facts this
// test needs: the advisor it runs, and the environment keys it sets.
type adviceStep struct {
	name    string
	file    string
	envKeys map[string]bool
}

// parseAdviceSteps reads the advice composite action and returns its advisor steps in the
// order action.yml declares them.
//
// A LINE SCAN rather than a YAML decode, which is a judgment worth recording. Decoding
// would mean modeling enough of the composite-action schema to reach `runs.steps[].env`
// and `.run`, and `run` would still be a shell string this test has to pick a script path
// out of by hand - so the schema buys nothing and the sub-parse remains either way. It
// would also put a YAML dependency in package main to serve one test. The scan reads the
// same two facts a human reads, off a file whose indentation the action schema fixes:
// steps open at `    - name:`, step keys sit at six spaces, env keys at eight.
//
// The scan is allowed to be wrong in one direction only. A step it fails to recognize
// drops out of the returned set and then surfaces as a mismatch against localAdvisors,
// which is a red test naming the file - never a quietly shorter list.
func parseAdviceSteps(t *testing.T) []adviceStep {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", adviceDirRel, "action.yml"))
	if err != nil {
		t.Fatalf("read action.yml: %v", err)
	}

	var steps []adviceStep
	var cur *adviceStep
	inEnv := false
	flush := func() {
		if cur != nil && cur.file != "" {
			steps = append(steps, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "    - name: "):
			flush()
			cur = &adviceStep{
				name:    strings.TrimSpace(strings.TrimPrefix(line, "    - name: ")),
				envKeys: map[string]bool{},
			}
			inEnv = false
		case cur == nil:
			// Everything ahead of the first step: the action's description and inputs.
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			// Blank lines and comments say nothing about where the scan is.
		case inEnv && strings.HasPrefix(line, "        "):
			if key, _, ok := strings.Cut(trimmed, ":"); ok {
				cur.envKeys[key] = true
			}
		case trimmed == "env:":
			inEnv = true
		case strings.HasPrefix(line, "      run: "):
			inEnv = false
			run := strings.TrimPrefix(line, "      run: ")
			_, after, ok := strings.Cut(run, "$GITHUB_ACTION_PATH/")
			if !ok {
				// A step invoking an advisor some other way would evade the scan
				// entirely, which is the one failure this design cannot absorb.
				if strings.Contains(run, ".buzz") {
					t.Errorf("step %q runs a .buzz script the scan cannot name: %q", cur.name, run)
				}
				continue
			}
			cur.file, _, _ = strings.Cut(after, `"`)
		default:
			inEnv = false
		}
	}
	flush()
	return steps
}

// TestLocalAdvisorsMatchActionYML is the gate on localAdvisors restating action.yml by
// hand. A read-only advisor added to CI that never reaches `magus diff` is invisible
// otherwise: both halves keep working, and the local command is simply quieter than the
// pull request for no stated reason.
func TestLocalAdvisorsMatchActionYML(t *testing.T) {
	steps := parseAdviceSteps(t)

	// A step carrying FIX_LABEL is a WRITER. That variable is the per-change consent the
	// two fixers and the label-settler each read before touching the branch, so it is a
	// structural signal action.yml already carries, rather than a second hand-kept list of
	// writers that could drift exactly the way localAdvisors can. FIX_LABEL_OFFER - which
	// the read-only merge-conflict advisor sets - is a different key and does not match.
	var readOnly []string
	writers := 0
	for _, s := range steps {
		if s.envKeys["FIX_LABEL"] {
			writers++
			continue
		}
		readOnly = append(readOnly, s.file)
	}
	if len(readOnly) == 0 || writers == 0 {
		t.Fatalf("the scan found %d steps, %d read-only and %d writers: it is measuring "+
			"nothing, and every comparison below would pass on an empty file",
			len(steps), len(readOnly), writers)
	}

	want := make([]string, 0, len(readOnly))
	for _, file := range readOnly {
		if _, excluded := adviceLocalExclusions[file]; !excluded {
			want = append(want, file)
		}
	}
	for file := range adviceLocalExclusions {
		if !slices.Contains(readOnly, file) {
			t.Errorf("adviceLocalExclusions names %q, which action.yml no longer runs as a "+
				"read-only advisor: drop the entry, since the exclusion now protects nothing", file)
		}
	}
	if slices.Equal(localAdvisors, want) {
		return
	}

	mismatched := false
	for _, file := range want {
		if !slices.Contains(localAdvisors, file) {
			mismatched = true
			t.Errorf("action.yml runs read-only advisor %q and `magus diff` does not. Add it "+
				"to localAdvisors, or name it in adviceLocalExclusions with the reason it has "+
				"no local meaning. If it WRITES it belongs in neither: a writer is recognized "+
				"here by the FIX_LABEL consent variable on its step, and one that pushes "+
				"without reading that label is a bug in the advisor, not in this test.", file)
		}
	}
	for _, file := range localAdvisors {
		if !slices.Contains(want, file) {
			mismatched = true
			t.Errorf("localAdvisors runs %q, which action.yml does not run as a read-only "+
				"advisor: it was renamed, removed, or has become a writer.", file)
		}
	}
	if !mismatched {
		// Same members either way, so only the order moved. It is not cosmetic: a local
		// reader gets the findings in the order CI chose to present them.
		t.Errorf("localAdvisors = %v, want action.yml's order %v", localAdvisors, want)
	}
}

// reviewFixture plants files in a working tree and returns the tree, a cache dir, and the
// changeset naming them.
func reviewFixture(t *testing.T, files map[string]string, roles map[string]string) (string, string, types.Diff) {
	t.Helper()
	root, cache := t.TempDir(), t.TempDir()

	var rev types.Diff
	for path, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))

		role := types.DiffRoleSource
		if r, ok := roles[path]; ok {
			role = r
		}
		rev.Files = append(rev.Files, types.DiffFile{Path: path, Role: role})
	}
	return root, cache, rev
}

// attach folds the receipt store onto the changeset the way annotateDiff does, so these
// tests exercise the join the CLI and the console both go through rather than a second one.
func attach(t *testing.T, root, cache string, rev types.Diff) types.Diff {
	t.Helper()
	states, err := review.ReadStates(cache, diffPaths(rev), reviewedContent{root: root}.digest)
	require.NoError(t, err)
	rev.AttachReadState(states)
	return rev
}

func TestCollectReview(t *testing.T) {
	t.Run("an unacknowledged changeset reports every file unread", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"}, nil)

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 2, got.Files)
		assert.Equal(t, 0, got.Read)
		assert.Len(t, got.Unread, 2)
	})

	t.Run("acknowledging then re-reading reports it read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n"}, nil)

		n, err := ackChangeset(reviewedContent{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 1, got.Read)
		assert.Empty(t, got.Unread)
	})

	// The property the whole feature turns on: acknowledging code and then changing it must
	// not leave the change looking reviewed.
	t.Run("editing after acknowledging goes stale, not read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"}, nil)
		_, err := ackChangeset(reviewedContent{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc G() {}\n"), 0o644))

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 0, got.Read)
		// Stale is its own list, not an annotated entry in the unopened one: it is a
		// different finding and it leads the section.
		assert.Equal(t, []string{"a.go"}, got.Stale)
		assert.Empty(t, got.Unread)
	})

	// Reading a machine's restatement of an edit made elsewhere is not the review, which is
	// why the file list folds generated output away by default too.
	t.Run("generated output is not something to have read", func(t *testing.T) {
		root, cache, rev := reviewFixture(t,
			map[string]string{"a.go": "package a\n", "gen/x.go": "package gen\n"},
			map[string]string{"gen/x.go": types.DiffRoleOutput})

		got := collectReview(attach(t, root, cache, rev), nil, nil)
		require.NotNil(t, got)
		assert.Equal(t, 1, got.Files)

		n, err := ackChangeset(reviewedContent{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, n)
	})

	// A file nothing could fingerprint carries no state, and a changeset of only those was
	// not measured. Reporting it as unread would accuse somebody of skipping a deletion.
	t.Run("a deleted file is unmeasured, not unread", func(t *testing.T) {
		root, cache := t.TempDir(), t.TempDir()
		rev := types.Diff{Files: []types.DiffFile{{Path: "gone.go", Role: types.DiffRoleSource}}}

		n, err := ackChangeset(reviewedContent{root: root}, cache, rev, "spot-checked", time.Now())
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		assert.Nil(t, collectReview(attach(t, root, cache, rev), nil, nil))
	})
}

func TestImpactReviewLines(t *testing.T) {
	// The empty form is the half that matters: a silent section reads as a clean bill of
	// health, and here that would mean "somebody read this".
	t.Run("unmeasured says so rather than saying unread", func(t *testing.T) {
		lines := impactReviewLines(nil)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "unavailable")
	})

	t.Run("names the unopened files and caps the list", func(t *testing.T) {
		r := &impactReview{Files: 30, Read: 0}
		for i := range 30 {
			r.Unread = append(r.Unread, fmt.Sprintf("f%d.go", i))
		}
		lines := impactReviewLines(r)
		assert.Contains(t, lines[0], "30 file(s) you have not opened")
		assert.Contains(t, lines[unreadShown+1], "and 20 more")
	})

	// No ratio, anywhere. A count with a target is one that gets cleared rather than
	// satisfied, and clearing it takes one keystroke and no reading.
	t.Run("reports no read ratio", func(t *testing.T) {
		lines := impactReviewLines(&impactReview{
			Files: 40, Read: 12,
			Stale:  []string{"internal/cache/key.go"},
			Unread: []string{"a.go", "b.go"},
		})
		for _, l := range lines {
			assert.NotContains(t, l, " of 40")
			assert.NotContains(t, l, "12")
		}
	})

	// Stale is the finding, so it leads whatever else the section holds: it is derived
	// from content rather than from a claim, so no amount of stamping produces it.
	t.Run("stale leads and is named", func(t *testing.T) {
		lines := impactReviewLines(&impactReview{
			Files: 4, Read: 1,
			Stale:  []string{"internal/cache/key.go"},
			Unread: []string{"a.go"},
		})
		assert.Contains(t, lines[0], "1 file(s) changed after you read them")
		assert.Equal(t, "      internal/cache/key.go", lines[1])
	})

	// A small undisturbed change needs no reading plan, and printing one there is how a
	// reader learns to skip the section before meeting a change big enough to need it.
	t.Run("says nothing about a small undisturbed change", func(t *testing.T) {
		assert.Nil(t, impactReviewLines(&impactReview{
			Files: 3, Unread: []string{"a.go", "b.go", "c.go"},
		}))
	})

	// ...but a small change where something moved under the reader is exactly when the
	// section earns its line.
	t.Run("a small change still reports stale", func(t *testing.T) {
		lines := impactReviewLines(&impactReview{Files: 2, Stale: []string{"a.go"}})
		require.NotEmpty(t, lines)
		assert.Contains(t, lines[0], "changed after you read them")
	})

	t.Run("says nothing when everything has been read", func(t *testing.T) {
		assert.Nil(t, impactReviewLines(&impactReview{Files: 30, Read: 30}))
	})
}

// TestReviewRequiredMatcher pins the scoping rule: globs are declared per project and
// matched against paths relative to it, so a project names its own files the same way its
// sources and outputs do.
func TestReviewRequiredMatcher(t *testing.T) {
	ws := &stubReviewWorkspace{projects: []*types.Project{
		{Path: ".", ReviewRequired: []string{"internal/secret/**"}},
		{Path: "console", ReviewRequired: []string{"src/auth/*.ts"}},
	}}
	match := reviewRequiredMatcher(ws)
	require.NotNil(t, match)

	assert.True(t, match("internal/secret/value.go"))
	assert.True(t, match("console/src/auth/token.ts"))
	assert.False(t, match("internal/cache/key.go"))
	// The console's glob must not reach outside the project that declared it.
	assert.False(t, match("src/auth/token.ts"))
}

// A workspace declaring nothing gets no matcher, which the report reads as "single nothing
// out" rather than as "everything matters".
func TestReviewRequiredMatcherNilWhenUndeclared(t *testing.T) {
	ws := &stubReviewWorkspace{projects: []*types.Project{{Path: "."}}}
	assert.Nil(t, reviewRequiredMatcher(ws))
}

func TestCollectReviewSeparatesRequiredPaths(t *testing.T) {
	root, cache, rev := reviewFixture(t, map[string]string{
		"internal/secret/value.go": "package secret\n",
		"internal/cache/key.go":    "package cache\n",
	}, nil)

	got := collectReview(attach(t, root, cache, rev), func(p string) bool {
		return strings.HasPrefix(p, "internal/secret/")
	}, nil)
	require.NotNil(t, got)
	assert.Equal(t, []string{"internal/secret/value.go"}, got.Required)
	assert.Len(t, got.Unread, 2)
}

// A bulk cover is echoed so a file stamped in one keystroke does not read as one somebody
// sat down with. It is a note the reader left themselves, not a toll they paid.
func TestImpactReviewLinesReportsBulkReasons(t *testing.T) {
	lines := impactReviewLines(&impactReview{
		Files: 8, Read: 4,
		Unread:  []string{"a.go"},
		Reasons: []string{"codemod output, spot-checked 3 of 40"},
	})
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "covered in bulk")
	assert.Contains(t, joined, "codemod output")
}

func TestImpactReviewLinesListsRequiredUncapped(t *testing.T) {
	r := &impactReview{Files: 30, Read: 0}
	for i := range unreadShown + 5 {
		r.Required = append(r.Required, fmt.Sprintf("internal/secret/f%d.go", i))
	}
	r.Unread = append([]string{}, r.Required...)
	lines := impactReviewLines(r)
	assert.Contains(t, lines[0], "15 unopened in review_required paths")
	// Uncapped, unlike the general list: the workspace said these cost something.
	assert.Contains(t, lines[15], "internal/secret/f14.go")
}

// A path the workspace flagged is named once, under review_required, and not again in the
// general list. Saying it twice reads as two findings.
func TestImpactReviewLinesDoesNotRepeatRequiredPaths(t *testing.T) {
	lines := impactReviewLines(&impactReview{
		Files:    8,
		Required: []string{"internal/secret/value.go"},
		Unread:   []string{"internal/secret/value.go", "a.go"},
	})
	joined := strings.Join(lines, "\n")
	assert.Equal(t, 1, strings.Count(joined, "internal/secret/value.go"))
	assert.Contains(t, joined, "1 file(s) you have not opened")
}

// stubReviewWorkspace is the narrow slice of the reader the matcher uses.
type stubReviewWorkspace struct {
	types.WorkspaceReader
	projects []*types.Project
}

func (s *stubReviewWorkspace) All() []*types.Project { return s.projects }

// TestScopeAck is the editor-freedom path: a reader who read three files in vim records
// those three, without claiming the other thirty and without opening magus's viewer.
func TestScopeAck(t *testing.T) {
	rev := types.Diff{Files: []types.DiffFile{
		{Path: "a.go", Role: types.DiffRoleSource},
		{Path: "b.go", Role: types.DiffRoleSource},
		{Path: "c.go", Role: types.DiffRoleSource},
	}}

	t.Run("no paths covers the whole changeset", func(t *testing.T) {
		got, err := scopeAck(rev, nil)
		require.NoError(t, err)
		assert.Len(t, got.Files, 3)
	})

	t.Run("named paths narrow it", func(t *testing.T) {
		got, err := scopeAck(rev, []string{"a.go", "c.go"})
		require.NoError(t, err)
		require.Len(t, got.Files, 2)
		assert.Equal(t, "a.go", got.Files[0].Path)
		assert.Equal(t, "c.go", got.Files[1].Path)
	})

	t.Run("a leading ./ still matches", func(t *testing.T) {
		got, err := scopeAck(rev, []string{"./a.go"})
		require.NoError(t, err)
		require.Len(t, got.Files, 1)
	})

	// A typo that quietly acknowledged nothing would leave the reader believing they had
	// recorded work they had not.
	t.Run("a path outside the changeset is an error, not a no-op", func(t *testing.T) {
		_, err := scopeAck(rev, []string{"a.go", "nope.go"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a changed file")
	})
}

// diffReach builds the pointer DiffFile.Reach wants. Reach is a pointer precisely so an
// unindexed workspace cannot serve `reach: 0` on every file, so a fixture has to opt in.
func diffReach(n int) *int { return &n }

// TestDiffFileFactsSaysWhatWasMeasured pins the one-claim-per-line contract, and with it
// the distinction the nil pointers exist to preserve: an unmeasured field prints NOTHING
// rather than a zero. A "0 files reference" line reads as "nothing depends on this", which
// is the opposite of "nobody looked".
func TestDiffFileFactsSaysWhatWasMeasured(t *testing.T) {
	t.Run("nothing measured yields no facts", func(t *testing.T) {
		assert.Empty(t, diffFileFacts(types.DiffFile{Path: "a.go", Surface: types.DiffSurfaceUnknown}))
	})

	t.Run("zero reach is not a fact", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{Path: "a.go", Reach: diffReach(0)})
		assert.Empty(t, facts)
	})

	t.Run("singular nouns agree with their counts", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:  "a.go",
			Reach: diffReach(1),
			Churn: &types.DiffChurn{Commits: 1},
		})
		assert.Contains(t, facts, "1 file reference its widest changed symbol")
		assert.Contains(t, facts, "changed in 1 commit")
	})

	t.Run("public surface names the exports when it knows them", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:    "a.go",
			Surface: types.DiffSurfacePublic,
			Symbols: []types.DiffSymbol{{Label: "Open", ModuleAPI: true}, {Label: "Close", ModuleAPI: true}},
		})
		require.NotEmpty(t, facts)
		assert.Equal(t, "PUBLIC SURFACE: exports Open, Close", facts[0])
	})

	t.Run("public surface falls back to the consuming projects", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:    "a.go",
			Surface: types.DiffSurfacePublic,
			Symbols: []types.DiffSymbol{
				{Label: "Open", ExternalProjects: []string{"web", "api"}},
				{Label: "Close", ExternalProjects: []string{"web"}},
			},
		})
		require.NotEmpty(t, facts)
		assert.Equal(t, "PUBLIC SURFACE: used by web, api", facts[0])
	})

	t.Run("public surface with no evidence still says public", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{Path: "a.go", Surface: types.DiffSurfacePublic})
		require.NotEmpty(t, facts)
		assert.Equal(t, "PUBLIC SURFACE", facts[0])
	})

	t.Run("a hot rising file says so, and an unranked one keeps its commit count", func(t *testing.T) {
		hot := diffFileFacts(types.DiffFile{
			Path:    "a.go",
			Project: "core",
			Churn:   &types.DiffChurn{Commits: 40, Authors: 6, Rank: 3, ProjectTrend: 12},
		})
		assert.Contains(t, hot, "changed in 40 commits by 6 people, hotspot #3 AND RISING - worth asking why it keeps changing")
		assert.Contains(t, hot, "in core")

		cold := diffFileFacts(types.DiffFile{
			Path:  "a.go",
			Churn: &types.DiffChurn{Commits: 40, Rank: types.NotableRankCutoff + 1, ProjectTrend: 12},
		})
		assert.Contains(t, cold, "changed in 40 commits")
		for _, f := range cold {
			assert.NotContains(t, f, "hotspot")
			assert.NotContains(t, f, "RISING")
		}
	})

	t.Run("coverage rounds and no history is stated plainly", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:      "a.go",
			Coverage:  &types.ImpactCoverage{Ratio: 0.675, Covered: 27, Total: 40},
			NoHistory: true,
		})
		assert.Contains(t, facts, "68% covered")
		assert.Contains(t, facts, "NO HISTORY - nothing has exercised this yet")
	})

	t.Run("zero total coverage is not zero percent", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{Path: "a.go", Coverage: &types.ImpactCoverage{}})
		assert.Empty(t, facts)
	})
}

// TestCapSliceReportsTheRemainder guards the property the helper exists for: a list that
// stops without saying so reads as the whole answer.
func TestCapSliceReportsTheRemainder(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, capSlice([]string{"a", "b"}, 4))
	assert.Equal(t, []string{"a", "b", "and 2 more"}, capSlice([]string{"a", "b", "c", "d"}, 2))

	// The cap must not write through the caller's backing array; the full slice is still
	// needed by -o json, which renders the same DiffFile.
	full := []string{"a", "b", "c", "d"}
	_ = capSlice(full, 2)
	assert.Equal(t, []string{"a", "b", "c", "d"}, full)
}

func TestChangedPathsFromPatchReadsTheHeaders(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/cmd/magus/diff.go b/cmd/magus/diff.go",
		"index 111..222 100644",
		"--- a/cmd/magus/diff.go",
		"+++ b/cmd/magus/diff.go",
		"+// a change",
		"diff --git a/docs/graph.json b/docs/graph.json",
		"diff --git a/cmd/magus/diff.go b/cmd/magus/diff.go",
		"",
	}, "\n")

	assert.Equal(t, []string{"cmd/magus/diff.go", "docs/graph.json"}, changedPathsFromPatch(patch))
	assert.Empty(t, changedPathsFromPatch(""))
}

// A header with no a//b prefixes is not malformed - it is what `git diff --no-prefix` emits,
// and diff.noPrefix is a setting plenty of people turn on. This used to be pinned the other
// way, as a line to skip, so such a patch reported ZERO changed files at exit 0. The reader
// falls back to a whitespace split there, which cannot handle a path containing spaces but
// names the file rather than dropping it.
func TestNoPrefixHeadersStillNameTheirFiles(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git f.txt renamed.txt",
		"similarity index 100%",
		"rename from f.txt",
		"rename to renamed.txt",
		"",
	}, "\n")
	assert.Equal(t, []string{"renamed.txt"}, changedPathsFromPatch(patch))
}

// TestDiffInputFromArgs pins the refusal that matters most: a git ref typed by someone
// arriving from `git diff <ref>` must not be swallowed into a plausible listing of their
// own uncommitted edits under exit 0.
func TestScopeFromArgs(t *testing.T) {
	t.Run("no argument narrows nothing", func(t *testing.T) {
		got, err := scopeFromArgs(nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("positionals are paths, taken at face value", func(t *testing.T) {
		// Deliberately not checked against the working tree: a path that exists in the
		// changeset and not on disk is exactly what a range review is for.
		got, err := scopeFromArgs([]string{"internal/ledger/", "types/diff.go", "gone.go"})
		require.NoError(t, err)
		assert.Equal(t, []string{"internal/ledger/", "types/diff.go", "gone.go"}, got)
	})

	t.Run("a bare dash is a source, and says where it moved", func(t *testing.T) {
		_, err := scopeFromArgs([]string{"-"})
		require.Error(t, err)
		assert.IsType(t, errUsage{}, err)
		assert.Contains(t, err.Error(), "--patch -")
	})

	t.Run("something flag-shaped is a typo, not a path", func(t *testing.T) {
		_, err := scopeFromArgs([]string{"--revv"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--rev")
	})
}

// TestDiffSourceFromFlags pins the rule the positionals depend on: the changeset is named by a
// flag, and exactly one flag may name it.
func TestDiffSourceFromFlags(t *testing.T) {
	t.Run("nothing named reads the working tree", func(t *testing.T) {
		in, err := diffSourceFromFlags(&gen.DiffFlags{})
		require.NoError(t, err)
		assert.Equal(t, inputWorkingTree, in.kind)
		assert.Equal(t, "the working tree", in.label)
	})

	t.Run("--patch - reads stdin", func(t *testing.T) {
		in, err := diffSourceFromFlags(&gen.DiffFlags{Patch: "-"})
		require.NoError(t, err)
		assert.Equal(t, inputStdin, in.kind)
	})

	t.Run("--patch names a file", func(t *testing.T) {
		in, err := diffSourceFromFlags(&gen.DiffFlags{Patch: "change.patch"})
		require.NoError(t, err)
		assert.Equal(t, inputFile, in.kind)
		assert.Equal(t, "change.patch", in.path)
		assert.Contains(t, in.label, "change.patch")
	})

	t.Run("--rev reads a range", func(t *testing.T) {
		in, err := diffSourceFromFlags(&gen.DiffFlags{Rev: "main...topic"})
		require.NoError(t, err)
		assert.Equal(t, inputRevRange, in.kind)
		assert.Equal(t, "main", in.base)
		assert.Equal(t, "topic", in.head)
	})

	t.Run("two sources are refused rather than ranked", func(t *testing.T) {
		_, err := diffSourceFromFlags(&gen.DiffFlags{Rev: "main...topic", Patch: "-"})
		require.Error(t, err)
		assert.IsType(t, errUsage{}, err)
		assert.Contains(t, err.Error(), "only one may be given")
	})
}

// TestPrintDiffTextOrdersTheEvidence covers the whole text rendering: the counts headline,
// the unranked caveat's placement BEFORE the list, the generated fold in both states, and
// the agent trail.
func TestPrintDiffTextOrdersTheEvidence(t *testing.T) {
	rev := types.Diff{
		Base: "working",
		Files: []types.DiffFile{
			{
				Path:    "core/engine.go",
				Project: "core",
				Role:    types.DiffRoleSource,
				Surface: types.DiffSurfacePublic,
				Symbols: []types.DiffSymbol{{Label: "Run", ModuleAPI: true, FileCount: 12}},
				Reach:   diffReach(12),
				Touches: []types.DiffTouch{{
					Host:       "claude-code",
					Read:       []string{"core/engine.go", "core/plan.go"},
					Transcript: "/tmp/session.jsonl",
				}},
			},
			{Path: "web/app.ts", Role: types.DiffRoleSource, Touches: []types.DiffTouch{{}}},
			{Path: "MAGUS.md", Role: types.DiffRoleOutput},
		},
		SeedProjects:     []string{"core"},
		AffectedProjects: []types.ImpactProject{{}, {}},
		Notes:            []string{"no coverage profile was loaded"},
	}

	t.Run("folded", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, printDiffText(rev, false, func(p string) string { return p }, nil))
		})

		assert.Contains(t, out, "2 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild")
		assert.Contains(t, out, "12 files reference its widest changed symbol")
		assert.Contains(t, out, "PUBLIC SURFACE: exports Run")
		assert.Contains(t, out, "written by claude-code, after reading core/engine.go, core/plan.go")
		assert.Contains(t, out, "transcript: /tmp/session.jsonl")
		// A touch with no host still attributes the write rather than printing a blank.
		assert.Contains(t, out, "written by an agent")
		assert.Contains(t, out, "1 generated files folded")
		assert.Contains(t, out, "--generated")
		assert.Contains(t, out, "note: no coverage profile was loaded")
		// The folded file itself is never listed, which is the whole affordance.
		assert.NotContains(t, out, "  MAGUS.md")
	})

	t.Run("shown", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, printDiffText(rev, true, func(p string) string { return "<" + p + ">" }, nil))
		})

		assert.Contains(t, out, "2 files to read, 1 generated shown")
		assert.Contains(t, out, "generated (1) - a target rewrites these")
		assert.Contains(t, out, "<MAGUS.md>")
		assert.Contains(t, out, "magus describe file <path>")
	})

	t.Run("unranked says so before the list", func(t *testing.T) {
		unranked := types.Diff{Files: []types.DiffFile{
			{Path: "b.go", Role: types.DiffRoleSource},
			{Path: "a.go", Role: types.DiffRoleSource},
		}}
		out := captureStdout(t, func() {
			require.NoError(t, printDiffText(unranked, false, func(p string) string { return p }, nil))
		})

		assert.Contains(t, out, "UNRANKED: no symbol index")
		assert.Less(t, strings.Index(out, "UNRANKED"), strings.Index(out, "b.go"),
			"the caveat must arrive before the reader has read the first entry as the most dangerous one")
	})

	t.Run("a single unranked file gets no caveat", func(t *testing.T) {
		out := captureStdout(t, func() {
			one := types.Diff{Files: []types.DiffFile{{Path: "a.go", Role: types.DiffRoleSource}}}
			require.NoError(t, printDiffText(one, false, func(p string) string { return p }, nil))
		})
		assert.NotContains(t, out, "UNRANKED")
		assert.Contains(t, out, "1 files to read")
	})
}

// TestPathLinkerLeavesPipedOutputBare pins the property that keeps a captured log free of
// escape sequences: the hyperlink gate refuses a non-terminal, and a test's stdout is a pipe.
func TestPathLinkerLeavesPipedOutputBare(t *testing.T) {
	link := pathLinker(t.TempDir())
	assert.Equal(t, "cmd/magus/diff.go", link("cmd/magus/diff.go"))
	assert.Equal(t, "/abs/path.go", link("/abs/path.go"))
}

// TestDiffTouchesWithoutATrail covers the common case: no guard hook is wired, so the
// replay is empty and the map is nil rather than an empty map that renders as a column.
func TestDiffTouchesWithoutATrail(t *testing.T) {
	assert.Nil(t, diffTouches(t.TempDir(), t.TempDir(), []string{"a.go"}))
}

// changeset returns a diff of n files, for exercising the hint's threshold.
func changeset(n int) types.Diff {
	rev := types.Diff{Base: "main"}
	for i := range n {
		rev.Files = append(rev.Files, types.DiffFile{Path: strings.Repeat("a", i+1) + ".go"})
	}
	return rev
}

// TestReviewPromptHintFiresOnlyOnALargeChangeset. A flag nobody knows about is a feature
// nobody has, which is why the hint exists - but one printed on every diff is one the reader
// stops seeing by the third time, which is exactly when it starts to matter. Both halves are
// the feature, so both are pinned.
func TestReviewPromptHintFiresOnlyOnALargeChangeset(t *testing.T) {
	var small, large strings.Builder
	hintReviewPrompt(&small, changeset(promptHintFiles-1), &gen.DiffFlags{})
	hintReviewPrompt(&large, changeset(promptHintFiles), &gen.DiffFlags{})

	assert.Empty(t, small.String(), "an ordinary changeset gets no hint")
	assert.Contains(t, large.String(), "--prompt")
	// The refusal travels with the offer: a reader must not have to wonder whether pressing
	// this sends their code somewhere.
	assert.Contains(t, large.String(), "calls no model and sends nothing")
}

// TestReviewPromptHintIsSilentWhenAlreadyAsked: suggesting a flag the reader just passed is
// how a surface teaches people to ignore its hints.
func TestReviewPromptHintIsSilentWhenAlreadyAsked(t *testing.T) {
	var out strings.Builder
	hintReviewPrompt(&out, changeset(promptHintFiles+50), &gen.DiffFlags{Prompt: true})

	assert.Empty(t, out.String())
}

func TestRevRangeFromFlag(t *testing.T) {
	t.Run("base...head is the accepted spelling", func(t *testing.T) {
		in, err := revRangeFromFlag("main...feat/audience")

		require.NoError(t, err)
		assert.Equal(t, diffInput{
			kind:  inputRevRange,
			base:  "main",
			head:  "feat/audience",
			label: "the range main...feat/audience",
		}, in)
	})

	// The positive case above is what keeps this honest: every refusal below would also pass
	// against a parser that rejected everything, and then --rev would be unusable while green.
	t.Run("two dots are refused rather than read as three", func(t *testing.T) {
		_, err := revRangeFromFlag("main..feat/audience")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "three dots")
		assert.Contains(t, err.Error(), "the branch author did not write")
	})

	t.Run("a missing end is refused rather than defaulted", func(t *testing.T) {
		for _, rev := range []string{"main...", "...feat/audience"} {
			_, err := revRangeFromFlag(rev)

			require.Error(t, err, rev)
			assert.Contains(t, err.Error(), "needs both ends", rev)
		}
	})

	t.Run("a bare ref is refused, because it names one end of two", func(t *testing.T) {
		_, err := revRangeFromFlag("feat/audience")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "base...head")
	})
}

func TestAddressableSeparatesTreeStatesFromHandedOverPatches(t *testing.T) {
	// The distinction --ack and the viewer both turn on: magus can re-derive and re-digest a
	// tree state, and cannot say what a patch on stdin describes.
	assert.True(t, diffInput{kind: inputWorkingTree}.addressable())
	assert.True(t, diffInput{kind: inputRevRange}.addressable())
	assert.False(t, diffInput{kind: inputStdin}.addressable())
	assert.False(t, diffInput{kind: inputFile}.addressable())
}

// TestReviewedContentDigestsTheRevisionNotTheCheckout is the one that matters for --rev.
//
// The bug it exists to catch is silent and permanent: minting a receipt for a colleague's branch
// from the reader's own working tree stamps content nobody read, and Covers then agrees with it
// forever. Both halves are asserted, because a digest function that returned "" for everything
// would pass the negative half alone.
func TestReviewedContentDigestsTheRevisionNotTheCheckout(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("what my checkout holds\n"), 0o644))

	atRevision := reviewedContent{
		root: root,
		at:   types.VCSCheckpoint{Revision: "cafe1234", Branch: "feat/audience", VCS: "git"},
		read: func(rev, path string) (string, error) {
			assert.Equal(t, "cafe1234", rev, "the resolved id is what gets read, never the branch name")
			assert.Equal(t, "a.go", path)
			return "what the branch holds\n", nil
		},
	}

	assert.Equal(t, review.Digest([]byte("what the branch holds\n")), atRevision.digest("a.go"))
	assert.NotEqual(t, review.DigestFile(filepath.Join(root, "a.go")), atRevision.digest("a.go"),
		"a range receipt must not fingerprint the reader's own checkout")

	// The working tree is still the working tree when no revision is named.
	assert.Equal(t, review.DigestFile(filepath.Join(root, "a.go")), reviewedContent{root: root}.digest("a.go"))
}

// A file absent at the revision has nothing anyone can have read, and must not become a receipt
// against "" - which Covers would otherwise satisfy for every unreadable file forever.
func TestReviewedContentYieldsNothingForAFileAbsentAtTheRevision(t *testing.T) {
	c := reviewedContent{
		root: t.TempDir(),
		at:   types.VCSCheckpoint{Revision: "cafe1234"},
		read: func(string, string) (string, error) { return "", fmt.Errorf("path does not exist at that revision") },
	}

	assert.Empty(t, c.digest("gone.go"))
}

// TestHintSinceLastReview covers the thing that makes a second pass cost only the second pass, and
// the three silences that keep it from being noise.
func TestHintSinceLastReview(t *testing.T) {
	t.Setenv("MAGUS_HINTS", "1")
	rev := types.Diff{Files: []types.DiffFile{{Path: "a.go"}, {Path: "b.go"}}}
	rangeSrc := diffInput{kind: inputRevRange, base: "main", head: "topic", label: "the range main...topic"}

	reviewed := func(t *testing.T, source types.VCSCheckpoint) string {
		t.Helper()
		cache := t.TempDir()
		require.NoError(t, review.Record(cache, []review.Receipt{
			{Path: "a.go", Digest: "a1", At: time.Now(), Source: source},
		}))
		return cache
	}

	t.Run("an earlier pass names the revision and the command", func(t *testing.T) {
		cache := reviewed(t, types.VCSCheckpoint{Revision: "0123456789abcdef0123", VCS: "git"})

		var out strings.Builder
		hintSinceLastReview(&out, cache, rev, rangeSrc)

		got := out.String()
		assert.Contains(t, got, "you last reviewed 1 of these 2 files")
		assert.Contains(t, got, "0123456789ab", "the prose abbreviates")
		assert.Contains(t, got, "--rev 0123456789abcdef0123...topic", "the command carries the full revision")
	})

	t.Run("nothing to subtract prints nothing", func(t *testing.T) {
		var out strings.Builder
		hintSinceLastReview(&out, t.TempDir(), rev, rangeSrc)
		assert.Empty(t, out.String(), "a first pass has no earlier one")
	})

	t.Run("a working-tree receipt names no revision, so there is nothing to diff from", func(t *testing.T) {
		cache := reviewed(t, types.VCSCheckpoint{})

		var out strings.Builder
		hintSinceLastReview(&out, cache, rev, rangeSrc)

		assert.Empty(t, out.String())
	})

	t.Run("already looking at the reviewed revision prints nothing", func(t *testing.T) {
		cache := reviewed(t, types.VCSCheckpoint{Revision: "main", VCS: "git"})

		var out strings.Builder
		hintSinceLastReview(&out, cache, rev, rangeSrc)

		assert.Empty(t, out.String(), "the reader is already seeing exactly the delta")
	})

	t.Run("a working-tree review has no earlier revision to name", func(t *testing.T) {
		cache := reviewed(t, types.VCSCheckpoint{Revision: "0123456789abcdef0123"})

		var out strings.Builder
		hintSinceLastReview(&out, cache, rev, diffInput{kind: inputWorkingTree, label: "the working tree"})

		assert.Empty(t, out.String())
	})
}
