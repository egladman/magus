package job

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claimTree is the shape of the cli-ls-files job that found the gap: it deleted
// hint.VCSAdd and changed cmd/magus/vcs.go and the registry config-generate reads, and the
// guard sent it to its orchestrator for each caller, test and output that change reached.
// It also changed the signature of Printer.Print and only the body of Printer.Flush, made
// vcs.go use vcsutil.Quote and lsFlag, and holds internal/lease/held.go unchanged.
func claimTree(t *testing.T) (string, types.Job, Observed, []*types.Project) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"internal/hint/cli_command.go":              "package hint\n\nvar VCSStatus = 1\n\nfunc Render() string {\n\treturn \"x\"\n}\n",
		"internal/guard/shell.go":                   "package guard\n\nimport \"example.com/m/internal/hint\"\n\nvar _ = hint.VCSAdd\n",
		"internal/guard/status.go":                  "package guard\n\nimport \"example.com/m/internal/hint\"\n\nvar _ = hint.VCSStatus\n",
		"internal/guard/render.go":                  "package guard\n\nimport \"example.com/m/internal/hint\"\n\nvar _ = hint.Render()\n",
		"cmd/magus/vcs.go":                          "package main\n\nimport \"example.com/m/internal/vcsutil\"\n\nconst lsFiles = \"ls-files\"\n\nfunc vcsLsFiles() string { return vcsutil.Quote(lsFiles) + lsFlag }\n",
		"cmd/magus/flags.go":                        "package main\n\nconst lsFlag = \"-z\"\n",
		"internal/vcsutil/quote.go":                 "package vcsutil\n\nfunc Quote(s string) string { return s }\n\nfunc Unused() {}\n",
		"internal/other/quote.go":                   "package vcsutil\n\nfunc Quote(s string) string { return s }\n",
		"internal/hint/printer.go":                  "package hint\n\ntype Printer struct{}\n\nfunc (Printer) Print(s string) string {\n\treturn s\n}\n\nfunc (Printer) Flush() {\n\tprintln()\n}\n",
		"internal/guard/print.go":                   "package guard\n\nimport \"example.com/m/internal/hint\"\n\nfunc Show(p hint.Printer) string { return p.Print(\"x\") }\n",
		"internal/guard/flush.go":                   "package guard\n\nimport \"example.com/m/internal/hint\"\n\nfunc Done(p hint.Printer) { p.Flush() }\n",
		"cmd/tool/print.go":                         "package tool\n\nfunc Show(p interface{ Print(string) string }) string { return p.Print(\"x\") }\n",
		"internal/lease/held.go":                    "package lease\n\nfunc Held() int { return 1 }\n",
		"internal/lease/held_test.go":               "package lease\n\nimport \"testing\"\n\nfunc TestHeld(t *testing.T) { _ = Held() }\n",
		"internal/lease/other_test.go":              "package lease\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n",
		"internal/free/free.go":                     "package free\n\nfunc Free() int { return 1 }\n",
		"internal/free/free_test.go":                "package free\n\nimport \"testing\"\n\nfunc TestFree(t *testing.T) { _ = Free() }\n",
		"cmd/magus/vcs_test.go":                     "package main\n\nimport \"testing\"\n\nfunc TestLs(t *testing.T) { _ = vcsLsFiles() }\n",
		"cmd/magus/other_test.go":                   "package main\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n",
		"cmd/magus/testdata/script/guard_git.txtar": "exec magus vcs ls-files\n",
		"cmd/magus/testdata/script/unrelated.txtar": "exec magus version\n",
		"internal/commands/registry.go":             "package commands\n\nvar Registry = 1\n",
		"cmd/magus/gen/flags.go":                    "package gen\n",
		"internal/guard/shell_test.go":              "package guard\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	row := types.Job{
		ID:         "cli-ls-files",
		State:      types.StateRunning,
		WritePaths: []string{"internal/hint/cli_command.go", "internal/hint/printer.go", "cmd/magus/vcs.go", "internal/commands/registry.go", "internal/lease/held.go"},
		DenyPaths:  []string{"internal/guard/shell_test.go"},
		Registered: 100,
	}
	region := func(file string, side types.RegionSide, lines [2]int, decl string) types.RegionChange {
		return types.RegionChange{File: types.FileChange{Path: file}, Side: side, Lines: lines, Declaration: decl, Driver: "golang"}
	}
	seen := Observed{
		Changed:      []string{"cmd/magus/vcs.go", "internal/commands/registry.go", "internal/hint/cli_command.go", "internal/hint/printer.go"},
		ChangedKnown: true,
		Regions: []types.RegionChange{
			region("internal/hint/cli_command.go", types.RegionOld, [2]int{3, 3}, "var VCSAdd = 0"),
			region("internal/hint/cli_command.go", types.RegionNew, [2]int{6, 6}, "func Render() string {"),
			region("internal/hint/printer.go", types.RegionNew, [2]int{5, 5}, ""),
			region("internal/hint/printer.go", types.RegionNew, [2]int{10, 10}, "func (Printer) Flush() {"),
			region("cmd/magus/vcs.go", types.RegionNew, [2]int{5, 5}, ""),
		},
		RegionsKnown: true,
	}
	projects := []*types.Project{{
		Path:    ".",
		Sources: types.MustParseGlobs("**/*.go"),
		TargetInputs: map[string][]types.InputRef{
			"config-generate": {{Glob: "internal/commands/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{
			"config-generate": {{Glob: "cmd/magus/gen/*.go"}},
		},
	}}
	return root, row, seen, projects
}

func proofFacts(t *testing.T, asked ...string) (types.Job, claimFacts) {
	t.Helper()
	root, row, seen, projects := claimTree(t)
	return row, readClaimFacts(root, row, seen, asked, projects)
}

type proveCase struct {
	path, because  string
	proof, refused string
}

// proveEach proves each case's because against claimTree. The kind is taken as spelled,
// so a kind types does not name yet is still proved.
func proveEach(t *testing.T, cases map[string]proveCase) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			row, facts := proofFacts(t, tc.path)
			kind, subject, _ := strings.Cut(tc.because, ":")
			proof, err := proveClaim(row, tc.path, types.ClaimEvidence{Kind: types.ClaimKind(kind), Subject: subject}, facts)
			if tc.refused != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.refused)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, proof, tc.proof)
		})
	}
}

