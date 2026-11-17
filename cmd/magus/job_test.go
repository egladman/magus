package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/contract"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
	guardverdict "github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func leaseRow(id, parent string) jobtypes.Job {
	return jobtypes.Job{ID: id, Parent: parent, State: jobtypes.StateRunning, Agent: &jobtypes.Agent{Model: jobtypes.Model{Tier: "bounded"}, Worker: jobtypes.Worker{Type: jobtypes.WorkerTypeResumed}}}
}

// execFixture pins XDG_STATE_HOME to a scratch dir (the job store lives there, not under
// t.TempDir() alone: see the tests-need-xdg-state-home-pinned lesson) and resolves the
// same cache dir jobExec itself will compute from root, so a marker or row seeded here is
// the one jobExec sees. Mirrors TestHookEnvelopeCwdLocatesTheWorkersCheckout's setup.
func execFixture(t *testing.T, rows ...jobtypes.Job) (root, cacheDir string) {
	t.Helper()
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	w := testkit.NewWorkspace(t)
	w.Write("magus.yaml", "")
	return seedJobStore(t, w.Root(), rows...)
}

// execRepositoryFixture is execFixture over a git repository holding the same magus.yaml.
func execRepositoryFixture(t *testing.T, rows ...jobtypes.Job) (repo *testkit.Repo, cacheDir string) {
	t.Helper()
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	repo = testkit.NewRepository(t, map[string]string{"magus.yaml": ""})
	_, cacheDir = seedJobStore(t, repo.Root(), rows...)
	return repo, cacheDir
}

func seedJobStore(t *testing.T, root string, rows ...jobtypes.Job) (string, string) {
	t.Helper()
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	for _, row := range rows {
		_, err := store.Update(t.Context(), row.ID, func(cur *jobtypes.Job) { *cur = row })
		require.NoError(t, err)
	}
	return root, cacheDir
}

// seedFiled appends job id's first try, having filed result, to the
// attempts file beside store's rows. The job package files a result only
// through an exit; internal/guard keeps the same copy.
func seedFiled(t *testing.T, store *job.Store, id string, result *jobtypes.Result) {
	t.Helper()
	path, err := store.Path()
	require.NoError(t, err)
	raw, err := codec.Marshal(result)
	require.NoError(t, err)
	var filed map[string]any
	require.NoError(t, codec.Unmarshal(raw, &filed))
	// An attempt stores its result without the result's own api_version, as the store writes it.
	delete(filed, "api_version")
	line, err := codec.Marshal(map[string]any{
		"apiVersion": "safetyscissors.job/v1alpha1", "kind": "Attempt",
		"spec": map[string]any{"job": id, "n": 1}, "status": map[string]any{"result": fixtureCamelKeys(filed)},
	})
	require.NoError(t, err)
	require.NoError(t, codec.AppendLines(filepath.Join(filepath.Dir(path), "attempts.jsonl"), [][]byte{line}, 0o600))
}

// fixtureCamelKeys is v with every object key camelCased, as the
// attempts.jsonl record spells them.
func fixtureCamelKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			parts := strings.Split(k, "_")
			for i := 1; i < len(parts); i++ {
				parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
			}
			out[strings.Join(parts, "")] = fixtureCamelKeys(x)
		}
		return out
	case []any:
		for i := range v {
			v[i] = fixtureCamelKeys(v[i])
		}
		return v
	}
	return v
}

// A deny path naming a declaration of a file no diff driver reads is refused at fork
// (MGS3031): the guard could only honor it by denying the whole file.
func TestJobForkRefusesAnUngradableDenyPath(t *testing.T) {
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t)
	root := repo.Root()
	repo.WriteTree(map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": "package run\n\nfunc A() {}\n", "notes.txt": "Intro\n"})
	fork := func(id, deny string) error {
		return jobFork(t.Context(), root, []string{id, "--check", "test .", "--write-paths", "run.go,notes.txt", "--deny-paths", deny})
	}

	err := fork("refused", "notes.txt#Intro")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MGS3031")
	assert.Contains(t, err.Error(), `deny path "notes.txt" has no diff driver`)

	require.NoError(t, fork("accepted", "run.go#A"))
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes no row")
	assert.Equal(t, []any{"accepted", []string{"run.go#A"}}, []any{rows[0].ID, rows[0].Paths.Deny})
}

// A retry names the id alone and forks the job again as it was declared. A spec flag
// declares an ended job anew, but may not erase a live job's write paths or check.
func TestJobForkByIdAloneKeepsTheStoredSpec(t *testing.T) {
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t)
	root := repo.Root()
	repo.WriteTree(map[string]string{"run.go": "package run\n"})
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	require.NoError(t, jobFork(t.Context(), root, []string{"w", "--check", "test .", "--write-paths", "run.go", "--instructions", "run"}))
	_, err := job.Exit(t.Context(), store, "w", nil, nil)
	require.NoError(t, err)

	spec := func(r jobtypes.Job) jobtypes.Declaration {
		d := job.RetryDeclaration(r)
		d.Schema = jobtypes.Schema{}
		return d
	}
	first, err := store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, first, 1)

	require.NoError(t, jobFork(t.Context(), root, []string{"w"}))
	rows, err := store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, spec(first[0]), spec(rows[0]), "the retry keeps the declared spec")
	assert.Equal(t, jobtypes.StateDeclared, rows[0].State, "the retry is a new attempt")

	err = jobFork(t.Context(), root, []string{"w", "--read-only", "--instructions", "anew"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this record would erase w's paths.write")
	_, err = job.Exit(t.Context(), store, "w", nil, nil)
	require.NoError(t, err)

	require.NoError(t, jobFork(t.Context(), root, []string{"w", "--read-only", "--instructions", "anew"}))
	rows, err = store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, jobtypes.Declaration{ID: "w", Agent: jobtypes.Instructed("anew"), ReadOnly: true}, spec(rows[0]), "a spec flag declares the job anew")
}

// A row naming no write paths bounds its holder by nothing unless it is read-only, so fork
// refuses it even with a check, and the refusal teaches the script check a scout passes by.
func TestJobForkRefusesARowScopedByNothing(t *testing.T) {
	resetOpenedWorkspaces(t)
	root, cacheDir := execFixture(t)
	fork := func(args ...string) error { return jobFork(t.Context(), root, args) }

	err := fork("scout", "--instructions", "find the callers", "--check", "test .")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scout names no write paths and is not read-only")
	assert.Contains(t, err.Error(), "--read-only")
	assert.Contains(t, err.Error(), `{"check": {"script": "<probe>.buzz"}}`)
	assert.Contains(t, err.Error(), fmt.Sprintf("`%s`", hint.Buzz.With("--record", "<probe>.buzz")))

	require.NoError(t, fork("reader", "--read-only", "--instructions", "find the callers"))
	require.NoError(t, fork("writer", "--write-paths", "run.go", "--check", "test ."))
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List(t.Context())
	require.NoError(t, err)
	got := make(map[string]bool, len(rows))
	for _, r := range rows {
		got[r.ID] = r.ReadOnly
	}
	assert.Equal(t, map[string]bool{"reader": true, "writer": false}, got, "a refused fork writes no row")
}

// A magusfile mid-edit still opens the store for model tiers, so the fork that fixes it can
// be declared, and the open names the tier it left unassigned: a tier declaration that
// failed to load must not read as one that declared nothing.
func TestOpenJobsOpensTheStoreWhenTheWorkspaceWillNotLoad(t *testing.T) {
	resetOpenedWorkspaces(t)
	root, _ := execFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\nmagus\\project({\n"), 0o644))

	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, err := openJobs(t.Context(), root, jobOpen{DryRun: true})
	require.NoError(t, err)
	assert.Empty(t, logged.String(), "a store opened without model tiers never loads the workspace")
	_, err = openJobs(t.Context(), root, jobOpen{ModelTiers: true})
	require.NoError(t, err)
	assert.Contains(t, logged.String(), "no model tier was assigned")
	assert.Contains(t, logged.String(), "magusfile.buzz")
}

// A fork resolves the checkpoint it is handed: a full-length revision the repository
// never held is refused and writes no row, and a real abbreviated one is stored in full.
func TestJobForkResolvesItsCheckpoint(t *testing.T) {
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t)
	root := repo.Root()
	head := repo.Head()
	fork := func(id, checkpoint string) error {
		return jobFork(t.Context(), root, []string{id, "--check", "test .", "--write-paths", "magus.yaml", "--checkpoint", checkpoint})
	}

	const madeUp = "34b41fd546ba8fa9c71a6a64e4ea0f3e93d5a7bc"
	err := fork("made-up", madeUp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), madeUp)
	assert.Contains(t, err.Error(), "`magus vcs checkpoint -o name`")

	captureStdout(t, func() { require.NoError(t, fork("real", head[:9])) })
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes no row")
	assert.Equal(t, []string{"real", head}, []string{rows[0].ID, rows[0].Checkpoint})
}

