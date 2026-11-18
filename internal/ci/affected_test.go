package ci

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/impact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func explorerLink(seed string) (string, string) { return "http://explorer/" + seed, "auth" }

type PrintRunRefSuite struct{ suite.Suite }

func TestPrintRunRefSuite(t *testing.T) { suite.Run(t, new(PrintRunRefSuite)) }

func (s *PrintRunRefSuite) TestNamesInvocationAndItsReader() {
	var out strings.Builder
	PrintRunRef(&out, "invmsm3vcou1")
	s.Equal("\nrun: invmsm3vcou1; `magus query invocation invmsm3vcou1` prints the whole run\n", out.String())
}

func (s *PrintRunRefSuite) TestPrintsNothingWithoutAnInvocation() {
	var out strings.Builder
	PrintRunRef(&out, "")
	s.Empty(out.String())
}

func TestPrintFailedResultsNamesEachFailureByRefOrCode(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	PrintFailedResults(&out, []journal.Run{
		{Project: "libs/x", Target: "test", Ref: "out1a2b3c", Error: "go exited 1\n--- FAIL: TestX"},
		{Project: ".", Target: "lint", DiagnosticCode: "MGS3009", Error: "not starting . lint"},
	})
	assert.Equal(t, "\nfailed: 2 targets; `magus query output <ref>` prints a whole log\n"+
		"  out1a2b3c libs/x:test  go exited 1\n"+
		"  MGS3009 .:lint  not starting . lint\n", out.String())

	out.Reset()
	PrintFailedResults(&out, nil)
	assert.Empty(t, out.String(), "a run with no failed result prints nothing")
}

// TestPrintImpactTextQualifiesUndeclaredSeeds pins the sentence this changed. "seeded by
// 2 changed files" reads as two files the project is built from; when one of them is
// declared by nobody, the line was claiming a keyed relationship that does not exist.
// The count and the per-file marker both matter: the count sizes the waste, the marker
// says which declaration is missing.
func TestPrintImpactTextQualifiesUndeclaredSeeds(t *testing.T) {
	var b strings.Builder
	require.NoError(t, PrintImpactText(&b, &types.ImpactResult{
		Base:             "origin/main",
		ChangedFileCount: 2,
		ChangedFiles:     []string{".golangci.yml", "main.go"},
		SeedProjects:     []string{"."},
		AffectedProjects: []impact.Project{{
			Path:            ".",
			Seed:            true,
			Files:           []string{".golangci.yml", "main.go"},
			UndeclaredFiles: []string{".golangci.yml"},
		}},
	}, explorerLink))
	out := b.String()
	assert.Contains(t, out, ". (seeded by 2 changed files, 1 declared by no project)")
	assert.Contains(t, out, "    .golangci.yml (undeclared)")
	assert.Contains(t, out, "    main.go\n", "a declared file carries no marker")
}

// TestPrintAffectedExplainTextMarksUndeclaredFiles: --explain answers "why did this
// project run". A file that seeds by containment alone is a different answer to that
// question than a declared source, and an unannotated list conflates them.
func TestPrintAffectedExplainTextMarksUndeclaredFiles(t *testing.T) {
	var b strings.Builder
	assert.True(t, PrintAffectedExplainText(&b, AffectedExplainOutput{
		Project:  ".",
		Affected: true,
		Base:     "origin/main",
		Paths: []AffectedExplainPath{{
			Seed:       ".",
			Chain:      []string{"."},
			Files:      []string{".golangci.yml", "main.go"},
			Undeclared: []string{".golangci.yml"},
		}},
	}))
	out := b.String()
	assert.Contains(t, out, "    .golangci.yml (undeclared: seeds by directory containment, keys nothing)")
	assert.Contains(t, out, "    main.go\n", "a declared source keeps its bare line")
}

// TestPrintImpactTextStaysPlainWhenEverythingIsDeclared: the qualification is a finding,
// so a workspace with nothing to report must read exactly as it did before.
func TestPrintImpactTextStaysPlainWhenEverythingIsDeclared(t *testing.T) {
	var b strings.Builder
	require.NoError(t, PrintImpactText(&b, &types.ImpactResult{
		Base:             "origin/main",
		ChangedFileCount: 1,
		ChangedFiles:     []string{"main.go"},
		SeedProjects:     []string{"."},
		AffectedProjects: []impact.Project{{Path: ".", Seed: true, Files: []string{"main.go"}}},
	}, explorerLink))
	out := b.String()
	assert.Contains(t, out, ". (seeded by 1 changed file)")
	assert.NotContains(t, out, "undeclared")
}

// TestUndeclaredOnlySeedsKeepsWhatContainmentAloneSelected: the run-path report is
// narrower than --impact's. A seed with one declared changed file among the undeclared
// ones had a keyed reason to run, and naming it at the point of cost would claim the
// whole rerun was waste.
func TestUndeclaredOnlySeedsKeepsWhatContainmentAloneSelected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		res  *types.AffectedResult
		want map[string][]string
	}{
		{
			name: "no set was computed",
			res:  nil,
			want: nil,
		},
		{
			name: "every changed file under the seed is undeclared",
			res: &types.AffectedResult{
				FilesBySeed:      map[string][]string{".": {".golangci.yml"}},
				UndeclaredBySeed: map[string][]string{".": {".golangci.yml"}},
			},
			want: map[string][]string{".": {".golangci.yml"}},
		},
		{
			name: "one declared file is enough to keep the seed out",
			res: &types.AffectedResult{
				FilesBySeed:      map[string][]string{".": {".golangci.yml", "main.go"}},
				UndeclaredBySeed: map[string][]string{".": {".golangci.yml"}},
			},
			want: map[string][]string{},
		},
		{
			name: "a seed whose files are all declared",
			res: &types.AffectedResult{
				FilesBySeed:      map[string][]string{"docs": {"docs/a.md"}},
				UndeclaredBySeed: map[string][]string{},
			},
			want: map[string][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, UndeclaredOnlySeeds(tt.res))
		})
	}
}