func TestProveClaimEachKind(t *testing.T) {
	t.Parallel()

	proveEach(t, map[string]proveCase{
		"a caller of a symbol the diff deleted": {
			path: "internal/guard/shell.go", because: "refs:hint.VCSAdd",
			proof: "internal/guard/shell.go references hint.VCSAdd, which the job's diff removed from package hint",
		},
		"a caller of a declaration the diff changed": {
			path: "internal/guard/render.go", because: "refs:hint.Render",
			proof: "internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/cli_command.go",
		},
		"a caller of a declaration the diff left alone": {
			path: "internal/guard/status.go", because: "refs:hint.VCSStatus",
			refused: "no package the job changed inside its write paths",
		},
		"a file that does not reference the symbol": {
			path: "internal/guard/status.go", because: "refs:hint.VCSAdd",
			refused: "no package the job changed inside its write paths",
		},
		"a test script asserting the changed output": {
			path: "cmd/magus/testdata/script/guard_git.txtar", because: "test:cmd/magus/vcs.go",
			proof: "cmd/magus/testdata/script/guard_git.txtar tests cmd/magus/vcs.go: it names \"ls-files\"",
		},
		"a go test calling what the file defines": {
			path: "cmd/magus/vcs_test.go", because: "test:cmd/magus/vcs.go",
			proof: "cmd/magus/vcs_test.go tests cmd/magus/vcs.go: it names vcsLsFiles",
		},
		"a test that references nothing the file defines": {
			path: "cmd/magus/testdata/script/unrelated.txtar", because: "test:cmd/magus/vcs.go",
			refused: "names nothing cmd/magus/vcs.go defines",
		},
		"a test that is not beside the file": {
			path: "internal/guard/shell_test.go", because: "test:cmd/magus/vcs.go",
			refused: "is not a test beside cmd/magus/vcs.go",
		},
		"a test of a file the job did not change": {
			path: "cmd/magus/vcs_test.go", because: "test:cmd/magus/main.go",
			refused: "cmd/magus/main.go is not a file the job holds or claimed",
		},
		"an output of a target whose sources the job changed": {
			path: "cmd/magus/gen/flags.go", because: "output:.:config-generate",
			proof: "cmd/magus/gen/flags.go is an output of .:config-generate, which reads internal/commands/*.go",
		},
		"an output of another target": {
			path: "cmd/magus/gen/flags.go", because: "output:.:man-generate",
			refused: "is not an output .:man-generate regenerates from what the job changed",
		},
		"a path the attempt never released": {
			path: "internal/guard/shell.go", because: "released",
			refused: "this attempt's holder never released internal/guard/shell.go",
		},
	})
}

