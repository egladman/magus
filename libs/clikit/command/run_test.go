package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/stretchr/testify/suite"
)

type ExecuteSuite struct {
	suite.Suite
	stdout, stderr bytes.Buffer
}

func TestExecuteSuite(t *testing.T) { suite.Run(t, new(ExecuteSuite)) }

func (s *ExecuteSuite) SetupTest() {
	s.stdout.Reset()
	s.stderr.Reset()
}

func (s *ExecuteSuite) execute(root Command, args []string, opts ...Option) int {
	return Execute(context.Background(), root, args, Streams{Stdin: strings.NewReader(""), Stdout: &s.stdout, Stderr: &s.stderr}, opts...)
}

func (s *ExecuteSuite) TestFlagsReachTheirTargetsAndOperandsTheInvocation() {
	var out string
	var all bool
	var wait time.Duration
	var from []string
	var got Invocation
	root := Command{Name: "tool", Flags: []Flag{
		{Name: "out", Kind: String, Default: "a.txt", Target: &out},
		{Name: "all", Kind: Bool, Default: false, Target: &all},
		{Name: "a", Kind: Bool, AliasOf: "all"},
		{Name: "wait", Kind: Duration, Unit: "s", Target: &wait},
		{Name: "from", Kind: StringList, Target: &from},
	}, Run: func(_ context.Context, inv Invocation) error {
		got = inv
		return nil
	}}
	code := s.execute(root, []string{"-out", "b.txt", "-a", "--wait", "3", "-from", "x", "-from", "y", "op"})
	s.Equal(exit.OK, code, s.stderr.String())
	s.Equal(Invocation{Streams: got.Streams, Path: "tool", Args: []string{"op"}}, got)
	s.Equal("b.txt", out)
	s.True(all)
	s.Equal(3*time.Second, wait)
	s.Equal([]string{"x", "y"}, from)
}

func (s *ExecuteSuite) TestAFlagLeftUnsetHoldsItsDefault() {
	out := "stale"
	root := Command{Name: "tool", Flags: []Flag{{Name: "out", Kind: String, Default: "a.txt", Target: &out}},
		Run: func(context.Context, Invocation) error { return nil }}
	s.Equal(exit.OK, s.execute(root, nil))
	s.Equal("a.txt", out)
}

func (s *ExecuteSuite) TestAChildIsDispatchedByItsName() {
	ran := ""
	root := Command{Name: "tool", Children: []Command{
		{Name: "gen", Run: func(_ context.Context, inv Invocation) error {
			ran = inv.Path + " " + strings.Join(inv.Args, " ")
			return nil
		}},
	}}
	s.Equal(exit.OK, s.execute(root, []string{"gen", "x"}))
	s.Equal("tool gen x", ran)
}

func (s *ExecuteSuite) TestAnUnknownChildIsAUsageError() {
	root := Command{Name: "tool", Children: []Command{{Name: "b", Run: noop}, {Name: "a", Run: noop}}}
	s.Equal(exit.Usage, s.execute(root, []string{"c"}))
	s.Equal("tool: unknown command \"c\", want one of a, b: `tool -h` lists them\n", s.stderr.String())
}

func (s *ExecuteSuite) TestAMissingChildIsAUsageError() {
	root := Command{Name: "tool", Children: []Command{{Name: "a", Run: noop}}}
	s.Equal(exit.Usage, s.execute(root, nil))
	s.Equal("tool: a command is required, one of a: `tool -h` lists them\n", s.stderr.String())
}

func (s *ExecuteSuite) TestARootWithARunTakesAnOperandNoChildNames() {
	var args []string
	root := Command{Name: "tool", Run: func(_ context.Context, inv Invocation) error {
		args = inv.Args
		return nil
	}, Children: []Command{{Name: "shots", Run: noop}}}
	s.Equal(exit.OK, s.execute(root, []string{"script.sh"}))
	s.Equal([]string{"script.sh"}, args)
}

