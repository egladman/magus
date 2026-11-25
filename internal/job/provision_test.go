package job

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

const everyDiskIsLow = int64(1) << 62

// lowDisk reports every disk below the floor, as a real probe does when the path reads.
var lowDisk = Disk{
	Below: func(dir string, floor int64) (int64, bool, error) {
		if _, err := os.Stat(dir); err != nil {
			return 0, false, err
		}
		return 1 << 20, floor > 1<<20, nil
	},
	ReclaimSnippet: "",
}

type ForkDiskFloorSuite struct {
	suite.Suite
	root string
}

func TestForkDiskFloorSuite(t *testing.T) { suite.Run(t, new(ForkDiskFloorSuite)) }

func (s *ForkDiskFloorSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *ForkDiskFloorSuite) TestANewCheckoutOnALowDiskIsRefusedNamingTheCode() {
	err := RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", s.root, filepath.Join(s.root, "new"), everyDiskIsLow)
	s.ErrorIs(err, types.DiskFull)
	s.ErrorContains(err, types.ReclaimDiskCommand)
}

func (s *ForkDiskFloorSuite) TestAnAdoptedCheckoutIsExempt() {
	s.NoError(RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", s.root, s.root, everyDiskIsLow))
}

func (s *ForkDiskFloorSuite) TestAnUnreadableDiskIsRefusedSayingSo() {
	absent := filepath.Join(s.root, "absent")
	err := RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", absent, filepath.Join(s.root, "new"), 1)
	s.ErrorContains(err, "cannot read the disk holding "+absent)
	s.NotErrorIs(err, types.DiskFull)
}

func (s *ForkDiskFloorSuite) TestADiskAboveTheFloorPasses() {
	s.NoError(RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", s.root, filepath.Join(s.root, "new"), 1))
}

type progressWriterSuite struct {
	suite.Suite
	said     bytes.Buffer
	previous *slog.Logger
}

func TestProgressWriterSuite(t *testing.T) { suite.Run(t, new(progressWriterSuite)) }

func (s *progressWriterSuite) SetupTest() {
	s.previous = slog.Default()
	s.said.Reset()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, nil)))
}

func (s *progressWriterSuite) TearDownTest() { slog.SetDefault(s.previous) }

func (s *progressWriterSuite) TestProgressGoesToStderrUnlessQuiet() {
	s.Equal(os.Stderr, progressWriter(s.T().Context()))
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, &slog.HandlerOptions{Level: slog.LevelWarn})))
	s.Equal(io.Discard, progressWriter(s.T().Context()))
}

// FailedProvisionSuite pins what a fork says, and leaves, when readying its checkout fails.
type FailedProvisionSuite struct {
	suite.Suite
	repo  *testkit.Repo
	store *Store
}

func TestFailedProvisionSuite(t *testing.T) { suite.Run(t, new(FailedProvisionSuite)) }

func (s *FailedProvisionSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"magus.yaml": ""})
	s.store = NewStore(Location{StateBase: s.T().TempDir(), CacheDir: s.T().TempDir(), Root: s.repo.Root()})
}

// failed bootstraps wt, which fails: the forker declares no hook.
func (s *FailedProvisionSuite) failed(wt job.Worktree, made bool) error {
	return FailedProvision(s.T().Context(), s.store, wt, made, bootstrapCheckout(s.T().Context(), lowDisk, "false", s.repo.Root(), wt.Root))
}

func (s *FailedProvisionSuite) TestAnAdoptedCheckoutStaysAndTheErrorSaysSo() {
	dir := s.repo.AddWorktree("adopted").Root()

	err := s.failed(job.Worktree{Root: dir, Source: s.repo.Root(), Base: s.repo.Head()}, false)

	s.Require().ErrorContains(err, NoCheckoutHook)
	s.NotContains(err.Error(), "removed")
	s.ErrorContains(err, fmt.Sprintf("%s stays as it was, since this fork did not make it", dir))
	s.DirExists(dir)
}

func (s *FailedProvisionSuite) TestACheckoutThisForkMadeIsRemovedAndTheErrorSaysSo() {
	wt, made, err := s.store.ForkWorktree(s.T().Context(), "w", filepath.Join(s.T().TempDir(), "w"), "", "")
	s.Require().NoError(err)
	s.Require().True(made)

	err = s.failed(wt, made)

	s.Require().ErrorContains(err, NoCheckoutHook)
	s.ErrorContains(err, fmt.Sprintf("this fork made %s, so it removed it", wt.Root))
	s.NoDirExists(wt.Root)
}

