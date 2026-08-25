package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/diff"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// TestDiffTUIRefusalMatrix pins every way --tui can be asked for something it cannot do.
//
// Each of these has a plausible reading that would "work" and produce a lie: a viewport over
// a patch file coordinating a session about a working tree nobody is editing, two loops
// fighting for one terminal, or -o json answered with a picture. A refusal that names the
// conflicting flag is the whole point, so the message is asserted, not just the failure.
func TestDiffTUIRefusalMatrix(t *testing.T) {
	workingTree := diffInput{kind: inputWorkingTree, label: "the working tree"}
	stdin := diffInput{kind: inputStdin, label: "a patch on stdin"}
	patchFile := diffInput{kind: inputFile, path: "x.patch", label: "the patch in x.patch"}
	// The viewer needs BOTH descriptors, which is why the gate carries them separately: it
	// reads stdin and paints stdout.
	atTerminal := diffTUITerm{Reads: true, Paints: true}

	tests := []struct {
		name       string
		flags      gen.DiffFlags
		src        diffInput
		format     Format
		term       diffTUITerm
		wantMsg    string
		wantStderr string
	}{
		{
			name:   "without --tui nothing here is refused",
			flags:  gen.DiffFlags{Watch: true},
			src:    stdin,
			format: outputJSON,
		},
		{
			name:   "--tui at a terminal over the working tree runs",
			flags:  gen.DiffFlags{Tui: true},
			src:    workingTree,
			format: outputText,
			term:   atTerminal,
		},
		{
			name:   "--generated composes: it only sets the initial fold",
			flags:  gen.DiffFlags{Tui: true, Generated: true},
			src:    workingTree,
			format: outputText,
			term:   atTerminal,
		},
		{
			name:    "a patch file has no working tree to coordinate over",
			flags:   gen.DiffFlags{Tui: true},
			src:     patchFile,
			format:  outputText,
			term:    atTerminal,
			wantMsg: "--tui reads the working tree, so it cannot be combined with the patch in x.patch",
		},
		{
			name:    "stdin, same reason and named the same way",
			flags:   gen.DiffFlags{Tui: true},
			src:     stdin,
			format:  outputText,
			term:    atTerminal,
			wantMsg: "--tui reads the working tree, so it cannot be combined with a patch on stdin",
		},
		{
			name:    "--watch and --tui both own the terminal",
			flags:   gen.DiffFlags{Tui: true, Watch: true},
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			wantMsg: "--tui and --watch both drive the terminal",
		},
		{
			name:    "a machine-readable format cannot be answered with a viewport",
			flags:   gen.DiffFlags{Tui: true},
			src:     workingTree,
			format:  outputJSON,
			term:    atTerminal,
			wantMsg: "cannot be combined with -o json",
		},
		{
			name:       "no terminal names the command that works here",
			flags:      gen.DiffFlags{Tui: true},
			src:        workingTree,
			format:     outputText,
			wantStderr: "magus: diff --tui requires an interactive terminal; use `magus diff` instead\n",
		},
		{
			// `magus diff --tui > file`. Every flag is fine and stdin is still a keyboard, so
			// the stdout probe is the only thing that can refuse this.
			name:       "a redirected stdout is refused here, not deep inside the viewer",
			flags:      gen.DiffFlags{Tui: true},
			src:        workingTree,
			format:     outputText,
			term:       diffTUITerm{Reads: true},
			wantStderr: "magus: diff --tui requires an interactive terminal; use `magus diff` instead\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := tc.flags
			var err error
			stderr := captureStderr(t, func() {
				err = diffTUIRefusal(&flags, tc.src, tc.format, tc.term)
			})
			assert.Equal(t, tc.wantStderr, stderr)
			if tc.wantMsg == "" && tc.wantStderr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			// Both refusal shapes exit 2: nothing was attempted, so this is a misuse rather
			// than a failure of the work.
			assert.Equal(t, exitUsage, exitCodeOf(err))
			if tc.wantMsg != "" {
				assert.Contains(t, err.Error(), tc.wantMsg)
			}
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

	files := diffTUIFiles(rev, diff.ParseHunks(patch))
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
		"      or step through them here: magus diff --tui",
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
		"REVIEW: read receipts unavailable; read a file through in `magus diff --tui` to earn one",
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

// TestDiffNextStepLinesTeachTheWorkflow pins the pointers at the end of the default report.
//
// That report is the funnel mouth - the surface everybody runs - and it used to name the
// console and nothing else, so `--tui` and `--impact` existed only in `-h` prose and the man
// page. The best teaching in the product sat furthest from the entrance.
func TestDiffNextStepLinesTeachTheWorkflow(t *testing.T) {
	got := strings.Join(diffNextStepLines(3), "\n")
	assert.Contains(t, got, "magus diff --tui")
	assert.Contains(t, got, "magus diff --impact")
	// The exit, named with the entrance: --tui takes over the screen, and an invitation into
	// a full-screen mode that does not say how to leave gets declined once and never retaken.
	assert.Contains(t, got, "q leaves it")

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

	_, err := diffInputFromArgs(seven)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pager.diff")
	assert.Contains(t, err.Error(), "magus diff -")
	assert.Contains(t, err.Error(), "f.txt", "the file git was asking about")

	// Seven positionals with no git around are an ordinary typo, and the generic refusal is
	// the honest answer there: inventing a git explanation would send them to the wrong page.
	t.Setenv("GIT_DIFF_PATH_TOTAL", "")
	_, err = diffInputFromArgs(seven)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most one patch argument")
	assert.NotContains(t, err.Error(), "pager.diff")
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