// A --stdin record and --checkout combine: the fork makes the checkout the flag names and
// judges the record against it, so a live job in the forking checkout refuses nothing.
// The same record without the flag is still refused, and a record naming another checkout
// is refused rather than one of the two being dropped.
func TestJobForkFromStdinMakesTheCheckoutTheFlagNames(t *testing.T) {
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t, jobtypes.Job{
		ID: "live", State: jobtypes.StateRunning, Paths: jobtypes.Paths{Write: []string{"a.go"}},
		Check: &jobtypes.LeaseCheck{Target: "test", Project: "."},
	})
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	_, err := store.Exec(t.Context(), "live")
	require.NoError(t, err)
	record := func(id, checkout string) string {
		return fmt.Sprintf(`{"api_version": %q, "id": %q, "checkout": %q, "paths": {"write": ["magus.yaml"]},`+
			` "check": {"target": "test", "project": "."}}`, jobtypes.APIVersion, id, checkout)
	}
	fork := func(stdin string, args ...string) error {
		writeStdin(t, stdin)
		return jobFork(t.Context(), root, append([]string{"--stdin"}, args...))
	}

	err = fork(record("shared", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live is live in this checkout")

	own := filepath.Join(t.TempDir(), "own")
	err = fork(record("elsewhere", filepath.Join(t.TempDir(), "other")), "--checkout", own)
	require.Error(t, err)
	assert.Contains(t, err.Error(), own)

	captureStdout(t, func() { require.NoError(t, fork(record("own", ""), "--checkout", own)) })
	rows, err := store.List(t.Context())
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(row jobtypes.Job) bool { return row.ID == "own" })
	require.GreaterOrEqual(t, i, 0, "the fork wrote its row")
	require.NotNil(t, rows[i].Worktree)
	assert.Equal(t, own, rows[i].Worktree.Root)
	assert.DirExists(t, own)
}

// A row forked from inside a job's checkout got no worktree record, so the guard refused
// its holder's magus there as a sibling; an integration row forked from the forker's own
// linked checkout bound its integrator to the forker's tree.
func TestJobForkBindsTheCheckoutItRunsIn(t *testing.T) {
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t)
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	fork := func(at, stdin string, args ...string) error {
		resetStartupSingletons()
		writeStdin(t, stdin)
		return jobFork(t.Context(), at, append([]string{"--stdin"}, args...))
	}
	record := func(id, path string) string {
		return fmt.Sprintf(`{"api_version": %q, "id": %q, "paths": {"write": [%q]}, "check": {"target": "test", "project": "."}}`,
			jobtypes.APIVersion, id, path)
	}

	own := filepath.Join(t.TempDir(), "own")
	captureStdout(t, func() { require.NoError(t, fork(root, record("integrate", "magus.yaml"), "--checkout", own)) })
	_, err := store.Update(t.Context(), "integrate", func(u *jobtypes.Job) { u.State = jobtypes.StateExited })
	require.NoError(t, err)
	captureStdout(t, func() { require.NoError(t, fork(own, record("finish", "notes.txt"))) })
	rows, err := store.List(t.Context())
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(row jobtypes.Job) bool { return row.ID == "finish" })
	require.GreaterOrEqual(t, i, 0)
	require.NotNil(t, rows[i].Worktree, "a fork run inside a job's checkout records it")
	assert.True(t, rows[i].Worktree.Standing())
	assert.Equal(t, own, rows[i].Worktree.Root)

	repo.Commit("raise prices", map[string]string{"pricing.txt": "apples 2\n"})
	repo.Git("branch", "done")
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	session := repo.AddWorktree("session").Root()
	integration := fmt.Sprintf(`{"api_version": %q, "id": "merge", "integrates": ["done"], "check": {"target": "test", "project": "."}}`, jobtypes.APIVersion)
	err = fork(session, integration)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the forker's own linked checkout")
	assert.Contains(t, err.Error(), "--checkout <dir>")
}

// The checkpoint of an integration fork into a standing checkout is that checkout's
// revision, not the forker's, so the integrator's merge is judged from where it starts.
func TestJobForkIntegrationIntoAStandingCheckoutRecordsTheCheckoutsRevision(t *testing.T) {
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t)
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})

	standing := filepath.Join(t.TempDir(), "integrator")
	repo.Git("worktree", "add", "-q", "-b", "integrator", standing)
	checkoutHead := repo.Head()
	repo.Commit("raise prices", map[string]string{"pricing.txt": "apples 2\n"})
	repo.Git("branch", "done")
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	repo.Commit("forker moves on", map[string]string{"forker.txt": "ahead\n"})
	require.NotEqual(t, checkoutHead, repo.Head())

	writeStdin(t, fmt.Sprintf(`{"api_version": %q, "id": "merge", "integrates": ["done"], "check": {"target": "test", "project": "."}}`, jobtypes.APIVersion))
	captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"--stdin", "--checkout", standing}))
	})
	rows, err := store.List(t.Context())
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(row jobtypes.Job) bool { return row.ID == "merge" })
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, []string{checkoutHead, "pricing.txt"}, []string{rows[i].Checkpoint, strings.Join(rows[i].Paths.Write, ",")})
}

// IntegrationStandingCheckoutSuite forks an integration row into a checkout
// that stands ahead of the checkpoint the record names.
type IntegrationStandingCheckoutSuite struct {
	suite.Suite
	root, standing, checkoutHead string
	store                        *job.Store
}

func TestIntegrationStandingCheckoutSuite(t *testing.T) {
	suite.Run(t, new(IntegrationStandingCheckoutSuite))
}

func (s *IntegrationStandingCheckoutSuite) SetupTest() {
	t := s.T()
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t)
	s.root = repo.Root()
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: s.root})
	old := repo.Head()
	repo.Commit("land the earlier work", map[string]string{"landed.txt": "landed\n"})
	s.standing = filepath.Join(t.TempDir(), "integrator")
	repo.Git("worktree", "add", "-q", "-b", "integrator", s.standing)
	s.checkoutHead = repo.Head()
	repo.Commit("raise prices", map[string]string{"pricing.txt": "apples 2\n"})
	repo.Git("branch", "done")
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	writeStdin(t, fmt.Sprintf(`{"api_version": %q, "id": "merge", "integrates": ["done"], "checkpoint": %q, "check": {"target": "test", "project": "."}}`, jobtypes.APIVersion, old))
	captureStdout(t, func() {
		s.Require().NoError(jobFork(t.Context(), s.root, []string{"--stdin", "--checkout", s.standing}))
	})
}

func (s *IntegrationStandingCheckoutSuite) row() jobtypes.Job {
	rows, err := s.store.List(s.T().Context())
	s.Require().NoError(err)
	i := slices.IndexFunc(rows, func(row jobtypes.Job) bool { return row.ID == "merge" })
	s.Require().GreaterOrEqual(i, 0)
	return rows[i]
}

func (s *IntegrationStandingCheckoutSuite) TestWritePathsAreWhatTheBranchChangedSinceTheCheckout() {
	s.Equal([]string{"pricing.txt"}, s.row().Paths.Write)
}

func (s *IntegrationStandingCheckoutSuite) TestTheCheckpointIsTheCheckoutsRevision() {
	s.Equal(s.checkoutHead, s.row().Checkpoint)
}

// An integration row that exited red was refused a same-id re-fork once its branch was
// merged ("nothing to merge"), so a small follow-up needed a second row.
func TestJobForkTakesAnUnfinishedIntegrationAgain(t *testing.T) {
	resetOpenedWorkspaces(t)
	repo, cacheDir := execRepositoryFixture(t)
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	repo.Commit("raise prices", map[string]string{"pricing.txt": "apples 2\n"})
	repo.Git("branch", "done")
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	integration := fmt.Sprintf(`{"api_version": %q, "id": "merge", "integrates": ["done"], "check": {"target": "test", "project": "."}}`, jobtypes.APIVersion)
	fork := func() error {
		resetStartupSingletons()
		writeStdin(t, integration)
		return jobFork(t.Context(), root, []string{"--stdin"})
	}

	captureStdout(t, func() { require.NoError(t, fork()) })
	_, err := store.Update(t.Context(), "merge", func(u *jobtypes.Job) { u.State = jobtypes.StateExited })
	require.NoError(t, err)
	repo.Git("merge", "-q", "--ff-only", "done")

	captureStdout(t, func() { require.NoError(t, fork(), "its check never passed, so the row takes the follow-up") })
	rows, err := store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"pricing.txt"}, rows[0].Paths.Write, "the re-fork keeps the paths the row held")

	_, err = store.Delete(t.Context(), "merge", true)
	require.NoError(t, err)
	err = fork()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to merge", "with no row to finish, an empty merge is still refused")
}