func (s *ExecuteSuite) TestAnUnknownFlagNamesTheNearestAndTheProgramsHelp() {
	root := Command{Name: "magus-tool", Flags: []Flag{{Name: "out", Kind: String}}, Run: noop}
	s.Equal(exit.Usage, s.execute(root, []string{"-otu", "x"}))
	s.Equal("magus-tool: flag provided but not defined: -otu: did you mean -out? `magus-tool -h` lists the flags\n", s.stderr.String())
}

func (s *ExecuteSuite) TestHelpPrintsTheDeclarationToStdout() {
	root := Command{Name: "tool", Short: "Render the pages.", Flags: []Flag{
		{Name: "out", Kind: String, Default: "docs", Doc: "the `dir` to write"},
		{Name: "check", Kind: Bool, Default: false, Doc: "type-check only"},
		{Name: "c", Kind: Bool, AliasOf: "check", Doc: "type-check only"},
	}, Children: []Command{{Name: "gen", Short: "Write them", Run: noop}}, Run: noop}
	s.Equal(exit.OK, s.execute(root, []string{"-h"}))
	s.Equal(`Usage: tool [flags] <command>

Render the pages.

Commands:
  gen  Write them

Flags:
  -c, --[no-]check  type-check only (default off)
  --out dir         the dir to write (default docs)
`, s.stdout.String())
	s.Empty(s.stderr.String())
}

func (s *ExecuteSuite) TestVersionPrintsTheLine() {
	root := Command{Name: "tool", Flags: []Flag{{Name: "v", Kind: Bool, AliasOf: "version"}}, Run: noop}
	s.Equal(exit.OK, s.execute(root, []string{"-v"}, WithVersion("tool 1.2")))
	s.Equal("tool 1.2\n", s.stdout.String())
}

func (s *ExecuteSuite) TestARunErrorIsPrintedAndSetsTheStatus() {
	root := Command{Name: "tool", Run: func(context.Context, Invocation) error { return exit.Usagef("no input") }}
	s.Equal(exit.Usage, s.execute(root, nil))
	s.Equal("tool: no input\n", s.stderr.String())
}

func (s *ExecuteSuite) TestAReportedErrorIsNotPrinted() {
	root := Command{Name: "tool", Run: func(context.Context, Invocation) error { return exit.Reported(3) }}
	s.Equal(3, s.execute(root, nil))
	s.Empty(s.stderr.String())
}

func (s *ExecuteSuite) TestACommandWithNothingToRunFailsBeforeAnythingRuns() {
	ran := false
	root := Command{Name: "tool", Children: []Command{{Name: "a", Run: func(context.Context, Invocation) error {
		ran = true
		return nil
	}}, {Name: "b"}}}
	s.Equal(exit.Failure, s.execute(root, []string{"a"}))
	s.False(ran)
	s.Equal("tool: command:b has neither a Run nor Children\n", s.stderr.String())
}

func (s *ExecuteSuite) TestAMistypedTargetFails() {
	var n string
	root := Command{Name: "tool", Flags: []Flag{{Name: "n", Kind: Int, Target: &n}}, Run: noop}
	s.Equal(exit.Failure, s.execute(root, nil))
	s.Equal("tool: flag --n is int but its Target is a *string, want a *int\n", s.stderr.String())
}

func (s *ExecuteSuite) TestTheContextReachesTheRun() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := Command{Name: "tool", Run: func(ctx context.Context, _ Invocation) error { return ctx.Err() }}
	code := Execute(ctx, root, nil, Streams{Stdout: &s.stdout, Stderr: &s.stderr})
	s.Equal(exit.Failure, code)
	s.True(strings.HasSuffix(s.stderr.String(), context.Canceled.Error()+"\n"))
	s.True(errors.Is(ctx.Err(), context.Canceled))
}

func noop(context.Context, Invocation) error { return nil }
