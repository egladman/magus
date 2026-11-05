package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// plantUnknownMember appends id's stored row again carrying a member no build declares,
// as a newer magus would write it; the store's own writers carry the stored bag instead.
func plantUnknownMember(t *testing.T, ctx context.Context, id, member string) {
	t.Helper()
	at := ctx.Value(locationKey{}).(location)
	store := job.NewStore(job.Location{CacheDir: at.cacheDir, StateBase: fixture.StateBase(t), Root: at.workspace})
	rows, err := store.List()
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	require.GreaterOrEqual(t, i, 0, "%s is seeded", id)
	row := rows[i]
	row.Unknown = map[string]json.RawMessage{member: json.RawMessage(`["**"]`)}
	line, err := json.Marshal(row)
	require.NoError(t, err)
	path, err := store.Path()
	require.NoError(t, err)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	require.NoError(t, err)
}

func TestRowUnknownRefusesARowCarryingAMemberThisBuildDoesNotDeclare(t *testing.T) {
	row := narrowLease()
	ctx, root := fleetFixture(t, row)
	plantUnknownMember(t, ctx, row.ID, "widens_paths")
	deps := Dependencies{Binary: "/work/magus", BinaryVersion: "v0.4.3-dev"}

	got := denyUnknownRowMembers(ctx, deps, row.ID, "magus describe job "+row.ID)
	require.True(t, got.refused())
	assert.Contains(t, got.Say, "widens_paths")
	assert.Contains(t, got.Say, "/work/magus")
	assert.Contains(t, got.Say, "v0.4.3-dev")
	assert.Contains(t, got.Why, ward.BootstrapCommand)
	assert.Contains(t, got.Why, "server stop")

	write := gradeLeasedEdit(ctx, deps, row.ID, filepath.Join(root, "cmd/magus/agent.go"), writeFields{})
	require.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleLeaseRowUnknown)}, types.GuardJudgment{Decision: write.Decision, Rule: write.Rule})
	assert.Contains(t, write.Reason, "widens_paths")

	assert.Equal(t, denyRuleLeaseRowUnknown, roleScopedCommandRules()[0].name, "judged before any rule that reads the row")
}

// Refusing these locked a worker out of the only lines that replace the judge.
func TestRowUnknownPassesTheRecoveryLines(t *testing.T) {
	row := narrowLease()
	ctx, _ := fleetFixture(t, row)
	plantUnknownMember(t, ctx, row.ID, "widens_paths")
	deps := Dependencies{Binary: "/work/magus", BinaryVersion: "v0.4.3-dev"}

	for _, line := range []string{
		fmt.Sprintf("/repo/magus buzz hack/dev/bootstrap-worktree.buzz -- --job %s --from /repo", row.ID),
		"git merge --ff-only f1980cc5c",
		"./magus buzz hack/dev/bootstrap-checkout.buzz -- --checkout /work/job",
		"./magus buzz hack/dev/bootstrap-checkout.buzz -- --here",
		fmt.Sprintf("export BAGGAGE=magus.lease=%s; ./magus buzz hack/dev/bootstrap-checkout.buzz -- --here", row.ID),
		fmt.Sprintf("export BAGGAGE=magus.lease=%s && ./magus buzz hack/dev/bootstrap-checkout.buzz -- --here", row.ID),
		fmt.Sprintf("BAGGAGE=magus.lease=%s ./magus buzz hack/dev/bootstrap-checkout.buzz -- --here", row.ID),
		ward.BootstrapCommand,
	} {
		assert.False(t, denyUnknownRowMembers(ctx, deps, row.ID, line).refused(), line)
	}
	for _, line := range []string{
		"./build.sh", "./magus run go-build .", "git commit -m x",
		fmt.Sprintf("export BAGGAGE=magus.lease=%s; ./build.sh", row.ID),
		"export FOO=1; ./magus buzz hack/dev/bootstrap-checkout.buzz -- --here",
		fmt.Sprintf("export BAGGAGE=magus.lease=%s; ./magus buzz hack/dev/bootstrap-checkout.buzz -- --here; ./build.sh", row.ID),
	} {
		assert.True(t, denyUnknownRowMembers(ctx, deps, row.ID, line).refused(), line)
	}
}

// RowUnknownVCSLinesSuite pins that a VCS line replaces no judge: the later
// rules would grade it on only the members they know.
type RowUnknownVCSLinesSuite struct {
	suite.Suite
	layers ctxLayers
	row    types.Job
}

func TestRowUnknownVCSLinesSuite(t *testing.T) {
	suite.Run(t, new(RowUnknownVCSLinesSuite))
}

func (s *RowUnknownVCSLinesSuite) SetupTest() {
	s.row = narrowLease()
	s.layers, _ = fleetLayers(s.T(), s.row)
	plantUnknownMember(s.T(), s.ctx(), s.row.ID, "narrows_paths")
}

func (s *RowUnknownVCSLinesSuite) refused(line string) bool {
	return denyUnknownRowMembers(s.ctx(), Dependencies{}, s.row.ID, line).refused()
}

func (s *RowUnknownVCSLinesSuite) TestAPickIsRefused() {
	s.True(s.refused("git cherry-pick f1980cc5c"))
}

func (s *RowUnknownVCSLinesSuite) TestAddingAWorktreeIsRefused() {
	s.True(s.refused("git worktree add ../elsewhere"))
}

func (s *RowUnknownVCSLinesSuite) TestAbortingAMergeIsRefused() {
	s.True(s.refused("git merge --abort"))
}