// limitHostSpell is a harness whose host waits ten minutes at most on a foreground call.
const limitHostSpell = `import "magus/spell";

export fun mgs_getName() > str { return "limit-host"; }

export fun mgs_getHarness() > Harness {
    return Harness{config = HarnessConfig{path = "hooks.json"}, guard = "hooks"};
}

export fun mgs_getSkills() > HarnessSkills {
    return HarnessSkills{form = SkillForm.short};
}

export fun mgs_getLimits() > Limits {
    return Limits{foregroundDefaultMs = 120000, foregroundMaxMs = 600000};
}

export fun mgs_renderPrompts(humanOnly: {str: str}) > [Prompt] { return []; }
`

// testTarget is the `test` target the fork's check names, which the workspace must have.
const testTarget = "\nexport fun test(ctx: magus\\Context, args: [str]) > void {\n}\n"

// A fork notes a check whose last real run took longer than a wired host lets a foreground
// call wait, naming both numbers, and says nothing when no wired host declares a limit.
func TestJobForkNotesACheckTooLongForTheForeground(t *testing.T) {
	// The spell's mgs_getLimits decode is the bindings' own test. Here the loader answers
	// for the wired id directly, since another test in this package may have cleared the
	// spell-backed one.
	fixture.WireHarness(t, agent.HarnessDescriptor{
		APIVersion: agent.HarnessAPIVersion,
		ID:         "limit-host",
		Display:    agent.HarnessDisplay{Name: "Limit host"},
		Skills:     types.HarnessSkills{Form: types.SkillFormShort},
		Config:     types.HarnessConfig{Path: "hooks.json"},
		Limits:     types.Limits{ForegroundDefaultMs: 120000, ForegroundMaxMs: 600000},
	})
	for _, tt := range []struct {
		name, magusfile, want string
	}{
		{"a wired host with a limit", "import \"magus\";\nimport \"spells/limit-host\" as host;\nmagus\\harness.provider(host);\n" + testTarget,
			fmt.Sprintf("the check `%s` last ran 14m0s here, past the 10m0s a foreground call may wait", hint.Run.With("test", "."))},
		{"no wired host", "import \"magus\";\n" + testTarget, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetOpenedWorkspaces(t)
			root, _ := execFixture(t)
			require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(tt.magusfile), 0o644))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "spells", "limit-host"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "spells", "limit-host", "spell.buzz"), []byte(limitHostSpell), 0o644))
			dir, err := sessions.Dir(root)
			require.NoError(t, err)
			w, err := session.Open(t.Context(), dir, "invmv00001", session.InvocationStart{Command: "run"})
			require.NoError(t, err)
			require.NoError(t, w.Append(t.Context(), session.KindTargetResult, session.TargetResult{
				Target: "test", Project: ".", Outcome: session.OutcomePass, DurationMs: (14 * time.Minute).Milliseconds(),
			}))

			out := captureStdout(t, func() {
				require.NoError(t, jobFork(t.Context(), root, []string{"long", "--check", "test .", "--write-paths", "magus.yaml"}))
			})
			if tt.want == "" {
				assert.NotContains(t, out, "foreground call may wait")
				return
			}
			assert.Contains(t, out, tt.want)
		})
	}
}

// A fork naming a model takes the tier of the harness row that names it.
// The row is what the wired harness publishes, whatever opus is elsewhere.
func TestJobForkTakesTheTierOfAWiredHarnessModelRow(t *testing.T) {
	fixture.WireHarness(t, agent.HarnessDescriptor{
		APIVersion: agent.HarnessAPIVersion,
		ID:         "limit-host",
		Display:    agent.HarnessDisplay{Name: "Limit host"},
		Skills:     types.HarnessSkills{Form: types.SkillFormShort},
		Config:     types.HarnessConfig{Path: "hooks.json"},
		Models:     []model.Row{{ID: "claude-opus-test", Alias: "opus", Tier: "routine"}},
	})
	resetOpenedWorkspaces(t)
	root, _ := execFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\nimport \"spells/limit-host\" as host;\nmagus\\harness.provider(host);\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "spells", "limit-host"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "spells", "limit-host", "spell.buzz"), []byte(limitHostSpell), 0o644))
	writeStdin(t, `{"api_version": "safetyscissors.job/v1alpha1", "id": "wave/opus", "paths": {"write": ["a.go"]}, "agent": {"model": {"id": "opus"}, "worker": {"type": "resumed"}},`+
		` "goals": [{"id": "edited", "kind": "paths", "expect": "changed", "paths": ["a.go"]}]}`)

	out := captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"--stdin"}))
	})
	assert.Contains(t, out, "model tier: routine (rank 0)")
}

// The tool rows a wired harness installs in a checkout are magus's, so ending its job
// removes the checkout; a harness that fails to load keeps it and names the failure,
// since its rows would otherwise read as work.
func TestEndWorktreesRestoresAWiredHarnessToolRows(t *testing.T) {
	harness := agent.HarnessDescriptor{
		APIVersion: agent.HarnessAPIVersion,
		ID:         "limit-host",
		Display:    agent.HarnessDisplay{Name: "Limit host"},
		Skills:     types.HarnessSkills{Form: types.SkillFormShort},
		Config:     types.HarnessConfig{Path: "hooks.json"},
	}
	for _, tt := range []struct {
		name, toolsDir, want string
	}{
		{"a harness that loads", ".host-tools", "removed once job w ended"},
		{"a broken descriptor", "/host-tools", `kept after job w ended: restoring what magus wrote in it failed: loading wired harness limit-host for the tool rows it installs: invalid harness spell "limit-host": tools: dir must be a workspace-relative path`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wired := harness
			wired.Tools = &types.HarnessTools{Dir: tt.toolsDir}
			fixture.WireHarness(t, wired)
			resetOpenedWorkspaces(t)
			t.Cleanup(snapshotGlobals())
			testkit.Isolate(t)
			global = globalFlags{}
			repo := testkit.NewRepository(t, map[string]string{
				"magus.yaml":                   "",
				"magusfile.buzz":               "import \"magus\";\nimport \"spells/limit-host\" as host;\nmagus\\harness.provider(host);\n" + testTarget,
				"spells/limit-host/spell.buzz": limitHostSpell,
				".host-tools/models":           "committed\n",
			})
			_, cacheDir := seedJobStore(t, repo.Root())
			store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: repo.Root()})
			wt, made, err := store.ForkWorktree(t.Context(), "w", filepath.Join(t.TempDir(), "w"), "", "")
			require.NoError(t, err)
			require.True(t, made)
			_, err = store.Update(t.Context(), "w", func(cur *jobtypes.Job) {
				*cur = jobtypes.Job{ID: "w", State: jobtypes.StatePass, Paths: jobtypes.Paths{Write: []string{"a.go"}}}
			})
			require.NoError(t, err)
			_, err = store.RecordWorktree(t.Context(), "w", wt)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(wt.Root, ".host-tools", "models"), []byte("reinstalled\n"), 0o644))

			var out bytes.Buffer
			require.NoError(t, endWorktrees(t.Context(), &out, store, "w"))
			assert.Contains(t, out.String(), tt.want)
		})
	}
}

// The store the CLI opens resolves a ref the worker recorded in its own checkout, so an
// orchestrator can exit a job on its worker's behalf.
func TestJobExitResolvesARefInTheJobsCheckout(t *testing.T) {
	worker := t.TempDir()
	recorded, err := cache.NewOutputStore(filepath.Join(worker, ".magus")).
		Persist(t.Context(), "key1", []byte("ok\n"), cache.OutputDescriptor{Project: ".", Target: "test", DurationMs: 1500, Revision: "abc123", Dirty: true})
	require.NoError(t, err)
	require.NotEmpty(t, recorded.Attempt)

	row := leaseRow("w", "")
	row.CheckoutRoot, row.Registered = worker, 1
	root, _ := execFixture(t, row)
	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	here := func(context.Context, string) (jobtypes.CapturedRun, error) { return jobtypes.CapturedRun{}, nil }
	result := jobtypes.Result{Job: row.ID, Validation: jobtypes.ResultValidation{OutputRef: recorded.Ref}}

	stored, err := job.Exit(t.Context(), store, row.ID, &result, here)
	require.NoError(t, err)
	assert.Equal(t, &jobtypes.CapturedRun{
		Found: true, Ref: recorded.Ref, Project: ".", Target: "test",
		AttemptID: recorded.Attempt, DurationMs: 1500, Revision: "abc123", Dirty: true, ObservedGeneration: 1,
	}, stored.Attempt, "the attempt carries what freshness is judged by: the execution, its start and the tree it read")
}

