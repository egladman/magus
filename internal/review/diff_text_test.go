package review

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types/check"
	"github.com/egladman/magus/types/diff"
	"github.com/egladman/magus/types/impact"
	"github.com/egladman/magus/types/release"
	"github.com/egladman/magus/types/vcs"
)

func TestDiffCountsLineIsWhatTheReaderIsLeftWith(t *testing.T) {
	rev := diff.Diff{
		Files: []diff.File{
			{Path: "a.go", Role: diff.RoleSource},
			{Path: "b.go", Role: diff.RoleSource},
			{Path: "gen/out.json", Role: diff.RoleOutput},
		},
		SeedProjects:     []string{"root"},
		AffectedProjects: []impact.Project{{Path: "root"}, {Path: "docs"}},
	}
	assert.Equal(t, "2 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild",
		CountsLine(rev, false))
	// Under --generated the same files are printed right below this line, so calling them
	// folded contradicts the page it introduces.
	assert.Equal(t, "2 files to read, 1 generated shown; 1 projects edited, 2 projects rebuild",
		CountsLine(rev, true))
	assert.Equal(t, "0 files to read", CountsLine(diff.Diff{}, false))
	assert.Equal(t, "0 files to read", CountsLine(diff.Diff{}, true),
		"with nothing generated there is no clause either way")
}

// TestPrintDiffTextWithoutImpactIsUnchanged is the additive guarantee.
//
// There is no golden file for this report, so the whole report is spelled out here: any
// impact line leaking into the default rendering fails this, and so does a stray blank
// line, which a substring check for the section headers would not catch.
func TestPrintDiffTextWithoutImpactIsUnchanged(t *testing.T) {
	reach := 12
	rev := diff.Diff{
		Files: []diff.File{
			{Path: "a.go", Role: diff.RoleSource, Reach: &reach, Project: "root"},
			{Path: "gen/out.json", Role: diff.RoleOutput},
		},
		SeedProjects:     []string{"root"},
		AffectedProjects: []impact.Project{{Path: "root"}, {Path: "docs"}},
	}
	identity := func(p string) string { return p }

	var buf bytes.Buffer
	require.NoError(t, PrintText(&buf, rev, TextOptions{ShowGenerated: false, Link: identity}))
	out := buf.String()

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
	rev := diff.Diff{Files: []diff.File{{Path: "a.go", Role: diff.RoleSource}}}
	var buf bytes.Buffer
	require.NoError(t, PrintText(&buf, rev, TextOptions{
		Link:   func(p string) string { return p },
		Impact: []string{"IMPACT - the blast radius of landing this"},
	}))
	out := buf.String()

	assert.True(t, strings.HasPrefix(out, "1 files to read\n"), "the counts line still opens the report")
	assert.Contains(t, out, "\nIMPACT - the blast radius of landing this\n")
	assert.Less(t, strings.Index(out, "  a.go"), strings.Index(out, "IMPACT"),
		"the changeset is what a reader came for; the impact follows it")
}

// TestDiffNextStepLinesTeachTheWorkflow pins the pointers at the end of the report.
//
// This report is what a reader meets when the viewer stood aside (piped, redirected, in CI,
// or asked for with --no-tui), and it has the most readers. It used to name
// the console and nothing else, so every other way of reading a changeset existed only in
// `-h` prose and the man page: the best teaching in the product sat furthest from the door.
//
// It no longer teaches --tui, because there is no such flag to teach: at a terminal the
// viewer is what already happened.
func TestDiffNextStepLinesTeachTheWorkflow(t *testing.T) {
	got := strings.Join(nextStepLines(3), "\n")
	assert.Contains(t, got, "magus diff --impact")
	assert.Contains(t, got, "magus diff --no-tui")
	assert.NotContains(t, got, "--tui ", "there is no opt-in flag left to name")

	// Nothing to read is nothing to suggest: a clean tree or an all-generated changeset has
	// no reading to offer, and a pointer there suggests doing nothing.
	assert.Nil(t, nextStepLines(0))
}