func TestProveClaimWiderKinds(t *testing.T) {
	t.Parallel()

	proveEach(t, map[string]proveCase{
		"a test of a file the job holds and left alone": {
			path: "internal/lease/held_test.go", because: "test:internal/lease/held.go",
			proof: "internal/lease/held_test.go tests internal/lease/held.go: it names Held",
		},
		"a test beside a held file that names nothing it defines": {
			path: "internal/lease/other_test.go", because: "test:internal/lease/held.go",
			refused: "names nothing internal/lease/held.go defines",
		},
		"a test of a file the job does not hold": {
			path: "internal/free/free_test.go", because: "test:internal/free/free.go",
			refused: "internal/free/free.go is not a file the job holds or claimed",
		},
		"a caller of a method whose signature the diff changed": {
			path: "internal/guard/print.go", because: "calls:Print",
			proof: "internal/guard/print.go calls Print, whose signature the job's diff changes in internal/hint/printer.go",
		},
		"a caller of a method whose body alone the diff changed": {
			path: "internal/guard/flush.go", because: "calls:Flush",
			refused: "the job's diff changes no signature of Flush",
		},
		"a caller of the name in a package that imports none the job changed": {
			path: "cmd/tool/print.go", because: "calls:Print",
			refused: "the job's diff changes no signature of Print in a package cmd/tool/print.go sits in or imports",
		},
		"a file that calls nothing by the name": {
			path: "internal/guard/status.go", because: "calls:Print",
			refused: "internal/guard/status.go calls nothing named Print",
		},
		"the declaration of a name a changed file imports": {
			path: "internal/vcsutil/quote.go", because: "defines:vcsutil.Quote",
			proof: "internal/vcsutil/quote.go declares Quote, which cmd/magus/vcs.go, a file the job changed, uses",
		},
		"the declaration of a name a changed file uses from its own package": {
			path: "cmd/magus/flags.go", because: "defines:lsFlag",
			proof: "cmd/magus/flags.go declares lsFlag, which cmd/magus/vcs.go, a file the job changed, uses",
		},
		"a declaration no changed file uses": {
			path: "internal/vcsutil/quote.go", because: "defines:vcsutil.Unused",
			refused: "no Go file the job changed inside its write paths uses vcsutil.Unused",
		},
		"a same-named declaration in a package no changed file imports": {
			path: "internal/other/quote.go", because: "defines:vcsutil.Quote",
			refused: "no Go file the job changed inside its write paths uses vcsutil.Quote from internal/other/quote.go",
		},
		"a name the file does not declare": {
			path: "internal/vcsutil/quote.go", because: "defines:vcsutil.Missing",
			refused: "internal/vcsutil/quote.go declares no vcsutil.Missing",
		},
	})
}