func (s *RowUnknownVCSLinesSuite) TestContinuingARebaseIsRefused() {
	s.True(s.refused("git rebase --continue"))
}

func (s *RowUnknownVCSLinesSuite) TestAFastForwardStillPasses() {
	s.False(s.refused("git merge --ff-only f1980cc5c"))
}

func TestRowUnknownPassesARowItCanRead(t *testing.T) {
	row := narrowLease()
	ctx, root := fleetFixture(t, row)

	assert.False(t, denyUnknownRowMembers(ctx, Dependencies{}, row.ID, "magus describe job "+row.ID).refused())
	assert.False(t, denyUnknownRowMembers(ctx, Dependencies{}, "", "magus describe job "+row.ID).refused(), "no lease, no row")
	assert.Empty(t, gradeLeasedEdit(ctx, Dependencies{}, row.ID, filepath.Join(root, "cmd/magus/agent.go"), writeFields{}).Decision)

	outside := gradeLeasedEdit(ctx, Dependencies{}, row.ID, filepath.Join(root, "README.md"), writeFields{})
	require.Equal(t, decisionDeny, outside.Decision, "the other rules still judge the row")
	assert.NotEqual(t, string(denyRuleLeaseRowUnknown), outside.Rule)
}

// RowUnknownReadOnlyCallerSuite pins the denial for a row with no checkout.
type RowUnknownReadOnlyCallerSuite struct {
	suite.Suite
	layers ctxLayers
	row    types.Job
	deps   Dependencies
}

func TestRowUnknownReadOnlyCallerSuite(t *testing.T) {
	suite.Run(t, new(RowUnknownReadOnlyCallerSuite))
}

func (s *RowUnknownReadOnlyCallerSuite) SetupTest() {
	s.row = types.Job{ID: "scout", ReadOnly: true, State: types.StateRunning, Registered: 1}
	s.layers, _ = fleetLayers(s.T(), s.row)
	plantUnknownMember(s.T(), s.ctx(), s.row.ID, "widens_paths")
	s.deps = Dependencies{Binary: "/work/magus", BinaryVersion: "v0.4.3-dev"}
}

func (s *RowUnknownReadOnlyCallerSuite) TestItTellsTheCallerToStopAndReportTheRisk() {
	got := denyUnknownRowMembers(s.ctx(), s.deps, s.row.ID, "magus describe job scout")
	s.Require().True(got.refused())
	s.Contains(got.Say, "widens_paths")
	s.Contains(got.Say, "stop")
	s.Contains(got.Say, "unresolved risk")
	s.Contains(got.Why, "only the root can rebuild")
}

func (s *RowUnknownReadOnlyCallerSuite) TestItNamesNoCheckoutPlaceholder() {
	got := denyUnknownRowMembers(s.ctx(), s.deps, s.row.ID, "magus describe job scout")
	s.NotContains(got.Say, "<checkout>")
	s.NotContains(got.Why, "<checkout>")
	s.NotContains(got.Say, "build this checkout")
}

func (s *RowUnknownReadOnlyCallerSuite) TestARowWithACheckoutStillNamesItsBootstrap() {
	row := s.row
	row.CheckoutRoot = "/work/job"
	got := unknownRowDenial(s.deps, withUnknown(row, "widens_paths"))
	s.Require().True(got.refused())
	s.Contains(got.Say, "/work/job")
}

// The spawn gate refuses a row the hook cannot read before a worker meets it.
func TestSpawnBoundToAnUnreadableRowIsDeniedWithTheRootsRemedy(t *testing.T) {
	row := withUnknown(types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared}, "widens_paths")
	titled := func() (types.Job, bool) { return row, true }
	deps := Dependencies{Binary: "/work/magus", BinaryVersion: "v0.4.3-dev"}
	at := location{cacheDir: t.TempDir()}

	got := gradeSpawnModel(deps, types.GuardJudgment{Decision: decisionPass}, hookRequest{IsSpawn: true, DeclaredModel: "haiku"},
		hookAttribution{Host: "claude-code", Session: "s"}, at, "", titled)

	assert.Contains(t, got.Reason, "widens_paths")
	assert.Contains(t, got.Reason, ward.BootstrapCommand)
	assert.Contains(t, got.Reason, "restart the daemon")
	assert.Contains(t, unknownRowSpawnDenial(deps, row).Why, "`./magus server stop`, then `./magus server start`")
	got.Reason = ""
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleLeaseRowUnknown)}, got)
}

func TestSpawnBoundToAReadableRowIsNotDeniedForSkew(t *testing.T) {
	row := types.Job{ID: "scout", ReadOnly: true, State: types.StateDeclared}
	titled := func() (types.Job, bool) { return row, true }
	at := location{cacheDir: t.TempDir()}

	got := gradeSpawnModel(Dependencies{}, types.GuardJudgment{Decision: decisionPass}, hookRequest{IsSpawn: true, DeclaredModel: "haiku"},
		hookAttribution{Host: "claude-code", Session: "s"}, at, "", titled)

	assert.NotEqual(t, decisionDeny, got.Decision)
}

func withUnknown(row types.Job, member string) types.Job {
	row.Unknown = map[string]json.RawMessage{member: json.RawMessage(`true`)}
	return row
}

func (s *RowUnknownVCSLinesSuite) ctx() context.Context { return s.layers.of(s.T()) }

func (s *RowUnknownReadOnlyCallerSuite) ctx() context.Context { return s.layers.of(s.T()) }