// --evidence names a run the WAITER recorded. The holder's checkout is the primary one, the
// checkout `magus query output` falls back to, so a resolver that searched other checkouts
// would find the holder's own passing run there and certify it as the waiter's.
func TestJobWaitEvidenceResolvesOnlyInTheWaitersCheckout(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	holder := testkit.NewRepository(t, map[string]string{"magus.yaml": "", "a.go": "package a\n"})
	waiter := holder.AddWorktree("waiter")
	holderCache, err := magus.ResolveCacheDir(holder.Root())
	require.NoError(t, err)
	passed, err := cache.NewOutputStore(holderCache).
		Persist(t.Context(), "key1", []byte("ok\n"), cache.OutputDescriptor{Project: ".", Target: "go-test", TimestampMs: time.Now().UnixMilli()})
	require.NoError(t, err)

	found, err := storedAttempts(holder.Root())(t.Context(), passed.Ref)
	require.NoError(t, err)
	require.True(t, found.Found, "the control: the holder's checkout holds the run")
	missed, err := storedAttempts(waiter.Root())(t.Context(), passed.Ref)
	require.NoError(t, err)
	assert.False(t, missed.Found, "the waiter's resolver reads its own store alone")

	row := jobtypes.Job{
		ID: "w", Created: 1, State: jobtypes.StateExited, Paths: jobtypes.Paths{Write: []string{"a.go"}}, CheckoutRoot: holder.Root(),
		Check:   &jobtypes.LeaseCheck{Target: "go-test", Project: "."},
		Attempt: &jobtypes.CapturedRun{Found: true, Ref: "failed", Project: ".", Target: "go-test", Failed: true, TimestampMs: 2_000},
	}
	seedJobStore(t, waiter.Root(), row)
	store, err := openJobs(t.Context(), waiter.Root(), jobOpen{})
	require.NoError(t, err)
	seedFiled(t, store, "w",
		&jobtypes.Result{Job: "w", ChangedPaths: []string{"a.go"}, Validation: jobtypes.ResultValidation{OutputRef: "failed"}})
	observe := func(context.Context, jobtypes.Job) (job.Observed, error) {
		return job.Observed{Changed: []string{"a.go"}, ChangedKnown: true}, nil
	}

	_, err = job.Wait(t.Context(), store, row.ID, nil, storedAttempts(waiter.Root()), observe, map[string]string{jobtypes.PrimaryGoalID: passed.Ref})

	require.ErrorContains(t, err, "names no run this checkout recorded")
	rows, err := store.List(t.Context())
	require.NoError(t, err)
	assert.Equal(t, jobtypes.StateExited, rows[0].State)
}

// jobApplyStateSuite pins what `job apply` says of a state it moved.
type jobApplyStateSuite struct {
	suite.Suite
}

func TestJobApplyState(t *testing.T) { suite.Run(t, new(jobApplyStateSuite)) }

func (s *jobApplyStateSuite) TestAReleaseAPatternStillNamesIsNoted() {
	row := leaseRow("w", "")
	row.State, row.Paths.Write = jobtypes.StateRunning, []string{"src/*.go"}
	row.Releases = []jobtypes.Release{{Path: "src/a.go", Digest: jobtypes.DigestAbsent, ReleasedAt: 1}}
	root, cacheDir := execFixture(s.T(), row)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: cacheDir, Root: root})

	notes := applyClaimNotes(s.T().Context(), store, []job.Applied{{Prev: row, Next: row}})

	s.Require().Len(notes["w"], 1)
	s.Contains(notes["w"][0], "src/a.go stays released, so the pattern naming it does not hold it")
}

// A workspace with no symbol index cannot say what references the write paths, so a fork
// says the list is unknown instead of printing nothing.
func TestJobForkSaysReferencedOutsideIsUnknownWithoutASymbolIndex(t *testing.T) {
	resetOpenedWorkspaces(t)
	root, _ := execFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"+testTarget), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644))

	out := captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"unindexed", "--check", "test .", "--write-paths", "a.go"}))
	})
	assert.Contains(t, out, fmt.Sprintf("\n  unknown, no symbol index answered; `%s` builds one\n", hint.GraphBuild.String()))

	prev := global.output
	t.Cleanup(func() { global.output = prev })
	global.output = "json"
	raw := captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"unindexed-json", "--check", "test .", "--write-paths", "a.go"}))
	})
	var got map[string]any
	require.NoError(t, codec.Unmarshal([]byte(raw), &got))
	assert.Equal(t, job.NoSymbolIndex(), got["referenced_outside_gap"], "-o json carries the gap beside the empty list")
}

// bindCheckout records id as the checkout's binding, as the guard does when a caller whose
// host names no session takes a job there.
func bindCheckout(t *testing.T, cacheDir, id string) {
	t.Helper()
	require.NoError(t, job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir}).Bind(job.Caller{}, id))
}

// Exec takes exactly one job: the read form it had printed a binding the CLI no longer
// holds, and a job that already ended has nothing left to take.
func TestJobPauseRefusesExecUntilResume(t *testing.T) {
	row := leaseRow("lease-enforcement/wave4/paused", "")
	row.State = jobtypes.StateDeclared
	repo, cacheDir := execRepositoryFixture(t, row)
	root := repo.Root()

	captureStdout(t, func() {
		require.NoError(t, jobPause(t.Context(), root, "pause", []string{"--reason", "release build", "--until", "1h", row.ID}))
	})
	err := jobExec(t.Context(), root, []string{row.ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), row.ID+" is paused by ")
	assert.Contains(t, err.Error(), ": release build (until ")
	assert.Contains(t, err.Error(), "job resume "+row.ID)
	bindCheckout(t, cacheDir, row.ID)
	err = jobExec(t.Context(), root, []string{"--timeout", "1m", "--", "true"})
	require.Error(t, err, "a command under the paused lease is refused too")
	assert.Contains(t, err.Error(), row.ID+" is paused")

	assert.Error(t, jobPause(t.Context(), root, "resume", []string{row.ID}), "the holder does not lift its own pause")

	orchestrator := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root, Actor: &jobtypes.Actor{}})
	_, err = orchestrator.Resume(t.Context(), row.ID)
	require.NoError(t, err)
	captureStdout(t, func() {
		require.NoError(t, jobExec(t.Context(), root, []string{row.ID}))
	})
}

func TestJobExecRefusesAnythingButOneLiveJob(t *testing.T) {
	done := leaseRow("lease-enforcement/wave4/done", "")
	done.State = jobtypes.StatePass
	repo, _ := execRepositoryFixture(t, done)
	root := repo.Root()

	for name, args := range map[string][]string{
		"no job":        nil,
		"two jobs":      {"a", "b"},
		"an unknown id": {"harness/no-such-job"},
		"an ended job":  {done.ID},
	} {
		assert.Error(t, jobExec(t.Context(), root, args), name)
	}
}

// A fork under `job exec --timeout` bootstraps with the wrapper holding the
// whole budget. Under budget_wait 0 a stranger's claim of that size refuses the
// same run.
func TestAForkUnderJobExecSharesItsClaim(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	privateSockDir(t)
	t.Setenv("MAGUS_BROKER", "")
	t.Setenv("MAGUS_BUDGET_WAIT", "0")
	ln, err := broker.Listen(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- broker.Serve(ctx, ln, broker.WithCapacity(0, 1)) }()
	t.Cleanup(func() { cancel(); <-done })

	root := testkit.NewRepository(t, map[string]string{
		"magus.yaml":     "disk:\n  critical_free_mb: 0\ncheckout:\n  bootstrap: hack/dev/bootstrap-checkout.buzz\n",
		"magusfile.buzz": "import \"magus\";\n" + testTarget,
		"a.go":           "package a\n",
		filepath.FromSlash("hack/dev/bootstrap-checkout.buzz"): "import \"proc\";\n\nexport fun main(args: [str]) > void !> any {\n" +
			"    _ = proc\\exec(\"./magus\", args: [\"run\", \"--no-cache\", \"test\", \".\"]);\n}\n",
	}).Root()
	// A hard link: Linux resolves a symlink, and a re-exec reruns the suite.
	self, err := os.Executable()
	require.NoError(t, err)
	bin := filepath.Join(root, "magus")
	if err := os.Link(self, bin); err != nil {
		data, err := os.ReadFile(self)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(bin, data, 0o755))
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	stranger, err := broker.Dial(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	defer func() { _ = stranger.Close() }()
	v, err := stranger.Request(t.Context(), budget.Claim{Project: "other", Target: "test", Slots: 1, PID: 1})
	require.NoError(t, err)
	out, err := run("run", "--no-cache", "test", ".")
	require.Error(t, err, out)
	assert.Contains(t, out, "MGS3009")
	stranger.Release(t.Context(), v.ID)

	checkout := filepath.Join(t.TempDir(), "under-exec")
	out, err = run("job", "exec", "--timeout", "2m", "--", bin,
		"job", "fork", "under-exec", "--check", "test .", "--write-paths", "a.go", "--checkout", checkout)
	require.NoError(t, err, out)
	assert.NotContains(t, out, "MGS3009")
	assert.DirExists(t, checkout)
}

// The base is read from the tree, so there is no flag to type one: --base is an unknown
// flag, refused before the row is touched.
func TestJobExecRefusesATypedBase(t *testing.T) {
	row := leaseRow("lease-enforcement/wave4/docs", "")
	row.State = jobtypes.StateDeclared
	repo, cacheDir := execRepositoryFixture(t, row)

	err := jobExec(t.Context(), repo.Root(), []string{row.ID, "--base", repo.Head()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "-base")

	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: repo.Root()}).List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []any{jobtypes.StateDeclared, ""}, []any{rows[0].State, rows[0].ReportedBase}, "a refused exec takes nothing")
}