// Nothing proves a refs or test kind on a diff nobody read: absent evidence is no proof.
func TestProveClaimRefusesAnUnreadDiff(t *testing.T) {
	t.Parallel()

	root, row, _, projects := claimTree(t)
	facts := readClaimFacts(root, row, Observed{}, []string{"internal/guard/shell.go"}, projects)
	_, err := proveClaim(row, "internal/guard/shell.go", types.ClaimEvidence{Kind: types.ClaimRefs, Subject: "hint.VCSAdd"}, facts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read what the job changed")
}

// A callers-expanded claim names a declaration of its file, and the file is what proves it.
func TestProveClaimReadsTheFileOfADeclarationClaim(t *testing.T) {
	t.Parallel()

	const claim = "internal/guard/shell.go#Shell"
	row, facts := proofFacts(t, claim)
	proof, err := proveClaim(row, claim, types.ClaimEvidence{Kind: types.ClaimRefs, Subject: "hint.VCSAdd"}, facts)
	require.NoError(t, err)
	assert.Contains(t, proof, "internal/guard/shell.go references hint.VCSAdd")
}

func TestProposeClaimNamesTheFirstProofOrNone(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"internal/guard/shell.go":                   "refs:hint.VCSAdd",
		"cmd/magus/testdata/script/guard_git.txtar": "test:cmd/magus/vcs.go",
		"cmd/magus/gen/flags.go":                    "output:.:config-generate",
		"internal/lease/held_test.go":               "test:internal/lease/held.go",
		"internal/guard/print.go":                   "calls:Print",
		"internal/vcsutil/quote.go":                 "defines:vcsutil.Quote",
		"cmd/magus/flags.go":                        "defines:lsFlag",
		"internal/guard/status.go":                  "",
		"internal/guard/flush.go":                   "",
		"internal/free/free_test.go":                "",
		"internal/guard/shell_test.go":              "",
	} {
		row, facts := proofFacts(t, path)
		req, proof, ok := ProposeClaim(row, path, facts, []types.Job{row})
		if kind, _, _ := strings.Cut(want, ":"); want == "" || !types.ClaimKind(kind).Valid() {
			assert.False(t, ok, "%s has no proof the store records, or a deny path covers it", path)
			continue
		}
		require.True(t, ok, path)
		assert.Equal(t, types.JobClaimRequest{Path: path, Because: want}, req)
		assert.NotEmpty(t, proof)
	}
}

func claimStore(t *testing.T, rows ...types.Job) (Location, types.Job, claimFacts) {
	t.Helper()
	root, row, seen, projects := claimTree(t)
	loc := tempLocation(t, root)
	s := NewStore(loc)
	for _, r := range append([]types.Job{row}, rows...) {
		seed(t, s, r)
	}
	asked := []string{"internal/guard/shell.go", "internal/guard/status.go", "internal/guard/shell_test.go", "cmd/magus/testdata/script/guard_git.txtar", "cmd/magus/gen/flags.go"}
	return loc, row, readClaimFacts(root, row, seen, asked, projects)
}

func TestHolderClaimsItsOwnJobWithAProof(t *testing.T) {
	t.Parallel()

	loc, row, facts := claimStore(t)
	reqs := []types.JobClaimRequest{
		{Path: "internal/guard/shell.go", Because: "refs:hint.VCSAdd"},
		{Path: "cmd/magus/testdata/script/guard_git.txtar", Because: "test:cmd/magus/vcs.go"},
		{Path: "cmd/magus/gen/flags.go", Because: "output:.:config-generate"},
	}
	stored, err := boundStore(loc, row.ID).Claim(t.Context(), row.ID, reqs, facts, config.Defaults().Jobs, false)
	require.NoError(t, err)

	want := row.Clone()
	want.WritePaths = append(want.WritePaths, "internal/guard/shell.go", "cmd/magus/testdata/script/guard_git.txtar", "cmd/magus/gen/flags.go")
	want.Widenings = 1
	want.Claims = []types.JobClaim{
		{Path: "internal/guard/shell.go", Evidence: types.ClaimEvidence{Kind: types.ClaimRefs, Subject: "hint.VCSAdd"},
			Proof: "internal/guard/shell.go references hint.VCSAdd, which the job's diff removed from package hint (internal/hint)"},
		{Path: "cmd/magus/testdata/script/guard_git.txtar", Evidence: types.ClaimEvidence{Kind: types.ClaimTest, Subject: "cmd/magus/vcs.go"},
			Proof: `cmd/magus/testdata/script/guard_git.txtar tests cmd/magus/vcs.go: it names "ls-files"`},
		{Path: "cmd/magus/gen/flags.go", Evidence: types.ClaimEvidence{Kind: types.ClaimOutput, Subject: ".:config-generate"},
			Proof: "cmd/magus/gen/flags.go is an output of .:config-generate, which reads internal/commands/*.go, a source the job changed"},
	}
	assert.Equal(t, want, claimedView(stored), "a claim adds the proved paths and never moves the job")

	again, err := NewStore(loc).List()
	require.NoError(t, err)
	assert.Equal(t, stored.Claims, again[0].Claims, "the proof is on the row")
}

