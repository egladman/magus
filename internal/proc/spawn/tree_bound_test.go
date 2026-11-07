//go:build linux || darwin

package spawn

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	forkDepthEnv = "MAGUS_TEST_FORK_DEPTH"
	forkDirEnv   = "MAGUS_TEST_FORK_DIR"
	forkHoldEnv  = "MAGUS_TEST_FORK_HOLD"
	// forkShapeEnv picks a leftover's shape: "trap" records a SIGTERM, and
	// "unmarked" drops the tree mark before it starts.
	forkShapeEnv = "MAGUS_TEST_FORK_SHAPE"
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
	if os.Getenv(forkShapeEnv) == "trap" && os.Getenv(forkHoldEnv) != "" {
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		go func() {
			<-term
			_ = os.WriteFile(filepath.Join(dir, "term"), nil, 0o600)
			os.Exit(0)
		}()
	}
	if depth > 0 {
		for range 2 {
			c := exec.Command(os.Args[0])
			env := os.Environ()
			if os.Getenv(forkShapeEnv) == "unmarked" {
				env = slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(kv, TreeMarkEnv+"=") })
			}
			c.Env = append(env, fmt.Sprintf("%s=%d", forkDepthEnv, depth-1), forkHoldEnv+"=1")
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			_ = c.Start()
		}
	}
	if os.Getenv(forkHoldEnv) == "" {
		// Outlive a few ticks, so the bound sees each child while its parent
		// lives.
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

// TreeBoundSuite runs fork trees under Exec and asserts what survives it.
type TreeBoundSuite struct {
	suite.Suite
	dir string
}

func TestTreeBoundSuite(t *testing.T) {
	suite.Run(t, new(TreeBoundSuite))
}

func (s *TreeBoundSuite) SetupTest() {
	s.dir = s.T().TempDir()
	processBounds.setLimit(8)
}

func (s *TreeBoundSuite) TearDownTest() {
	processBounds.setLimit(defaultMaxTreeProcs)
}

// run execs a fork tree of depth; hold keeps the root running as well.
func (s *TreeBoundSuite) run(depth int, hold bool, shape string) error {
	env := append(os.Environ(), testPidEnv(), fmt.Sprintf("%s=%d", forkDepthEnv, depth), fmt.Sprintf("%s=%s", forkDirEnv, s.dir), fmt.Sprintf("%s=%s", forkShapeEnv, shape))
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
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func gone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

func (s *TreeBoundSuite) assertNoneAlive() {
	for _, pid := range s.recorded() {
		if !s.Eventually(func() bool { return gone(pid) }, 2*time.Second, 10*time.Millisecond, "pid %d outlived Exec", pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func (s *TreeBoundSuite) TestATreePastTheBoundFailsNamingTheBinaryAndKeepsItsExit() {
	err := s.run(4, true, "")
	s.Require().ErrorIs(err, ErrTooManyProcesses)
	name := filepath.Base(os.Args[0])
	s.Contains(err.Error(), "most of them "+name[:min(15, len(name))])
	var exit *exec.ExitError
	s.ErrorAs(err, &exit, "the command's own exit survives beside the bound")
	s.assertNoneAlive()
}

func (s *TreeBoundSuite) TestSetsidChildrenThatOutliveTheRootAreReaped() {
	s.Require().NoError(s.run(1, false, ""))
	s.Len(s.recorded(), 3, "the root and its two setsid children")
	s.assertNoneAlive()
}

func (s *TreeBoundSuite) TestALeftoverGetsSigtermBeforeSigkill() {
	s.Require().NoError(s.run(1, false, "trap"))
	_, err := os.Stat(filepath.Join(s.dir, "term"))
	s.Require().NoError(err, "a leftover that handles SIGTERM got the chance to")
	s.assertNoneAlive()
}

func (s *TreeBoundSuite) TestAProcessThatDropsTheMarkOutlivesTheRun() {
	s.Require().NoError(s.run(1, false, "unmarked"))
	children := slices.DeleteFunc(s.recorded(), gone)
	s.Len(children, 2, "both unmarked children outlive the run, though every tick saw them")
	for _, pid := range children {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (s *TreeBoundSuite) TestANestedMarkAppendsToTheOneItInherits() {
	env, mark := (&treeBounds{}).marked([]string{"A=1", TreeMarkEnv + "=outer"})
	s.Equal([]string{"A=1", fmt.Sprintf("%s=outer,%s", TreeMarkEnv, mark)}, env)
	s.True(hasMark("outer,"+mark, mark))
	s.False(hasMark("outer", mark))
}