func TestLsJobsSaysWhenTheQueueWasNeverFetched(t *testing.T) {
	root, _ := execFixture(t, leaseRow("fleet", ""))
	withOutput(t, "")
	out := captureStdout(t, func() {
		require.NoError(t, lsJobs(root, nil))
	})
	assert.Contains(t, out, "in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes\n")
}

// guideMarker pins the fenced block on the next line to a section of a job_people script.
var guideMarker = regexp.MustCompile(`^<!-- golden: (job_people_[a-z]+\.txtar) (\S+) -->$`)

// The job guides show people running the CLI, and their transcripts are the outputs the
// job_people scripts compare byte for byte: a block marked with guideMarker must equal
// "$ <the command>" plus the golden that command's stdout was compared against, or, for a
// section no command's stdout is compared against (an input file), the section itself.
// Every job_people script is shown somewhere, so a script nobody reads cannot pass for
// documentation.
func TestJobGuideMatchesItsScripts(t *testing.T) {
	t.Parallel()
	guides := filepath.Join("..", "..", "docs", "guides")
	pages, err := filepath.Glob(filepath.Join(guides, "jobs*.md"))
	require.NoError(t, err)
	more, err := filepath.Glob(filepath.Join(guides, "jobs", "*.md"))
	require.NoError(t, err)
	pages = append(pages, more...)
	require.NotEmpty(t, pages)

	shown := map[string]bool{}
	for _, page := range pages {
		data, err := os.ReadFile(page)
		require.NoError(t, err)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			m := guideMarker.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// One blank line may separate the two, as the formatter leaves it.
			fence := i + 1
			if fence < len(lines) && lines[fence] == "" {
				fence++
			}
			require.Less(t, fence, len(lines), "%s:%d: a marker with no block under it", page, i+1)
			require.True(t, strings.HasPrefix(lines[fence], "```"), "%s:%d: the marker must sit above a fence", page, i+1)
			end := slices.Index(lines[fence+1:], "```")
			require.GreaterOrEqual(t, end, 0, "%s:%d: the fenced block never closes", page, fence+1)
			got := strings.Join(lines[fence+1:fence+1+end], "\n") + "\n"
			assert.Equal(t, guideBlock(t, m[1], m[2]), got, "%s:%d shows something %s does not pin", page, i+1, m[1])
			shown[m[1]] = true
		}
	}
	scripts, err := filepath.Glob(filepath.Join("testdata", "script", "job_people_*.txtar"))
	require.NoError(t, err)
	require.NotEmpty(t, scripts)
	for _, script := range scripts {
		assert.True(t, shown[filepath.Base(script)], "no job guide shows %s", script)
	}
}

// guideBlock renders what a guide must show for section of script: the section as is, or,
// when a `cmp stdout $WORK/<section>` compares a command's output against it, that command
// as a transcript line followed by the section. The command is the nearest `exec magus`
// above the cmp, since the steps between only normalize its output. A stdin the archive
// carries is shown as the heredoc a person would type; one the script generated, as a
// redirect from the file of that name.
func guideBlock(t *testing.T, script, section string) string {
	t.Helper()
	archive, err := txtar.ParseFile(filepath.Join("testdata", "script", script))
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range archive.Files {
		files[f.Name] = string(f.Data)
	}
	golden, ok := files[section]
	require.True(t, ok, "%s has no section %s", script, section)

	steps := strings.Split(string(archive.Comment), "\n")
	cmp := slices.Index(steps, "cmp stdout $WORK/"+section)
	if cmp < 0 {
		return golden
	}
	run := cmp - 1
	for run >= 0 && !strings.HasPrefix(strings.TrimPrefix(steps[run], "! "), "exec magus ") {
		run--
	}
	require.GreaterOrEqual(t, run, 0, "%s compares %s against no magus command", script, section)
	command := "$ " + strings.TrimPrefix(strings.TrimPrefix(steps[run], "! "), "exec ")
	if run > 0 {
		if in, ok := strings.CutPrefix(steps[run-1], "stdin $WORK/"); ok {
			if body, archived := files[in]; archived {
				return fmt.Sprintf(`%s <<'EOF'
%sEOF
%s`, command, body, golden)
			}
			command += " < " + in
		}
	}
	return fmt.Sprintf("%s\n%s", command, golden)
}

// A -run that leaves out a test of the write paths' package is refused at fork, listing
// the tests and a pattern that covers them, and a check running the whole package passes.
func TestJobForkRefusesACheckThatSkipsATestOfItsPackage(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepository(t, map[string]string{
		"magus.yaml":     "{}\n",
		"magusfile.buzz": "import \"magus\";\nimport \"magus/spell/go\";\n\nmagus\\project({\"spells\": [go]});\n",
		"go.mod":         goModule,
		"pkg/a.go":       "package pkg\n",
		"pkg/a_test.go":  "package pkg\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\nfunc TestBeta(t *testing.T) {}\n",
	}).Root()
	resetOpenedWorkspaces(t)
	t.Chdir(root)
	root, cacheDir := seedJobStore(t, root)
	fork := func(check string) error {
		return jobFork(t.Context(), root, []string{"w", "--write-paths", "pkg/a.go", "--check", check})
	}

	err := fork("go-test . -- -run Alpha")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkg (TestBeta)")
	assert.Contains(t, err.Error(), "`Alpha|^(TestBeta)$`")

	require.NoError(t, fork("go-test . -- -run Alpha|^(TestBeta)$"))
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1, "the refused fork wrote no row")
}

// goModule makes a fixture repository a Go module, which the go spell the deny-rule
// magusfiles bind needs for the checks they name to exist.
const goModule = "module example.com/w\n\ngo 1.25\n"

// denyWorkerBuildRule is a magusfile whose command rule denies a worker the go-build target.
const denyWorkerBuildRule = `import "magus";
import "magus/spell/go";

magus\project({"spells": [go]});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (req.role == magus\AgentRole.worker and c.args.indexOf("go-build") != null) {
            return magus\guard.deny("a worker does not build magus");
        }
    }
    return magus\guard.defer();
});
`

// The fork judges the check as its not-yet-stored worker would meet the guard: the rule
// that turns on the worker role denies it, the same line from the orchestrator would pass,
// and the refusal names the target that compiles without linking the binary.
func TestJobForkRefusesACheckTheGuardDeniesItsWorker(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyWorkerBuildRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetOpenedWorkspaces(t)
	t.Chdir(root)
	fork := func(check string) error {
		return jobFork(t.Context(), root, []string{"w", "--write-paths", "run.go", "--check", check})
	}

	err := fork("go-build .")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "w's check `magus run go-build .` would be denied to its worker by the guard")
	assert.Contains(t, err.Error(), "a worker does not build magus")
	assert.Contains(t, err.Error(), "`go::go-vet .`")

	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	rows, err := store.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, rows, "grading dry-runs the guard and the refused fork wrote no row")

	require.NoError(t, fork("go::go-vet ."))
	rows, err = store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// denyBuildFromElsewhereRule is a magusfile whose command rule denies a worker the go-build
// target only when the call runs in a directory named "elsewhere": a stand-in for any answer
// that depends on where the guard is asked from.
const denyBuildFromElsewhereRule = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (req.role == magus\AgentRole.worker and c.args.indexOf("go-build") != null and req.dir.indexOf("elsewhere") != null) {
            return magus\guard.deny("judged from the wrong directory");
        }
    }
    return magus\guard.defer();
});
`

// The grade follows the workspace the store was opened for, not the cwd.
// A daemon serving magus\workspace().putJob runs from somewhere else.
// The rule answers differently from the "elsewhere" directory.
// So a grade that took the process cwd would deny the check.
func TestGradeCheckJudgesFromTheWorkspaceNotTheWorkingDirectory(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyBuildFromElsewhereRule, "run.go": "package run\n"}).Root()
	resetOpenedWorkspaces(t)
	row := jobtypes.Job{ID: "w", Paths: jobtypes.Paths{Write: []string{"run.go"}}, Check: &jobtypes.LeaseCheck{Target: "go-build", Project: "."}}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.MkdirAll(elsewhere, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(elsewhere, "magus.yaml"), []byte("{}\n"), 0o644))

	// The control: asked without a Dir, from elsewhere, the same request is denied. This is
	// the grade gradeCheck would reach with no Dir, so the cases below fail without it.
	t.Run("the rule answers differently from elsewhere", func(t *testing.T) {
		t.Chdir(elsewhere)
		unplaced := guard.Judge(t.Context(), guardDependencies(t.Context(), root), guard.Request{
			Input:      "./" + guard.WorkerCheckCommand(*row.Check),
			DryRun:     true,
			Lease:      row.ID,
			PendingRow: &row,
		})
		assert.Equal(t, guardverdict.Deny, unplaced.Decision)
		assert.Contains(t, unplaced.Reason, "judged from the wrong directory")
	})

	for name, cwd := range map[string]string{"the root": root, "elsewhere": elsewhere} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(cwd)
			verdict := gradingWith(nil)(t.Context(), root, row)
			assert.Empty(t, verdict.NotGraded)
			assert.False(t, verdict.Denied, "the check is judged from the workspace, wherever the process runs: %s", verdict.Reason)
		})
	}
}

// denyBuildRule is a magusfile whose command rule denies a worker the go-build target
// wherever the call runs.
const denyBuildRule = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (req.role == magus\AgentRole.worker and c.args.indexOf("go-build") != null) {
            return magus\guard.deny("builds are refused here");
        }
    }
    return magus\guard.defer();
});
`