// claimedView is a stored row less what the store stamps on every write, and each
// claim less when and by whom, so it compares with the row its fixture seeded.
func claimedView(stored types.Job) types.Job {
	v := stored.Clone()
	v.Schema, v.Created, v.Updated, v.Generation, v.Attempts, v.RegisteredBy = types.Schema{}, 0, 0, 0, 0, types.Origin{}
	for i := range v.Claims {
		v.Claims[i].At, v.Claims[i].By = 0, types.Origin{}
	}
	return v
}

// An exited job claims too: its verification may send it back for a path its exit released.
func TestHolderClaimsAPathItsExitReleased(t *testing.T) {
	t.Parallel()

	loc, row, facts := claimStore(t)
	held := boundStore(loc, row.ID)
	_, err := held.Update(t.Context(), row.ID, func(u *types.Job) {
		u.WritePaths = []string{"cmd/magus/vcs.go", "internal/hint/cli_command.go"}
		u.State = types.StateExited
	})
	require.NoError(t, err)

	stored, err := held.Claim(t.Context(), row.ID, []types.JobClaimRequest{{Path: "internal/commands/registry.go", Because: "released"}}, facts, config.Defaults().Jobs, false)
	require.NoError(t, err)
	assert.Contains(t, stored.WritePaths, "internal/commands/registry.go")
	assert.Equal(t, types.StateExited, stored.State)
	assert.Contains(t, stored.Claims[0].Proof, "released it at")
}

func TestClaimRefusals(t *testing.T) {
	t.Parallel()

	owner := types.Job{ID: "other", State: types.StateRunning, WritePaths: []string{"internal/guard/shell.go"}}
	for name, tc := range map[string]struct {
		actor   string
		cap     int
		reqs    []types.JobClaimRequest
		refused string
	}{
		"a holder claims nothing but its own job": {
			actor: "other", reqs: []types.JobClaimRequest{{Path: "cmd/magus/gen/flags.go", Because: "output:.:config-generate"}},
			refused: "a holder claims only its own job",
		},
		"a path another live job owns": {
			actor: "cli-ls-files", reqs: []types.JobClaimRequest{{Path: "internal/guard/shell.go", Because: "refs:hint.VCSAdd"}},
			refused: "MGS3032",
		},
		"a deny path": {
			actor: "cli-ls-files", reqs: []types.JobClaimRequest{{Path: "internal/guard/shell_test.go", Because: "test:cmd/magus/vcs.go"}},
			refused: "a deny path of cli-ls-files",
		},
		"a path with no proof": {
			actor: "cli-ls-files", reqs: []types.JobClaimRequest{{Path: "internal/guard/status.go", Because: "refs:hint.VCSStatus"}},
			refused: "no package the job changed",
		},
		"past the cap": {
			actor: "cli-ls-files", cap: 1,
			reqs: []types.JobClaimRequest{
				{Path: "cmd/magus/gen/flags.go", Because: "output:.:config-generate"},
				{Path: "cmd/magus/testdata/script/guard_git.txtar", Because: "test:cmd/magus/vcs.go"},
			},
			refused: "past max_claims, 1 (max_claims on job cli-ls-files)",
		},
		"a cap past the ceiling": {
			actor: "cli-ls-files", cap: 11,
			reqs:    []types.JobClaimRequest{{Path: "cmd/magus/gen/flags.go", Because: "output:.:config-generate"}},
			refused: "11 claims (max_claims on job cli-ls-files) is past the ceiling of 10 (jobs.max_claims in magus.yaml)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			loc, row, facts := claimStore(t, owner)
			if tc.cap > 0 {
				_, err := NewStore(loc).Update(t.Context(), row.ID, func(u *types.Job) { u.MaxClaims = tc.cap })
				require.NoError(t, err)
				row.MaxClaims = tc.cap
			}
			_, err := boundStore(loc, tc.actor).Claim(t.Context(), row.ID, tc.reqs, facts, config.Defaults().Jobs, false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.refused)

			after, err := NewStore(loc).List()
			require.NoError(t, err)
			assert.Equal(t, row.WritePaths, after[0].WritePaths, "a refused claim writes nothing")
			assert.Empty(t, after[0].Claims)
		})
	}
}

