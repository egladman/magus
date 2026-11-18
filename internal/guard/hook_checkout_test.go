package guard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
)

func TestCallDirIsWhereTheCallLands(t *testing.T) {
	assert.Equal(t, "/w/b/internal", hookCallDir(`{"cwd":"/w/a","tool_input":{"file_path":"/w/b/internal/x.go"}}`), "a write's file")
	assert.Equal(t, "/w/a/sub", hookCallDir(`{"cwd":"/w/a","tool_input":{"file_path":"sub/x.go"}}`), "relative to the session")
	assert.Equal(t, "/w/b", hookCallDir(`{"cwd":"/w/a","tool_input":{"command":"ls","workdir":"/w/b"}}`), "a shell tool's workdir")
	assert.Equal(t, "/w/a", hookCallDir(`{"cwd":"/w/a","tool_input":{"command":"ls"}}`), "the session")
	assert.Empty(t, hookCallDir("git status"), "a bare command names no place")
}

// placeStub writes an executable ./magus into root that reports stamp as its commit and
// answers any other call with say and code, printing its arguments to stderr one per line.
func placeStub(t *testing.T, root, stamp, say string, code int) string {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then printf '%%s\\n' '%s'; exit 0; fi\nprintf '%%s\\n' \"$@\" >&2\ncat >/dev/null\nprintf '%%s' '%s'\nexit %d\n", stamp, say, code)
	own := filepath.Join(root, "magus")
	require.NoError(t, os.WriteFile(own, []byte(script), 0o755))
	return own
}

func TestCheckoutBinaryIsAnotherBinaryOnly(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	own := placeStub(t, root, "", "", 0)
	assert.Equal(t, own, CheckoutBinary(filepath.Join(root, "new", "dir"), "/usr/local/bin/magus"), "a directory not made yet")
	assert.Empty(t, CheckoutBinary(root, own), "the running binary judges its own checkout")
	assert.Empty(t, CheckoutBinary(t.TempDir(), "/usr/local/bin/magus"), "no workspace")
}

// DelegateToCheckoutSuite hands hook calls from a session in a linked
// worktree to a job checkout's own ./magus, a stub that denies with exit 2.
type DelegateToCheckoutSuite struct {
	suite.Suite
	repo     *testkit.Repo
	session  string
	cacheDir string
	env      HookEnv
	// argv is the command line the session's hook was called with.
	argv []string
}

func TestDelegateToCheckoutSuite(t *testing.T) {
	suite.Run(t, new(DelegateToCheckoutSuite))
}

func (s *DelegateToCheckoutSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"})
	s.session = s.repo.AddWorktree("session").Root()
	testkit.Isolate(s.T())
	s.cacheDir = s.T().TempDir()
	s.env = testHookEnv()
	s.env.OpenJobs = func(_ context.Context, root string) (*job.Store, error) {
		return job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: s.cacheDir, Root: root, DryRun: true}), nil
	}
	s.runAs(filepath.Join(s.session, "magus"))
	s.argv = []string{"shell", "--transport", "hooks"}
}

// runAs makes bin the running binary for the rest of the test.
func (s *DelegateToCheckoutSuite) runAs(bin string) {
	s.env.HookBinaryPath = func() (string, error) { return bin, nil }
}

// line is a Bash call from the session that runs magus in the checkout.
func (s *DelegateToCheckoutSuite) line() string {
	return s.lineInto(s.repo.Root())
}

// lineInto is a Bash call from the session that runs magus in dir.
func (s *DelegateToCheckoutSuite) lineInto(dir string) string {
	return fmt.Sprintf(`{"cwd":%q,"tool_name":"Bash","tool_input":{"command":"cd %s && ./magus run lint ."}}`, s.session, dir)
}

// write is a write from the session into a file under dir.
func (s *DelegateToCheckoutSuite) write(dir string) string {
	return fmt.Sprintf(`{"cwd":%q,"tool_input":{"file_path":%q}}`, s.session, filepath.Join(dir, "x.go"))
}

// pin records own, hashed as it stands, as the binary of a live row's checkout in the job
// store the session reads.
func (s *DelegateToCheckoutSuite) pin(own string) string {
	t := s.T()
	digest, err := job.BinaryDigest(own)
	s.Require().NoError(err)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: s.cacheDir, Root: s.session})
	_, err = store.Update(t.Context(), "pinned", func(cur *jobtypes.Job) {
		*cur = jobtypes.Job{ID: "pinned", State: jobtypes.StateDeclared, Paths: jobtypes.Paths{Write: []string{"a.go"}}}
	})
	s.Require().NoError(err)
	_, err = store.RecordWorktree(t.Context(), "pinned", jobtypes.Worktree{Root: filepath.Dir(own), Binary: own, BinaryDigest: digest})
	s.Require().NoError(err)
	return own
}