// A grader outlives one command in a daemon serving magus\workspace().putJob.
// So a rule edited after its first grade must apply to the next.
// The edit tightens the rules: a relaxing edit is held back and proves nothing.
func TestCheckGraderReadsAnEditedRuleAgain(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyBuildFromElsewhereRule, "run.go": "package run\n"}).Root()
	resetOpenedWorkspaces(t)
	t.Chdir(root)
	row := jobtypes.Job{ID: "w", Paths: jobtypes.Paths{Write: []string{"run.go"}}, Check: &jobtypes.LeaseCheck{Target: "go-build", Project: "."}}
	grade := gradingWith(nil)

	before := grade(t.Context(), root, row)
	require.Empty(t, before.NotGraded)
	require.False(t, before.Denied, "the rule allows the check from the root: %s", before.Reason)

	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(denyBuildRule), 0o644))

	after := grade(t.Context(), root, row)
	assert.Empty(t, after.NotGraded)
	assert.True(t, after.Denied, "the edited rule denies the check")
	assert.Contains(t, after.Reason, "builds are refused here")
}

// One judge: the check a fork grades is the check `describe job` briefs and the guard lets
// the holder run. A row forked with --checkout gates in that checkout, so its `ci` check is
// accepted by all three; the same row with no checkout of its own is refused by all three.
func TestGradeCheckJudgesInTheJobsOwnCheckout(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	repo := testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": "", "go.mod": goModule, "run.go": "package run\n"})
	root := repo.Root()
	own := filepath.Join(t.TempDir(), "own")
	repo.Git("worktree", "add", "-q", "--detach", own)
	resetOpenedWorkspaces(t)
	t.Chdir(root)
	ci := &jobtypes.LeaseCheck{Target: types.TargetCI, Project: "."}
	held := jobtypes.Job{ID: "w", State: jobtypes.StateDeclared, Paths: jobtypes.Paths{Write: []string{"run.go"}}, Check: ci, Worktree: &jobtypes.Worktree{Root: own}}
	shared := held
	shared.Worktree = nil
	deps := guardDependencies(t.Context(), root)

	t.Run("its own checkout", func(t *testing.T) {
		verdict := gradingWith(nil)(t.Context(), root, held)
		assert.Empty(t, verdict.NotGraded)
		assert.False(t, verdict.Denied, "the holder gates in the checkout it holds: %s", verdict.Reason)
		require.NoError(t, jobRefusesTheGate(t.Context(), root, held), "describe briefs what the fork accepted")

		taken := held
		taken.State, taken.CheckoutRoot = jobtypes.StateRunning, own
		ran := guard.Judge(t.Context(), deps, guard.Request{Input: "./" + guard.WorkerCheckCommand(*ci), DryRun: true, Lease: taken.ID, PendingRow: &taken, Dir: own})
		assert.NotEqual(t, guardverdict.Deny, ran.Decision, "the guard lets the holder run it there: %s", ran.Reason)
	})

	t.Run("the forker's checkout", func(t *testing.T) {
		verdict := gradingWith(nil)(t.Context(), root, shared)
		assert.True(t, verdict.Denied, "a job's check is never the gate in a shared checkout")
		require.Error(t, jobRefusesTheGate(t.Context(), root, shared))
	})
}

// denyEveryWorkerRule is a magusfile whose command rule denies a worker every command, the
// way the stale-binary rule does: it describes the binary, not the check.
const denyEveryWorkerRule = `import "magus";
import "magus/spell/go";

magus\project({"spells": [go]});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.role == magus\AgentRole.worker) {
        return magus\guard.deny("the hook binary is older than the tree\nrebuild it");
    }
    return magus\guard.defer();
});
`

// A deny that holds for the compiling check too is about the binary or the workspace, so
// it is no verdict on the check: the fork is refused as ungraded, never as a denied check,
// and never accepted.
func TestJobForkRefusesAsUngradedADenyThatHoldsForEveryCommand(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyEveryWorkerRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetOpenedWorkspaces(t)
	t.Chdir(root)
	row := jobtypes.Job{ID: "w", Paths: jobtypes.Paths{Write: []string{"run.go"}}, Check: &jobtypes.LeaseCheck{Target: "go-build", Project: "."}}

	verdict := gradingWith(nil)(t.Context(), root, row)

	assert.False(t, verdict.Denied)
	assert.Contains(t, verdict.NotGraded, "denies this worker `magus run go::go-vet .` as well")
	assert.Contains(t, verdict.NotGraded, "the hook binary is older than the tree", "the first line of the verdict is quoted")
	assert.NotContains(t, verdict.NotGraded, "rebuild it")

	err := jobFork(t.Context(), root, []string{"w", "--write-paths", "run.go", "--check", "go-build ."})
	require.ErrorIs(t, err, types.JobUngraded)
	assert.NotErrorIs(t, err, types.JobCheckUnrunnable)
	assert.Equal(t, 1, strings.Count(err.Error(), string(types.JobUngraded)), "reported once: %s", err)
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, rows, "a fork nobody graded writes nothing")
}

// gradingWith is the grader runCLI installs, loading the rules the CLI loads with mutate
// applied to them.
func gradingWith(mutate func(*guard.Dependencies)) job.CheckGrader {
	return guard.NewCheckGrader(func(ctx context.Context, root string) (guard.Dependencies, *guard.WorkspaceRules) {
		deps, rules := loadGuardDependencies(ctx, root)
		if mutate != nil {
			mutate(&deps)
		}
		return deps, rules
	})
}

// `magus job fork` grades with a forkGrader of its own, not the store's grader, so its
// kept verdicts live no longer than the fork. The store's grader serves the doors that put a row
// without forking: std's TestMagusPutJobGradesTheCheckLikeFork pins those.
func TestJobForkGradesWithItsOwnGrader(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyWorkerBuildRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetOpenedWorkspaces(t)
	t.Chdir(root)

	err := jobFork(t.Context(), root, []string{"w", "--write-paths", "run.go", "--check", "go-build ."})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a worker does not build magus", "the fork's own grader judged the check")
}

// GradeCheckLapseSuite pins that a guard which ran out of time reached no verdict on
// the check, so the grade defers where a real deny refuses.
type GradeCheckLapseSuite struct {
	suite.Suite
}

func TestGradeCheckLapseSuite(t *testing.T) {
	suite.Run(t, new(GradeCheckLapseSuite))
}

func (s *GradeCheckLapseSuite) TestALapseDefersTheGrade() {
	s.T().Cleanup(snapshotGlobals())
	testkit.Isolate(s.T())
	global = globalFlags{}
	root := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyBuildFromElsewhereRule, "run.go": "package run\n"}).Root()
	resetOpenedWorkspaces(s.T())
	row := jobtypes.Job{ID: "w", Paths: jobtypes.Paths{Write: []string{"run.go"}}, Check: &jobtypes.LeaseCheck{Target: "go-build", Project: "."}}
	ctx, cancel := context.WithTimeout(s.T().Context(), time.Second)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	hang := gradingWith(func(deps *guard.Dependencies) {
		deps.Inspect = func(context.Context, string) (types.WorkspaceRepository, error) {
			<-release
			return nil, errors.New("released")
		}
	})

	verdict := hang(ctx, root, row)

	s.False(verdict.Denied, verdict.Reason)
	s.Contains(verdict.Deferred, "the guard ran out of time grading it")
}

type jobApplyAddWritePathSuite struct {
	suite.Suite
	root  string
	store *job.Store
}

func TestJobApplyAddWritePath(t *testing.T) { suite.Run(t, new(jobApplyAddWritePathSuite)) }

