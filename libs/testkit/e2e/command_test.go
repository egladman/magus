package e2e

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/proc/spawn"
)

type commandSuite struct{ suite.Suite }

func TestCommand(t *testing.T) { suite.Run(t, new(commandSuite)) }

func shell(script string) Command {
	return Command{Path: "/bin/sh", Args: []string{"-c", script}, Timeout: time.Minute}
}

func (s *commandSuite) TestRunKeepsTheStreamsApart() {
	r := shell("echo out; echo err >&2").Run()

	r.Elapsed = 0
	s.Equal(Result{Stdout: "out\n", Stderr: "err\n"}, r)
}

func (s *commandSuite) TestRunHandsBackTheExitError() {
	r := shell("echo partial; exit 7").Run()

	var exit *exec.ExitError
	s.Require().ErrorAs(r.Err, &exit)
	s.Equal(7, exit.ExitCode())
	s.Equal("partial\n", r.Stdout)
}

func (s *commandSuite) TestRunUsesOnlyTheGivenEnvironmentAndDirectory() {
	dir, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	c := shell(`echo "$ONLY"; echo "${HOME:-unset}"; pwd -P`)
	c.Env, c.Dir = []string{"ONLY=this"}, dir

	r := c.Run()

	s.Require().NoError(r.Err)
	s.Equal(strings.Join([]string{"this", "unset", dir, ""}, "\n"), r.Stdout)
}

// The grandchild holds the pipes, so only a kill of the whole group ends
// this before WaitDelay does.
func (s *commandSuite) TestTimeoutKillsTheProcessGroup() {
	c := shell("sleep 60 & wait")
	c.Timeout = 300 * time.Millisecond

	r := c.Run()

	s.True(r.TimedOut)
	s.ErrorContains(r.Err, "timed out after 300ms")
	s.Less(r.Elapsed, 5*time.Second)
}

func (s *commandSuite) TestRunRefusesAMissingTimeout() {
	c := shell("exit 0")
	c.Timeout = 0

	s.Require().ErrorIs(c.Run().Err, errNoTimeout)
}

func (s *commandSuite) TestConverseRefusesAMissingTimeout() {
	c := shell("exit 0")
	c.Timeout = 0

	r := c.Converse(func(io.WriteCloser, io.Reader) { s.Fail("talk ran") })

	s.Require().ErrorIs(r.Err, errNoTimeout)
}

func (s *commandSuite) TestConverseAnswersWhatTheProgramPrints() {
	r := shell(`echo question; read answer; echo "got $answer"`).Converse(func(stdin io.WriteCloser, stdout io.Reader) {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			if lines.Text() == "question" {
				_, _ = fmt.Fprintln(stdin, "yes")
			}
		}
	})

	s.Require().NoError(r.Err)
	s.Equal("question\ngot yes\n", r.Stdout)
}

func (s *commandSuite) TestConverseClosesStdinAndKeepsWhatTalkLeftUnread() {
	r := shell(`cat; echo after`).Converse(func(io.WriteCloser, io.Reader) {})

	s.Require().NoError(r.Err)
	s.Equal("after\n", r.Stdout)
}

// stragglerEnv makes this test binary a host leaving a child outside its group.
const stragglerEnv = "E2E_COMMAND_STRAGGLER"

// TestStragglerHost is the program under test when stragglerEnv is set.
func TestStragglerHost(t *testing.T) {
	if os.Getenv(stragglerEnv) == "" {
		t.Skip("run only as the program under test")
	}
	child := exec.Command("sleep", "30")
	child.Stdout = os.Stdout
	spawn.SetupProcessGroup(child)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("started")
	os.Exit(0)
}

func (s *commandSuite) TestConverseEndsAtItsTimeoutWhenAStragglerHoldsStdout() {
	c := Command{
		Path:    os.Args[0],
		Args:    []string{"-test.run=^TestStragglerHost$"},
		Env:     append(os.Environ(), stragglerEnv+"=1"),
		Timeout: time.Second,
	}

	r := c.Converse(func(_ io.WriteCloser, stdout io.Reader) {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
		}
	})

	s.True(r.TimedOut)
	s.Contains(r.Stdout, "started")
	s.Less(r.Elapsed, 10*time.Second)
}

func (s *commandSuite) TestConverseFailsToStartAMissingProgram() {
	c := Command{Path: "/no/such/program", Timeout: time.Minute}

	r := c.Converse(func(io.WriteCloser, io.Reader) { s.Fail("talk ran") })

	s.Require().Error(r.Err)
}

func (s *commandSuite) TestScrubDropsPrefixesAndKeepsExceptions() {
	got := Scrub(
		[]string{"PATH=/bin", "TOOL_MODE=x", "TOOL_TOKEN=y", "TOOLBOX=z", "EXACT=1", "EXACTLY=2"},
		[]string{"TOOL_", "EXACT="},
		[]string{"TOOL_TOKEN="},
	)

	s.Equal([]string{"PATH=/bin", "TOOL_TOKEN=y", "TOOLBOX=z", "EXACTLY=2"}, got)
}