// placePinned is placeStub into the checkout, pinned.
func (s *DelegateToCheckoutSuite) placePinned(stamp, say string, code int) string {
	return s.pin(placeStub(s.T(), s.repo.Root(), stamp, say, code))
}

func (s *DelegateToCheckoutSuite) delegate(input string) (handled bool, declined, out string, err error) {
	return s.delegateWithin(context.Background(), input)
}

func (s *DelegateToCheckoutSuite) delegateWithin(ctx context.Context, input string) (handled bool, declined, out string, err error) {
	handled, declined, out, _, err = s.delegateCapturing(ctx, input)
	return handled, declined, out, err
}

// delegateCapturing is delegateWithin that also returns what the delegate wrote to stderr.
func (s *DelegateToCheckoutSuite) delegateCapturing(ctx context.Context, input string) (handled bool, declined, out, errOut string, err error) {
	var stdout, stderr bytes.Buffer
	handled, decline, err := s.env.delegateToCheckout(ctx, s.argv, input, output.Options{Format: output.Text}, &stdout, &stderr)
	return handled, decline.note, stdout.String(), stderr.String(), err
}

// delegateToSleeper places a stamped ./magus that prints say and sleeps, and
// hands it the line, cancelling once it has started.
func (s *DelegateToCheckoutSuite) delegateToSleeper(say string) (own string, handled bool, out string, err error) {
	started := filepath.Join(s.T().TempDir(), "started")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then printf '%%s\\n' '%s'; exit 0; fi\nprintf '%%s' '%s'\ntouch '%s'\nexec sleep 30\n", s.repo.Head(), say, started)
	own = filepath.Join(s.repo.Root(), "magus")
	s.Require().NoError(os.WriteFile(own, []byte(script), 0o755))
	s.pin(own)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(started); err == nil {
				cancel()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	handled, declined, out, err := s.delegateWithin(ctx, s.line())
	s.Empty(declined)
	return own, handled, out, err
}

func (s *DelegateToCheckoutSuite) TestADelegateKilledPastTheBudgetIsAnsweredWithADenyNamingIt() {
	own, handled, out, err := s.delegateToSleeper("")
	s.True(handled)
	s.Equal(2, exit.Code(err))
	s.Contains(out, own)
	s.Contains(out, "was killed unanswered at the hook's deadline")
}

func (s *DelegateToCheckoutSuite) TestADelegateKilledAfterAnsweringGetsNoSecondVerdict() {
	_, handled, out, err := s.delegateToSleeper(`{"decision":"defer"}`)
	s.True(handled)
	s.Equal(2, exit.Code(err))
	s.Equal(`{"decision":"defer"}`, out)
}

func (s *DelegateToCheckoutSuite) TestADelegateIsHandedTheParentsDeadline() {
	own := filepath.Join(s.repo.Root(), "magus")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then printf '%%s\\n' '%s'; exit 0; fi\ncat >/dev/null\nprintf '%%s' \"$%s\"\n", s.repo.Head(), HookDeadlineEnv)
	s.Require().NoError(os.WriteFile(own, []byte(script), 0o755))
	s.pin(own)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	want, _ := ctx.Deadline()
	handled, _, out, err := s.delegateWithin(ctx, s.line())
	s.True(handled)
	s.Require().NoError(err)
	s.Equal(strconv.FormatInt(want.UnixMilli(), 10), out)
}

func (s *DelegateToCheckoutSuite) TestADelegateIsHandedTheHooksCommandLine() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	handled, declined, _, errOut, _ := s.delegateCapturing(context.Background(), s.line())
	s.True(handled)
	s.Empty(declined)
	s.Equal(strings.Join(s.argv, "\n")+"\n", errOut)
}

func (s *DelegateToCheckoutSuite) TestACallWithNoCommandLineIsJudgedHereAndSaysWhy() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	s.argv = nil
	handled, declined, out, err := s.delegate(s.line())
	s.Require().NoError(err)
	s.False(handled)
	s.Empty(out)
	s.Contains(declined, "the hook's command line was not handed to this binary")
}

func (s *DelegateToCheckoutSuite) TestABuildAtHeadJudgesTheWrite() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	handled, declined, out, err := s.delegate(s.write(s.repo.Root()))
	s.True(handled)
	s.Empty(declined)
	s.Equal(2, exit.Code(err))
	s.Equal(`{"decision":"deny"}`, out)
}