// TestPrintDiffTextOrdersTheEvidence covers the whole text rendering: the counts headline,
// the unranked caveat's placement BEFORE the list, the generated fold in both states, and
// the agent trail.
func TestPrintDiffTextOrdersTheEvidence(t *testing.T) {
	rev := diff.Diff{
		Base: "working",
		Files: []diff.File{
			{
				Path:       "core/engine.go",
				Project:    "core",
				Role:       diff.RoleSource,
				Visibility: diff.VisibilityPublic,
				Symbols:    []diff.Symbol{{Label: "Run", PublicBeyondWorkspace: true, FileCount: 12}},
				Reach:      diffReach(12),
				Touches: []diff.Touch{{
					Host:       "claude-code",
					Read:       []string{"core/engine.go", "core/plan.go"},
					Transcript: "/tmp/session.jsonl",
				}},
			},
			{Path: "web/app.ts", Role: diff.RoleSource, Touches: []diff.Touch{{}}},
			{Path: "MAGUS.md", Role: diff.RoleOutput},
		},
		SeedProjects:     []string{"core"},
		AffectedProjects: []impact.Project{{}, {}},
		Notes:            []string{"no coverage profile was loaded"},
	}

	t.Run("folded", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, PrintText(&buf, rev, TextOptions{ShowGenerated: false, Link: func(p string) string { return p }}))
		out := buf.String()

		assert.Contains(t, out, "2 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild")
		assert.Contains(t, out, "12 files reference its widest changed symbol")
		assert.Contains(t, out, "EXPORTED API: exports Run")
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
		var buf bytes.Buffer
		require.NoError(t, PrintText(&buf, rev, TextOptions{ShowGenerated: true, Link: func(p string) string { return fmt.Sprintf("<%s>", p) }}))
		out := buf.String()

		assert.Contains(t, out, "2 files to read, 1 generated shown")
		assert.Contains(t, out, "generated (1) - a target rewrites these")
		assert.Contains(t, out, "<MAGUS.md>")
		assert.Contains(t, out, "magus describe file <path>")
	})

	t.Run("unranked says so before the list", func(t *testing.T) {
		unranked := diff.Diff{Files: []diff.File{
			{Path: "b.go", Role: diff.RoleSource},
			{Path: "a.go", Role: diff.RoleSource},
		}}
		var buf bytes.Buffer
		require.NoError(t, PrintText(&buf, unranked, TextOptions{ShowGenerated: false, Link: func(p string) string { return p }}))
		out := buf.String()

		assert.Contains(t, out, "UNRANKED: no symbol index")
		assert.Less(t, strings.Index(out, "UNRANKED"), strings.Index(out, "b.go"),
			"the caveat must arrive before the reader has read the first entry as the most dangerous one")
	})

	t.Run("a single unranked file gets no caveat", func(t *testing.T) {
		one := diff.Diff{Files: []diff.File{{Path: "a.go", Role: diff.RoleSource}}}
		var buf bytes.Buffer
		require.NoError(t, PrintText(&buf, one, TextOptions{Link: func(p string) string { return p }}))
		out := buf.String()
		assert.NotContains(t, out, "UNRANKED")
		assert.Contains(t, out, "1 files to read")
	})
}

// TestPathLinkerLeavesPipedOutputBare pins the property that keeps a captured log free of
// escape sequences: the hyperlink gate refuses a non-terminal, and a test's stdout is a pipe.
func TestPathLinkerLeavesPipedOutputBare(t *testing.T) {
	link := PathLinker(t.TempDir())
	assert.Equal(t, "cmd/magus/diff.go", link("cmd/magus/diff.go"))
	assert.Equal(t, "/abs/path.go", link("/abs/path.go"))
}