func (s *jobApplyAddWritePathSuite) SetupTest() {
	row := leaseRow("w", "")
	row.State, row.Paths.Write = jobtypes.StateRunning, []string{"a.go"}
	row.Check = &jobtypes.LeaseCheck{Target: "test", Project: "."}
	row.Validation = job.CheckCommand(*row.Check)
	root, cacheDir := execFixture(s.T(), row)
	s.root = root
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: cacheDir, Root: root})
}

func (s *jobApplyAddWritePathSuite) TestNamingRecordsAsWellIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"w", "--add-write-path", "b.go", "-f", "-"}))
}

func (s *jobApplyAddWritePathSuite) TestWithoutAnIdIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"--add-write-path", "b.go"}))
}

type jobApplyFailureSuite struct {
	suite.Suite
}

func TestJobApplyFailure(t *testing.T) { suite.Run(t, new(jobApplyFailureSuite)) }

func (s *jobApplyFailureSuite) TestARecordFileThatWillNotOpenFailsWithItsChain() {
	err := jobApply(s.T().Context(), s.T().TempDir(), []string{"-f", filepath.Join(s.T().TempDir(), "absent.jsonl")})

	s.Require().ErrorIs(err, os.ErrNotExist)
	s.Equal(exit.Failure, exit.Code(err))
}

type forkFactsReportSuite struct {
	suite.Suite
	logged bytes.Buffer
}

func TestForkFactsReport(t *testing.T) { suite.Run(t, new(forkFactsReportSuite)) }

func (s *forkFactsReportSuite) SetupTest() {
	s.logged.Reset()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.logged, nil)))
	s.T().Cleanup(func() { slog.SetDefault(prev) })
}

func (s *forkFactsReportSuite) TestAConfigThatWillNotLoadIsNamedInsteadOfAnEmptyStore() {
	root := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{ not yaml\n"}).Root()
	store := job.NewStore(job.Location{StateBase: s.T().TempDir(), CacheDir: s.T().TempDir(), Root: root})

	reportForkFacts(s.T().Context(), store, root)

	s.Contains(s.logged.String(), "the output store could not be read")
	s.NotContains(s.logged.String(), "holds no passed full test")
}

type jobApplyContractSuite struct{ suite.Suite }

func TestJobApplyContract(t *testing.T) { suite.Run(t, new(jobApplyContractSuite)) }

func (s *jobApplyContractSuite) apply(root string, record jobtypes.Declaration) (string, error) {
	s.T().Helper()
	data, err := codec.Marshal(record)
	s.Require().NoError(err)
	path := filepath.Join(s.T().TempDir(), "job.jsonl")
	s.Require().NoError(os.WriteFile(path, append(data, '\n'), 0o600))
	var applyErr error
	out := captureStdout(s.T(), func() {
		applyErr = jobApply(s.T().Context(), root, []string{"-f", path})
	})
	return out, applyErr
}

func (s *jobApplyContractSuite) integration(integrates ...string) (string, *job.Store, jobtypes.Declaration) {
	s.T().Helper()
	resetOpenedWorkspaces(s.T())
	row := leaseRow("integration", "")
	row.Paths.Write = []string{"magus.yaml"}
	row.Check = &jobtypes.LeaseCheck{Target: "test", Project: "."}
	row.Validation = job.CheckCommand(*row.Check)
	root, cacheDir := execFixture(s.T(), row)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: cacheDir, Root: root})
	return root, store, jobtypes.Declaration{
		Schema: jobtypes.Schema{Version: jobtypes.APIVersion}, ID: row.ID,
		Agent: row.Agent.Clone(), Paths: jobtypes.Paths{Write: row.Paths.Write}, Check: row.Check, Integrates: integrates,
	}
}

func (s *jobApplyContractSuite) stored(store *job.Store) jobtypes.Job {
	s.T().Helper()
	rows, err := store.List(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	return rows[0]
}

func (s *jobApplyContractSuite) assertStored(store *job.Store, want jobtypes.Job) {
	s.T().Helper()
	got := s.stored(store)
	// Each accepted apply stamps validation and appends a new store generation.
	// Updated is in seconds, so it moves only when the apply crosses one.
	want.Validated, want.Generation, want.Updated = got.Validated, got.Generation, got.Updated
	s.Equal(want, got)
}

func (s *jobApplyContractSuite) TestIntegratesIsReportedPersistedAndIdempotent() {
	root, store, record := s.integration("worker")
	want := s.stored(store)
	want.Integrates = []string{"worker"}
	out, err := s.apply(root, record)
	s.Require().NoError(err)
	s.Contains(out, "updated integration, still running:\n  integrates +worker\n")
	s.assertStored(store, want)

	out, err = s.apply(root, record)
	s.Require().NoError(err)
	s.Equal("unchanged integration\n", out)
	s.assertStored(store, want)
}

func (s *jobApplyContractSuite) TestDryRunReportsIntegratesWithoutPersistingIt() {
	root, store, record := s.integration("worker")
	before := s.stored(store)
	globalCfg.DryRun = true
	out, err := s.apply(root, record)
	s.Require().NoError(err)
	s.Contains(out, "would update integration, still running:\n  integrates +worker\n")
	s.Contains(out, "dry run: nothing written")
	s.Equal(before, s.stored(store))
}

func (s *jobApplyContractSuite) TestOmittingIntegratesClearsTheStoredSpec() {
	root, store, record := s.integration("worker")
	_, err := s.apply(root, record)
	s.Require().NoError(err)
	want := s.stored(store)
	want.Integrates, record.Integrates = nil, nil
	out, err := s.apply(root, record)
	s.Require().NoError(err)
	s.Contains(out, "integrates -worker\n")
	s.assertStored(store, want)
}

func (s *jobApplyContractSuite) TestAnUnknownSettlementExitsFailureAndWritesNothing() {
	resetOpenedWorkspaces(s.T())
	root, cacheDir := execFixture(s.T())
	_, err := s.apply(root, jobtypes.Declaration{
		Schema: jobtypes.Schema{Version: jobtypes.APIVersion}, ID: "absent",
		LeftUnmerged: "superseded by another change",
	})
	s.Require().ErrorContains(err, "nothing declares job absent, so nothing settles")
	s.Equal(exit.Failure, exit.Code(err))
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: cacheDir, Root: root}).List(s.T().Context())
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *jobApplyContractSuite) TestAnInvalidFiledResultExitsUsageAndKeepsTheJob() {
	root, store, _ := s.integration()
	before, err := store.List(s.T().Context())
	s.Require().NoError(err)
	writeStdin(s.T(), `{"api_version":"safetyscissors.job-result/v1alpha1","unexpected":"field"}`)
	err = jobExit(s.T().Context(), root, []string{"integration", "--stdin"})
	s.Require().ErrorContains(err, "File this shape:")
	s.Equal(exit.Usage, exit.Code(err))
	after, err := store.List(s.T().Context())
	s.Require().NoError(err)
	s.Equal(before, after)
}

func (s *jobApplyContractSuite) TestAMalformedRecordExitsUsage() {
	resetOpenedWorkspaces(s.T())
	root, _ := execFixture(s.T())
	path := filepath.Join(s.T().TempDir(), "broken.jsonl")
	s.Require().NoError(os.WriteFile(path, []byte("{\n"), 0o600))
	err := jobApply(s.T().Context(), root, []string{"-f", path})
	s.Require().ErrorContains(err, "magus job apply:")
	s.Equal(exit.Usage, exit.Code(err))
}

type forkErrorsSuite struct{ suite.Suite }

func TestForkErrors(t *testing.T) { suite.Run(t, new(forkErrorsSuite)) }

func (s *forkErrorsSuite) TestUsageFormattingPreservesTheCauseAndStatus() {
	cause := errors.New("cannot read the plan")
	format := "magus job fork: %w"
	err := exit.Usagef(format, cause)

	s.ErrorIs(err, cause)
	s.EqualError(err, "magus job fork: cannot read the plan")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *forkErrorsSuite) TestUsageFormattingPreservesTypedCauses() {
	cause := &os.PathError{Op: "read", Path: "jobs.jsonl", Err: fs.ErrPermission}
	format := "magus job fork: %w"
	err := exit.Usagef(format, cause)

	var got *os.PathError
	s.Require().ErrorAs(err, &got)
	s.Same(cause, got)
	s.ErrorIs(err, fs.ErrPermission)
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *forkErrorsSuite) TestForkPreservesTheStackedHeadCancellation() {
	resetOpenedWorkspaces(s.T())
	repo, _ := execRepositoryFixture(s.T())
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()

	err := jobFork(ctx, repo.Root(), []string{"child", "--read-only", "--instructions", "inspect", "--delivery", "stacked"})

	s.ErrorIs(err, context.Canceled)
	s.ErrorContains(err, "magus job fork: child is stacked with no pull request base")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *forkErrorsSuite) TestForkPreservesThePlanWriteFailure() {
	resetOpenedWorkspaces(s.T())
	repo, _ := execRepositoryFixture(s.T())
	store, err := openJobs(s.T().Context(), repo.Root(), jobOpen{})
	s.Require().NoError(err)
	path, err := store.Path()
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	// Readers can inspect the absent plan; its writer cannot open the dangling lock link.
	s.Require().NoError(os.Symlink(filepath.Join(s.T().TempDir(), "missing", "lock"), path+".lock"))

	err = jobFork(s.T().Context(), repo.Root(), []string{"child", "--read-only", "--instructions", "inspect"})

	var cause *os.PathError
	s.Require().ErrorAs(err, &cause)
	s.Equal(path+".lock", cause.Path)
	s.ErrorIs(err, fs.ErrNotExist)
	s.ErrorContains(err, "magus job fork:")
	s.Equal(exit.Usage, exit.Code(err))
	rows, err := store.List(s.T().Context())
	s.Require().NoError(err)
	s.Empty(rows)
}