func (s *DelegateToCheckoutSuite) TestABuildAtHeadJudgesALineThatCdsIntoTheCheckout() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	handled, declined, out, err := s.delegate(s.line())
	s.True(handled)
	s.Empty(declined)
	s.Equal(2, exit.Code(err))
	s.Equal(`{"decision":"deny"}`, out)
}

func (s *DelegateToCheckoutSuite) TestABuildAfterHeadJudges() {
	base := s.repo.Head()
	ahead := s.repo.Commit("ahead", nil)
	s.repo.Git("checkout", "-q", base)
	s.placePinned(ahead, `{"decision":"deny"}`, 2)
	handled, declined, _, _ := s.delegate(s.line())
	s.True(handled)
	s.Empty(declined)
}

func (s *DelegateToCheckoutSuite) TestABuildBehindHeadJudges() {
	behind := s.repo.Head()
	s.repo.Commit("moved on", nil)
	s.placePinned(behind, `{"decision":"deny"}`, 2)
	handled, declined, out, err := s.delegate(s.line())
	s.True(handled)
	s.Empty(declined)
	s.Equal(2, exit.Code(err))
	s.Equal(`{"decision":"deny"}`, out)
}

func (s *DelegateToCheckoutSuite) TestAnUnstampedBuildJudges() {
	s.placePinned(buildinfo.UnknownVersion, `{"decision":"deny"}`, 2)
	handled, declined, _, _ := s.delegate(s.line())
	s.True(handled)
	s.Empty(declined)
}

func (s *DelegateToCheckoutSuite) TestACallAlreadyHandedOnIsJudgedHere() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	s.T().Setenv(delegatedEnv, "/other/magus")
	ctx, cancel, err := takeDelegation(context.Background())
	defer cancel()
	s.Require().NoError(err)
	handled, declined, _, err := s.delegateWithin(ctx, s.line())
	s.False(handled)
	s.Empty(declined)
	s.NoError(err)
}

func (s *DelegateToCheckoutSuite) TestTakingTheDelegationLeavesNothingForAChildToInherit() {
	s.T().Setenv(delegatedEnv, "/other/magus")
	s.T().Setenv(HookDeadlineEnv, strconv.FormatInt(time.Now().Add(time.Second).UnixMilli(), 10))
	ctx, cancel, err := takeDelegation(context.Background())
	defer cancel()
	s.Require().NoError(err)
	s.Equal("/other/magus", delegatedBy(ctx))
	_, bounded := ctx.Deadline()
	s.True(bounded)
	for _, name := range []string{delegatedEnv, HookDeadlineEnv} {
		_, set := os.LookupEnv(name)
		s.False(set, name)
	}
}

func (s *DelegateToCheckoutSuite) TestADeadlineWithoutADelegationIsDroppedUnread() {
	s.T().Setenv(HookDeadlineEnv, "soon")
	ctx, cancel, err := takeDelegation(context.Background())
	defer cancel()
	s.Require().NoError(err)
	s.Empty(delegatedBy(ctx))
	_, set := os.LookupEnv(HookDeadlineEnv)
	s.False(set)
}

func (s *DelegateToCheckoutSuite) TestADelegateHandedAPastDeadlineDeniesAndSaysToRetry() {
	s.T().Setenv(recorder.EnvBaggage, "")
	s.T().Setenv(delegatedEnv, "/other/magus")
	s.T().Setenv(HookDeadlineEnv, strconv.FormatInt(time.Now().Add(-time.Second).UnixMilli(), 10))
	var out bytes.Buffer
	err := hookStdin(context.Background(), strings.NewReader("git status"), &out, nil)
	s.Equal(2, exit.Code(err))
	s.Contains(out.String(), "had no budget left")
	s.Contains(out.String(), "Retry it")
	s.NotContains(out.String(), "Fix the environment")
}

func (s *DelegateToCheckoutSuite) TestALineThatStaysInTheSessionIsJudgedHere() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	input := fmt.Sprintf(`{"cwd":%q,"tool_name":"Bash","tool_input":{"command":"./magus run lint ."}}`, s.T().TempDir())
	handled, declined, _, _ := s.delegate(input)
	s.False(handled)
	s.Empty(declined)
}

func (s *DelegateToCheckoutSuite) TestASessionDirectoryMovedIntoAnotherRepositoryIsJudgedHere() {
	clone := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"})
	placeStub(s.T(), clone.Root(), clone.Head(), `{"decision":"defer"}`, 0)
	s.session = clone.Root()
	handled, declined, out, err := s.delegate(s.write(clone.Root()))
	s.Require().NoError(err)
	s.False(handled)
	s.Empty(out)
	s.Contains(declined, "no live job row records it")
}