func TestClaimCapSaysWhereItCameFrom(t *testing.T) {
	t.Parallel()

	jobs := config.Defaults().Jobs
	defaulted, err := claimCap(types.Job{ID: "a"}, jobs)
	require.NoError(t, err)
	assert.Equal(t, 3, defaulted.Max)
	assert.Contains(t, defaulted.Source, "default")

	named, err := claimCap(types.Job{ID: "a", MaxClaims: 7}, jobs)
	require.NoError(t, err)
	assert.Equal(t, claimLimit{Max: 7, Source: "max_claims on job a"}, named)

	_, err = claimCap(types.Job{ID: "a", MaxClaims: 7}, config.Jobs{})
	assert.NoError(t, err, "a config that names no ceiling enforces none")
}

// The line the guard serves is the record apply decodes, bounded so a refusal never grows
// a context.
func TestClaimCommandIsOneApplyLine(t *testing.T) {
	t.Parallel()

	reqs := []types.JobClaimRequest{{Path: "internal/guard/shell.go", Because: "refs:hint.VCSAdd"}}
	line, ok := ClaimCommand("cli-ls-files", reqs)
	require.True(t, ok)
	assert.NotContains(t, line, "\n")
	assert.LessOrEqual(t, len(line), maxClaimCommand)

	cmd, record, found := strings.Cut(line, " <<<")
	require.True(t, found, line)
	assert.True(t, strings.HasSuffix(cmd, "job apply -f -"), cmd)
	record = strings.TrimSuffix(strings.TrimPrefix(record, "'"), "'")
	decoded, err := DecodeDeclarations(bytes.NewBufferString(record))
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, types.Declaration{Schema: types.Schema{Version: types.JobSchemaVersion}, ID: "cli-ls-files", Claim: reqs}, decoded[0])
	assert.Empty(t, decoded[0].Problems())

	long := []types.JobClaimRequest{{Path: strings.Repeat("a/", maxClaimCommand) + "x.go", Because: "released"}}
	_, ok = ClaimCommand("cli-ls-files", long)
	assert.False(t, ok, "a line past the budget is not served")
}

func TestForkRefusesAClaimCapPastTheCeiling(t *testing.T) {
	t.Parallel()

	jobs := config.Defaults().Jobs
	assert.Empty(t, claimCeilingProblems(types.Job{ID: "a", MaxClaims: 10}, jobs))
	assert.Empty(t, claimCeilingProblems(types.Job{ID: "a"}, jobs))

	err := claimCeilingProblems(types.Job{ID: "a", MaxClaims: 11}, jobs).Err()
	require.ErrorIs(t, err, types.JobForkLimitExceeded)
	assert.Contains(t, err.Error(), "11 claims (max_claims on job a)")
	assert.Contains(t, err.Error(), "ceiling of 10 (jobs.max_claims in magus.yaml)")
}

func TestForkAdvisesASmallClaimCap(t *testing.T) {
	t.Parallel()

	assert.Empty(t, claimLimitAdvice(types.Job{ID: "a", MaxClaims: advisedMaxClaims}))
	assert.Empty(t, claimLimitAdvice(types.Job{ID: "a"}))
	assert.Contains(t, claimLimitAdvice(types.Job{ID: "a", MaxClaims: 8}), "a asks for 8 claims")
}