// TestTrackedUndeclaredSeedsDropsWhatTheRunItselfWrote pins both halves of MGS1028's
// filter. The run that emits it has usually just written into the workspace, and in a
// workspace whose ignore rules miss the cache directory those writes come back as
// "changed files nothing declares": magus advising you to declare its own lock files
// and the binary it had just linked. Neither is a forgotten declaration.
//
// The other half matters more: a committed source file nobody declared is exactly what
// this diagnostic exists for, and a filter that swallowed it would leave the code in
// place doing nothing.
func TestTrackedUndeclaredSeedsDropsWhatTheRunItselfWrote(t *testing.T) {
	repo := testkit.NewRepository(t, nil)
	dir := repo.Root()

	repo.Commit("seed", map[string]string{"LICENSE": "x"})

	// What the run leaves behind: its own cache directory and a linked binary, both
	// untracked because nothing ignores them here.
	repo.Write(".magus/locks/h/lock", "")
	repo.WriteExecutable("admin", "elf")

	ctx := context.Background()
	got := TrackedUndeclaredSeeds(ctx, dir, types.VCSOptions{}, map[string][]string{
		".":     {"LICENSE", ".magus/locks/h/lock"},
		"admin": {"admin"},
	})
	assert.Equal(t, map[string][]string{".": {"LICENSE"}}, got,
		"the committed file still reports; the seed left with only the run's own leavings goes away")

	assert.Empty(t, TrackedUndeclaredSeeds(ctx, dir, types.VCSOptions{}, map[string][]string{
		".": {".magus/locks/h/lock", "admin"},
	}))

	// Nothing undeclared reaches the VCS at all, and the empty map survives so the
	// callers that range over it keep their shape.
	assert.Equal(t, map[string][]string{}, TrackedUndeclaredSeeds(ctx, dir, types.VCSOptions{}, map[string][]string{}))
}

// TestTrackedUndeclaredSeedsGoesQuietWithoutAVCS: a backend that cannot say what it
// tracks makes this unanswerable, and the doctor twin skips the question rather than
// guessing. Guessing here would name build products as forgotten declarations, which is
// the defect this filter exists to fix.
func TestTrackedUndeclaredSeedsGoesQuietWithoutAVCS(t *testing.T) {
	t.Parallel()

	off := false
	got := TrackedUndeclaredSeeds(context.Background(), t.TempDir(),
		types.VCSOptions{Enabled: &off}, map[string][]string{".": {"LICENSE"}})
	assert.Nil(t, got)
}

// TestUndeclaredSeedNoticeNamesFilesAndFix pins what MGS1028's notice carries. The
// channel it rides (a hint in text, which -s still bubbles up and hints.enabled turns
// off) is the sink's, pinned by TestTextSinkRendersANoticeAsAHint at the root, and so is
// the code: the sink prefixes it in text and records it as a field in JSONL.
func TestUndeclaredSeedNoticeNamesFilesAndFix(t *testing.T) {
	got := UndeclaredSeedNotice(map[string][]string{"silent-run": {".golangci.yml", "dprint.json"}}, true)
	assert.NotContains(t, got, string(types.UndeclaredSeedingFile)+"]", "the code is the notice's field, not its text")
	assert.Contains(t, got, "silent-run (.golangci.yml, dprint.json)", "the run prints a project list and never the changeset, so the files come with it")
	assert.Contains(t, got, "sources", "the one-line fix rides along")
	assert.Contains(t, got, types.CodeURL(types.UndeclaredSeedingFile))
}

// TestUndeclaredSeedNoticeCapsEveryList: the notice promises to be one line. A changeset
// that touches a hundred undeclared files under a dozen projects must still fit it, and
// the tail is a count rather than a truncation, so the reader knows what was left out.
func TestUndeclaredSeedNoticeCapsEveryList(t *testing.T) {
	t.Parallel()

	seeds := map[string][]string{}
	for i := range undeclaredSeedHintCap + 2 {
		seeds[fmt.Sprintf("p%d", i)] = []string{"a.txt"}
	}
	got := UndeclaredSeedNotice(seeds, false)
	assert.Contains(t, got, "p4")
	assert.NotContains(t, got, "p5", "past the cap the projects are counted, not named")
	assert.Contains(t, got, "and 2 more")

	files := map[string][]string{".": {}}
	for i := range undeclaredSeedHintCap + 2 {
		files["."] = append(files["."], fmt.Sprintf("f%d.txt", i))
	}
	got = UndeclaredSeedNotice(files, true)
	assert.Contains(t, got, "f4.txt")
	assert.NotContains(t, got, "f5.txt", "one project's files are capped by the same rule")
	assert.Contains(t, got, "and 2 more")

	// The forensic modes keep the shape they had: --impact and --explain mark every
	// file inline already, so repeating them there would be the same list twice.
	assert.NotContains(t, UndeclaredSeedNotice(map[string][]string{".": {"LICENSE"}}, false), "LICENSE")
}

func TestImpactPct(t *testing.T) {
	assert.Equal(t, "0%", impactPct(0))
	assert.Equal(t, "25%", impactPct(0.25))
	assert.Equal(t, "76%", impactPct(0.756))
	assert.Equal(t, "100%", impactPct(1))
}