func (s *DelegateToCheckoutSuite) TestAReadIntoAnotherRepositoryIsJudgedHere() {
	clone := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"})
	placeStub(s.T(), clone.Root(), clone.Head(), `{"decision":"defer"}`, 0)
	handled, declined, out, err := s.delegate(s.write(clone.Root()))
	s.Require().NoError(err)
	s.False(handled)
	s.Empty(out)
	s.Contains(declined, "no live job row records it")
}

func (s *DelegateToCheckoutSuite) TestALineThatCdsIntoAPlantedWorkspaceIsJudgedHere() {
	planted := filepath.Join(s.session, "planted")
	s.Require().NoError(os.MkdirAll(planted, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(planted, "magus.yaml"), []byte("{}\n"), 0o644))
	placeStub(s.T(), planted, s.repo.Head(), `{"decision":"defer"}`, 0)
	handled, declined, out, err := s.delegate(s.lineInto(planted))
	s.Require().NoError(err)
	s.False(handled)
	s.Empty(out)
	s.Contains(declined, "no live job row records it")
}

func (s *DelegateToCheckoutSuite) TestABinaryInNoCheckoutHandsNothingOn() {
	placeStub(s.T(), s.repo.Root(), s.repo.Head(), `{"decision":"defer"}`, 0)
	s.runAs(filepath.Join(s.T().TempDir(), "magus"))
	handled, declined, out, err := s.delegate(s.line())
	s.Require().NoError(err)
	s.False(handled)
	s.Empty(out)
	s.Contains(declined, "is no checkout's own ./magus")
}

func (s *DelegateToCheckoutSuite) TestACheckoutBinaryNoRowPinsIsJudgedHere() {
	placeStub(s.T(), s.repo.Root(), s.repo.Head(), `{"decision":"defer"}`, 0)
	handled, declined, out, err := s.delegate(s.line())
	s.Require().NoError(err)
	s.False(handled)
	s.Empty(out)
	s.Contains(declined, "no live job row records it as its checkout's binary")
}

func (s *DelegateToCheckoutSuite) TestABinaryRewrittenSinceItsPinIsJudgedHere() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	// A worker's relink lands a different binary at the pinned path.
	placeStub(s.T(), s.repo.Root(), s.repo.Head(), `{"decision":"defer"}`, 0)
	handled, declined, out, err := s.delegate(s.line())
	s.Require().NoError(err)
	s.False(handled)
	s.Empty(out)
	s.Contains(declined, "its bytes do not hash to what job pinned pins")
}

func (s *DelegateToCheckoutSuite) TestAPinOnAnEndedRowHandsNothingOn() {
	s.placePinned(s.repo.Head(), `{"decision":"deny"}`, 2)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: s.cacheDir, Root: s.session})
	_, err := store.Update(s.T().Context(), "pinned", func(cur *jobtypes.Job) { cur.State = jobtypes.StatePass })
	s.Require().NoError(err)
	handled, declined, _, err := s.delegate(s.line())
	s.Require().NoError(err)
	s.False(handled)
	s.Contains(declined, "no live job row records it")
}

func (s *DelegateToCheckoutSuite) TestCallDirIsTheCheckoutALineCdsInto() {
	s.Equal(s.repo.Root(), hookCallDir(s.line()))
}

// JudgedBySuite pins what a verdict says about the binary that judged it.
type JudgedBySuite struct {
	suite.Suite
}

func TestJudgedBySuite(t *testing.T) {
	suite.Run(t, new(JudgedBySuite))
}

func (s *JudgedBySuite) TestADenyNamesItsJudge() {
	got := judgedBy(verdict.Judgment{Decision: verdict.Deny, Reason: "no"}, "Judged by /c/magus.")
	s.Equal("no\nJudged by /c/magus.", got.Reason)
}

func (s *JudgedBySuite) TestAnAdviseNamesItsJudge() {
	got := judgedBy(verdict.Judgment{Decision: verdict.Advise, Context: "see"}, "Judged by /c/magus.")
	s.Equal("see\nJudged by /c/magus.", got.Context)
}

func (s *JudgedBySuite) TestAPassCarriesNoText() {
	got := judgedBy(verdict.Judgment{Decision: verdict.Defer}, "Judged by /c/magus.")
	s.Equal(verdict.Judgment{Decision: verdict.Defer}, got)
}