type RecordCheckoutSuite struct{ suite.Suite }

func TestRecordCheckoutSuite(t *testing.T) { suite.Run(t, new(RecordCheckoutSuite)) }

func (s *RecordCheckoutSuite) TestMakesARelativeRecordCheckoutAbsolute() {
	got, err := recordCheckout("../w", "")
	s.Require().NoError(err)
	want, err := filepath.Abs("../w")
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *RecordCheckoutSuite) TestTakesTheFlagWhenTheRecordNamesNone() {
	got, err := recordCheckout("", "/work/w")
	s.Require().NoError(err)
	s.Equal("/work/w", got)
}

func (s *RecordCheckoutSuite) TestAcceptsARelativeRecordCheckoutThatMatchesTheFlag() {
	abs, err := filepath.Abs("w")
	s.Require().NoError(err)
	got, err := recordCheckout("w", abs)
	s.Require().NoError(err)
	s.Equal(abs, got)
}

func (s *RecordCheckoutSuite) TestRefusesTwoCheckouts() {
	_, err := recordCheckout("/work/a", "/work/b")
	s.ErrorContains(err, "name two checkouts")
}

type jobApplyReleaseWritePathSuite struct {
	suite.Suite
	root  string
	store *job.Store
}

func TestJobApplyReleaseWritePath(t *testing.T) { suite.Run(t, new(jobApplyReleaseWritePathSuite)) }

func (s *jobApplyReleaseWritePathSuite) SetupTest() {
	row := leaseRow("w", "")
	row.State, row.Paths.Write = jobtypes.StateRunning, []string{"a.go", "b.go", "c.go#Run"}
	row.Check = &jobtypes.LeaseCheck{Target: "test", Project: "."}
	row.Validation = job.CheckCommand(*row.Check)
	row.Goals = []jobtypes.Goal{{ID: "g", Kind: jobtypes.GoalKindPaths, Paths: []string{"a.go"}}}
	root, cacheDir := execFixture(s.T(), row)
	s.root = root
	s.store = job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: cacheDir, Root: root})
}

func (s *jobApplyReleaseWritePathSuite) TestNamingRecordsAsWellIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"w", "--release-write-path", "b.go", "--stdin"}))
}

func (s *jobApplyReleaseWritePathSuite) TestWithoutAnIdIsAUsageError() {
	s.Require().Error(jobApply(s.T().Context(), s.root, []string{"--release-write-path", "b.go"}))
}

// ForkProvisionContractSuite pins how a fork judges an adopted checkout of
// magus: by the contract its tree carries, never by the commit.
type ForkProvisionContractSuite struct {
	suite.Suite
	root string
}

func TestForkProvisionContractSuite(t *testing.T) {
	suite.Run(t, new(ForkProvisionContractSuite))
}

func (s *ForkProvisionContractSuite) SetupTest() {
	s.root = s.T().TempDir()
}

func (s *ForkProvisionContractSuite) lock(body string) {
	p := filepath.Join(s.root, "internal", "commands", "testdata", "api.lock")
	s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
	s.Require().NoError(os.WriteFile(p, []byte(body), 0o644))
	for _, dir := range []string{"gen/decls", "gen/types"} {
		s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "internal", "spell", dir), 0o755))
	}
}

func (s *ForkProvisionContractSuite) binary(contract string) string {
	bin := filepath.Join(s.T().TempDir(), "magus")
	body := fmt.Sprintf("#!/bin/sh\necho '{\"version\":\"v0.5.0\",\"embedded\":{\"contract_sha256\":%q}}'\n", contract)
	s.Require().NoError(os.WriteFile(bin, []byte(body), 0o755))
	return bin
}

func (s *ForkProvisionContractSuite) TestATreeThatReadsTheRowPasses() {
	s.lock(fmt.Sprintf("store %s\n", jobtypes.APIVersion))
	s.NoError(checkoutReadsRow(s.root))
}

func (s *ForkProvisionContractSuite) TestATreeOlderThanTheStoreIsRefused() {
	s.lock("config a\n")
	err := checkoutReadsRow(s.root)
	s.Require().Error(err)
	s.Contains(err.Error(), fmt.Sprintf("reads no %s rows", jobtypes.APIVersion))
}

func (s *ForkProvisionContractSuite) TestATreeCarryingNoContractPasses() {
	s.NoError(checkoutReadsRow(s.root))
}

func (s *ForkProvisionContractSuite) TestABinaryOfTheTreesContractReadsIt() {
	s.lock("config a\n")
	tree, err := contract.Tree(s.root)
	s.Require().NoError(err)
	s.True(readsTree(s.T().Context(), s.binary(tree), s.root))
	s.False(readsTree(s.T().Context(), s.binary("another"), s.root))
	s.False(readsTree(s.T().Context(), "", s.root))
}

func (s *ForkProvisionContractSuite) TestAnyBinaryReadsATreeCarryingNoContract() {
	s.True(readsTree(s.T().Context(), s.binary(""), s.root))
	s.False(readsTree(s.T().Context(), "", s.root))
}

func TestPointAtConsoleNamesTheServerStartOnlyWhenAsked(t *testing.T) {
	testkit.Isolate(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	pointAtConsole(t.Context(), "some-job", false)
	assert.Empty(t, buf.String(), "nobody asked for the server")

	pointAtConsole(t.Context(), "some-job", true)
	assert.Contains(t, buf.String(), "the console is not running")
}

// pinCheckoutBinary records bin, hashed as it stands, as the binary of a live
// row's checkout in the job store session reads.
func pinCheckoutBinary(t *testing.T, session, bin string) {
	t.Helper()
	digest, err := job.BinaryDigest(bin)
	require.NoError(t, err)
	_, cacheDir := seedJobStore(t, session, jobtypes.Job{ID: "pinned", State: jobtypes.StateDeclared, Paths: jobtypes.Paths{Write: []string{"a.go"}}})
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: session})
	_, err = store.RecordWorktree(t.Context(), "pinned", jobtypes.Worktree{Root: filepath.Dir(bin), Binary: bin, BinaryDigest: digest})
	require.NoError(t, err)
}

// UnloadedWorkspaceSuite pins what a binary too old for the tree says.
type UnloadedWorkspaceSuite struct {
	suite.Suite
	root string
}

func TestUnloadedWorkspaceSuite(t *testing.T) {
	suite.Run(t, new(UnloadedWorkspaceSuite))
}

func (s *UnloadedWorkspaceSuite) SetupTest() {
	root, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	s.root = root
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus.yaml"), []byte("{}\n"), 0o644))
}

func (s *UnloadedWorkspaceSuite) placeBinary() string {
	own := filepath.Join(s.root, "magus")
	s.Require().NoError(os.WriteFile(own, []byte("#!/bin/sh\n"), 0o755))
	return own
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadNamesTheCheckoutsOwnBinary() {
	own := s.placeBinary()
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, "/elsewhere/magus")
	s.Contains(got, types.WorkspaceNeedsNewerMagus.Error())
	s.Contains(got, fmt.Sprintf("rerun the command with `%s`", own))
	s.NotContains(got, "job apply")
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadByThatVeryBinaryNamesNone() {
	own := s.placeBinary()
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, own)
	s.Equal(types.WorkspaceNeedsNewerMagus.Error(), got)
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadWithANonExecutableFileNamesNone() {
	own := s.placeBinary()
	s.Require().NoError(os.Chmod(own, 0o644))
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, "/elsewhere/magus")
	s.Equal(types.WorkspaceNeedsNewerMagus.Error(), got)
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadWithNoBinaryNamesNone() {
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, "/elsewhere/magus")
	s.Equal(types.WorkspaceNeedsNewerMagus.Error(), got)
}

func (s *UnloadedWorkspaceSuite) TestAnotherFailureNamesNoBinary() {
	s.placeBinary()
	s.Equal("a typo", workspaceLoadFailure(errors.New("a typo"), s.root, "/elsewhere/magus"))
}