// TestHintSinceLastReview covers the thing that makes a second pass cost only the second pass, and
// the three silences that keep it from being noise.
//
// It drives Diff.Reviewed rather than seeding a receipt store, because that field is the one answer
// every client reads: the console gets it over the wire and the terminal gets it here, and a hint
// that re-derived it would be the second lookup this join exists to prevent.
func TestHintSinceLastReview(t *testing.T) {
	rangeSrc := Source{Kind: SourceRevRange, Base: "main", Head: "topic", Label: "the range main...topic"}
	reviewed := func(at vcs.Checkpoint, files int) diff.Diff {
		rev := diff.Diff{Files: []diff.File{{Path: "a.go"}, {Path: "b.go"}}}
		rev.AttachReviewed(at, files)
		return rev
	}

	t.Run("an earlier pass names the revision and the command", func(t *testing.T) {
		got := notices(t, func() {
			HintSinceLastReview(t.Context(), reviewed(vcs.Checkpoint{Revision: "0123456789abcdef0123", VCS: "git"}, 1), rangeSrc)
		})
		assert.Contains(t, got, "you last reviewed 1 of these 2 files")
		assert.Contains(t, got, "0123456789ab", "the prose abbreviates")
		assert.Contains(t, got, "--rev 0123456789abcdef0123...topic", "the command carries the full revision")
	})

	t.Run("nothing to subtract prints nothing", func(t *testing.T) {
		out := notices(t, func() { HintSinceLastReview(t.Context(), reviewed(vcs.Checkpoint{}, 0), rangeSrc) })
		assert.Empty(t, out, "a first pass has no earlier one")
	})

	t.Run("already looking at the reviewed revision prints nothing", func(t *testing.T) {
		out := notices(t, func() {
			HintSinceLastReview(t.Context(), reviewed(vcs.Checkpoint{Revision: "main", VCS: "git"}, 1), rangeSrc)
		})
		assert.Empty(t, out, "the reader is already seeing exactly the delta")
	})

	t.Run("a working-tree review has no earlier revision to name", func(t *testing.T) {
		out := notices(t, func() {
			HintSinceLastReview(t.Context(), reviewed(vcs.Checkpoint{Revision: "0123456789abcdef0123"}, 1),
				Source{Kind: SourceWorkingTree, Label: "the working tree"})
		})
		assert.Empty(t, out)
	})
}