// ReadyCheckoutSuite pins how a checkout's own checkout.ready steps run.
type ReadyCheckoutSuite struct {
	suite.Suite
	said     bytes.Buffer
	previous *slog.Logger
}

func TestReadyCheckoutSuite(t *testing.T) { suite.Run(t, new(ReadyCheckoutSuite)) }

func (s *ReadyCheckoutSuite) SetupTest() {
	s.previous = slog.Default()
	s.said.Reset()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, nil)))
}

func (s *ReadyCheckoutSuite) TearDownTest() { slog.SetDefault(s.previous) }

const twoReadySteps = "checkout:\n  ready:\n    - target: build\n      project: libs/a\n    - target: graph\n      project: .\n"

// checkout is a repository holding yaml as its magus.yaml. Its ./magus logs each call
// to calls and prints a target result. It fails the target named failing.
func (s *ReadyCheckoutSuite) checkout(yaml, failing string) (root, calls string) {
	root = testkit.NewRepository(s.T(), map[string]string{"magus.yaml": yaml}).Root()
	calls = filepath.Join(s.T().TempDir(), "calls")
	script := fmt.Sprintf(`#!/bin/sh
echo "$PWD $*" >> %q
if [ "$2" = %q ]; then
  echo '{"type":"run.target.result","target":"dep","status":"failed","ref":"ref-dep"}'
  echo '{"type":"run.target.result","target":"'"$2"'","status":"failed","ref":"ref-'"$2"'"}'
  exit 1
fi
echo 'not json'
echo '{"type":"run.target.result","target":"'"$2"'","status":"ok","ref":"ref-'"$2"'"}'
`, calls, failing)
	s.Require().NoError(os.WriteFile(filepath.Join(root, binaryName), []byte(script), 0o755))
	return root, calls
}

func (s *ReadyCheckoutSuite) calls(path string) string {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	s.Require().NoError(err)
	return string(body)
}

func (s *ReadyCheckoutSuite) TestEachStepRunsInOrderInTheCheckoutAndKeepsItsRef() {
	root, calls := s.checkout(twoReadySteps, "")

	ran, err := runReadySteps(s.T().Context(), root)

	s.Require().NoError(err)
	s.Equal([]readyRun{
		{Step: config.ReadyStep{Target: "build", Project: "libs/a"}, Ref: "ref-build"},
		{Step: config.ReadyStep{Target: "graph", Project: "."}, Ref: "ref-graph"},
	}, ran)
	real, err := filepath.EvalSymlinks(root)
	s.Require().NoError(err)
	s.Equal(fmt.Sprintf("%[1]s run build libs/a -o jsonl\n%[1]s run graph . -o jsonl\n", real), s.calls(calls))
}

func (s *ReadyCheckoutSuite) TestAFailingStepStopsTheRestAndNamesItsRefAndRerun() {
	root, calls := s.checkout(twoReadySteps, "build")

	ran, err := runReadySteps(s.T().Context(), root)

	var failed *readyStepError
	s.Require().ErrorAs(err, &failed)
	s.Equal(readyStepError{Root: root, Step: config.ReadyStep{Target: "build", Project: "libs/a"}, Ref: "ref-dep", Err: failed.Err}, *failed)
	s.ErrorContains(err, "`./magus query output ref-dep`")
	s.ErrorContains(err, "`./magus run build libs/a` reruns it there")
	s.ErrorContains(err, "no later step ran")
	s.Empty(ran)
	s.NotContains(s.calls(calls), "graph")
}

func (s *ReadyCheckoutSuite) TestNoDeclarationIsSaidOnceAndRunsNothing() {
	root, calls := s.checkout("", "")

	ran, err := runReadySteps(s.T().Context(), root)

	s.Require().NoError(err)
	s.Empty(ran)
	s.Empty(s.calls(calls))
	s.Equal(1, strings.Count(s.said.String(), "declares no checkout.ready"))
}

func (s *ReadyCheckoutSuite) TestACheckoutWithNoMagusIsRefused() {
	root, _ := s.checkout(twoReadySteps, "")
	s.Require().NoError(os.Remove(filepath.Join(root, binaryName)))

	ran, err := runReadySteps(s.T().Context(), root)

	s.Require().ErrorContains(err, "declares 2 checkout.ready step(s) and holds no ./magus to run them, so none ran")
	s.Empty(ran)
}
