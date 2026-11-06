//go:build linux || darwin

package spawn

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	forkDepthEnv = "MAGUS_TEST_FORK_DEPTH"
	forkDirEnv   = "MAGUS_TEST_FORK_DIR"
	forkHoldEnv  = "MAGUS_TEST_FORK_HOLD"
)

// maybeForkTree makes the test binary one node of a fork tree: it records
// its pid, starts two setsid children a level shallower, and holds. Depth
// caps the tree, and each node exits within 20s.
func maybeForkTree() {
	depth, err := strconv.Atoi(os.Getenv(forkDepthEnv))
	if err != nil {
		return
	}
	dir := os.Getenv(forkDirEnv)
	_ = os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600)
	if depth > 0 {
		for range 2 {
			c := exec.Command(os.Args[0])
			c.Env = append(os.Environ(), fmt.Sprintf("%s=%d", forkDepthEnv, depth-1), forkHoldEnv+"=1")
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			_ = c.Start()
		}
	}
	if os.Getenv(forkHoldEnv) == "" {
		// Outlive a few ticks: a child the bound never saw while its parent
		// lived is reparented out of reach.
		time.Sleep(10 * boundEvery)
		os.Exit(0)
	}
	testPID, _ := strconv.Atoi(os.Getenv("TEST_PID"))
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if testPID == 0 || syscall.Kill(testPID, 0) != nil {
			break
		}
	}
	os.Exit(0)
}

// TreeBoundSuite runs fork trees under Exec and asserts none survive it.
type TreeBoundSuite struct {
	suite.Suite
	dir   string
	limit int
}

func TestTreeBoundSuite(t *testing.T) {
	suite.Run(t, new(TreeBoundSuite))
}

func (s *TreeBoundSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.limit = maxTreeProcs
	maxTreeProcs = 8
}

func (s *TreeBoundSuite) TearDownTest() {
	maxTreeProcs = s.limit
}

// run execs a fork tree of depth; hold keeps the root running as well.
func (s *TreeBoundSuite) run(depth int, hold bool) error {
	env := append(os.Environ(), testPidEnv(), fmt.Sprintf("%s=%d", forkDepthEnv, depth), fmt.Sprintf("%s=%s", forkDirEnv, s.dir))
	if hold {
		env = append(env, forkHoldEnv+"=1")
	}
	_, err := Exec(s.T().Context(), os.Args[0], nil, ExecOptions{Env: env, Capture: true, Quiet: true})
	return err
}

// recorded is every pid the tree wrote.
func (s *TreeBoundSuite) recorded() []int {
	entries, err := os.ReadDir(s.dir)
	s.Require().NoError(err)
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		s.Require().NoError(err)
		pids = append(pids, pid)
	}
	return pids
}

func (s *TreeBoundSuite) assertNoneAlive() {
	for _, pid := range s.recorded() {
		gone := func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
		if !s.Eventually(gone, 2*time.Second, 10*time.Millisecond, "pid %d outlived Exec", pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func (s *TreeBoundSuite) TestATreePastTheBoundFailsNamingTheBinary() {
	err := s.run(4, true)
	s.Require().ErrorIs(err, ErrTooManyProcesses)
	name := filepath.Base(os.Args[0])
	s.Contains(err.Error(), "most of them "+name[:min(15, len(name))])
	s.assertNoneAlive()
}

func (s *TreeBoundSuite) TestSetsidChildrenThatOutliveTheRootAreReaped() {
	s.Require().NoError(s.run(1, false))
	s.Len(s.recorded(), 3, "the root and its two setsid children")
	s.assertNoneAlive()
}