func TestDiffAPILinesNameTheReleaseStage(t *testing.T) {
	t.Parallel()

	api := func(stage release.Stage) *diff.API {
		return &diff.API{Base: "main", Floor: diff.BumpMajor, Likely: diff.BumpMajor, Removed: 1, Stage: stage}
	}
	cases := map[string]struct {
		stage release.Stage
		want  []string
	}{
		"below 1.0 a major floor ships as a minor": {
			stage: release.Stage{Tag: "v0.4.0", Major: 0},
			want:  []string{"  newest release v0.4.0: below 1.0 a major floor ships as a minor release"},
		},
		"from 1.0 on the tag stands alone": {
			stage: release.Stage{Tag: "v1.2.0", Major: 1},
			want:  []string{"  newest release v1.2.0"},
		},
		"no tag visible says nothing about a stage": {},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lines := apiLines(api(tc.stage))
			var stageLines []string
			for _, l := range lines {
				if strings.HasPrefix(l, "  newest release") {
					stageLines = append(stageLines, l)
				}
			}
			assert.Equal(t, tc.want, stageLines)
			assert.Equal(t, "  a floor, not a verdict: behavior can change under an unchanged signature, so raise it freely", lines[len(lines)-1],
				"the caveat stays last")
		})
	}
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
		assert.Empty(t, FileFacts(diff.File{Path: "a.go", Visibility: diff.VisibilityUnknown}))
	})

	t.Run("zero reach is not a fact", func(t *testing.T) {
		facts := FileFacts(diff.File{Path: "a.go", Reach: diffReach(0)})
		assert.Empty(t, facts)
	})

	t.Run("singular nouns agree with their counts", func(t *testing.T) {
		facts := FileFacts(diff.File{
			Path:  "a.go",
			Reach: diffReach(1),
			Churn: &diff.Churn{Commits: 1},
		})
		assert.Contains(t, facts, "1 file reference its widest changed symbol")
		assert.Contains(t, facts, "changed in 1 commit")
	})

	t.Run("exported API names the exports when it knows them", func(t *testing.T) {
		facts := FileFacts(diff.File{
			Path:       "a.go",
			Visibility: diff.VisibilityPublic,
			Symbols:    []diff.Symbol{{Label: "Open", PublicBeyondWorkspace: true}, {Label: "Close", PublicBeyondWorkspace: true}},
		})
		require.NotEmpty(t, facts)
		assert.Equal(t, "EXPORTED API: exports Open, Close", facts[0])
	})

	t.Run("conformance shows each check's message", func(t *testing.T) {
		facts := FileFacts(diff.File{
			Path: "a.go",
			Symbols: []diff.Symbol{{Label: "EntryPointFrom", Checks: []check.Check{{
				Name: diff.CheckNamingAffix, Status: check.Advice, Evidence: check.EvidenceInferred,
				Message: "`EntryPointFrom`: 8 of 9 functions shaped `func(ctx) value` that say From or Context are named `<X>FromContext`",
				Details: []string{"named <X>FromContext: BaseFromContext"},
			}}}},
		})
		assert.Equal(t, []string{
			"CONFORMANCE `EntryPointFrom`: 8 of 9 functions shaped `func(ctx) value` that say From or Context are named `<X>FromContext`",
		}, facts)
	})

	t.Run("exported API falls back to the consuming projects", func(t *testing.T) {
		facts := FileFacts(diff.File{
			Path:       "a.go",
			Visibility: diff.VisibilityPublic,
			Symbols: []diff.Symbol{
				{Label: "Open", PublicTo: []string{"web", "api"}},
				{Label: "Close", PublicTo: []string{"web"}},
			},
		})
		require.NotEmpty(t, facts)
		assert.Equal(t, "EXPORTED API: used by web, api", facts[0])
	})

	t.Run("exported API with no evidence still says public", func(t *testing.T) {
		facts := FileFacts(diff.File{Path: "a.go", Visibility: diff.VisibilityPublic})
		require.NotEmpty(t, facts)
		assert.Equal(t, "EXPORTED API", facts[0])
	})

	t.Run("a hot rising file says so, and an unranked one keeps its commit count", func(t *testing.T) {
		hot := FileFacts(diff.File{
			Path:    "a.go",
			Project: "core",
			Churn:   &diff.Churn{Commits: 40, Authors: 6, Rank: 3, ProjectTrend: 12},
		})
		assert.Contains(t, hot, "changed in 40 commits by 6 people, hotspot #3 AND RISING - worth asking why it keeps changing")
		assert.Contains(t, hot, "in core")

		cold := FileFacts(diff.File{
			Path:  "a.go",
			Churn: &diff.Churn{Commits: 40, Rank: diff.NotableRankCutoff + 1, ProjectTrend: 12},
		})
		assert.Contains(t, cold, "changed in 40 commits")
		for _, f := range cold {
			assert.NotContains(t, f, "hotspot")
			assert.NotContains(t, f, "RISING")
		}
	})

	t.Run("coverage rounds and no history is stated plainly", func(t *testing.T) {
		facts := FileFacts(diff.File{
			Path:      "a.go",
			Coverage:  &impact.Coverage{Ratio: 0.675, Covered: 27, Total: 40},
			NoHistory: true,
		})
		assert.Contains(t, facts, "68% covered")
		assert.Contains(t, facts, "NO HISTORY - nothing has exercised this yet")
	})

	t.Run("zero total coverage is not zero percent", func(t *testing.T) {
		facts := FileFacts(diff.File{Path: "a.go", Coverage: &impact.Coverage{}})
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

// notices returns what the pretty display prints for the records fn logs through the default
// logger, the way a notice reaches a person now that it is a record.
func notices(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(cache.NewLogHandler(&buf, "pretty", &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}
