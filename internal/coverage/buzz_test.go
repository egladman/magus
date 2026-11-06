package coverage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

type BuzzSourcesSuite struct {
	suite.Suite
	reads [][]string
}

func TestBuzzSourcesSuite(t *testing.T) { suite.Run(t, new(BuzzSourcesSuite)) }

func (s *BuzzSourcesSuite) SetupTest() { s.reads = nil }

// read names a script by its arguments: "eval" for a snippet, else its path.
func (s *BuzzSourcesSuite) read(eval string, args []string) (string, string, []string, error) {
	s.reads = append(s.reads, append([]string{eval}, args...))
	if eval != "" {
		return eval, "eval", nil, nil
	}
	if len(args) == 0 {
		return "code of stdin", "stdin", nil, nil
	}
	if args[0] == "missing.buzz" {
		return "", "", nil, errors.New("missing.buzz: no such file")
	}
	return "code of " + args[0], args[0], nil, nil
}

func (s *BuzzSourcesSuite) TestEachPathIsOneScriptWithTheForwardedArgs() {
	got, err := BuzzTestSources(s.read, "", []string{"a.buzz"}, []string{"--flag"})
	s.Require().NoError(err)
	s.Equal([]BuzzScript{{Code: "code of a.buzz", Name: "a.buzz", Args: []string{"--flag"}}}, got)
}

func (s *BuzzSourcesSuite) TestSeveralPathsAreReadInOrder() {
	got, err := BuzzTestSources(s.read, "", []string{"a.buzz", "b.buzz"}, nil)
	s.Require().NoError(err)
	s.Equal([]string{"a.buzz", "b.buzz"}, []string{got[0].Name, got[1].Name})
}

func (s *BuzzSourcesSuite) TestArgsAfterTheSeparatorGoToOneScriptOnly() {
	_, err := BuzzTestSources(s.read, "", []string{"a.buzz", "b.buzz"}, []string{"--flag"})
	s.EqualError(err, "magus buzz: args after -- go to one script, and -t was given 2")
	s.Empty(s.reads, "nothing is read")
}

func (s *BuzzSourcesSuite) TestASnippetIsTheOneScript() {
	got, err := BuzzTestSources(s.read, "1 + 1", []string{"x"}, nil)
	s.Require().NoError(err)
	s.Equal([]BuzzScript{{Code: "1 + 1", Name: "eval"}}, got)
	s.Equal([][]string{{"1 + 1", "x"}}, s.reads)
}

func (s *BuzzSourcesSuite) TestAPathThatCannotBeReadFails() {
	_, err := BuzzTestSources(s.read, "", []string{"a.buzz", "missing.buzz"}, nil)
	s.EqualError(err, "missing.buzz: no such file")
}

// stdinAs makes f the process's stdin until the test ends.
func (s *BuzzSourcesSuite) stdinAs(f *os.File) {
	saved := os.Stdin
	os.Stdin = f
	s.T().Cleanup(func() { os.Stdin = saved })
}

func (s *BuzzSourcesSuite) TestAPipedStdinIsTheOneScript() {
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = r.Close(); _ = w.Close() })
	s.stdinAs(r)

	got, err := BuzzTestSources(s.read, "", nil, []string{"--flag"})

	s.Require().NoError(err)
	s.Equal([]BuzzScript{{Code: "code of stdin", Name: "stdin", Args: []string{"--flag"}}}, got)
}

func (s *BuzzSourcesSuite) TestAFileOnStdinIsTheOneScript() {
	f, err := os.CreateTemp(s.T().TempDir(), "script")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = f.Close() })
	s.stdinAs(f)

	s.True(stdinIsPiped())
}

func (s *BuzzSourcesSuite) TestDevNullOnStdinTestsNothingAndIsRefused() {
	f, err := os.Open(os.DevNull)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = f.Close() })
	s.stdinAs(f)

	_, err = BuzzTestSources(s.read, "", nil, nil)

	s.EqualError(err, "magus buzz: -t <file>... names the scripts to test; none was given and nothing is piped on stdin")
	s.Empty(s.reads, "nothing is read")
}

func (s *BuzzSourcesSuite) TestAClosedStdinIsNotPiped() {
	f, err := os.CreateTemp(s.T().TempDir(), "closed")
	s.Require().NoError(err)
	s.Require().NoError(f.Close())
	s.stdinAs(f)

	s.False(stdinIsPiped())
}

type BuzzScriptsSuite struct{ suite.Suite }

func TestBuzzScriptsSuite(t *testing.T) { suite.Run(t, new(BuzzScriptsSuite)) }

func (s *BuzzScriptsSuite) TestAFailingScriptDoesNotHideTheNext() {
	var ran []string
	err := BuzzTestScripts(context.Background(), "", []BuzzScript{{Name: "a"}, {Name: "b"}},
		func(_ context.Context, sc BuzzScript, cov *buzz.LineCoverage) error {
			ran = append(ran, sc.Name)
			s.Nil(cov, "no profile, no collector")
			return errors.New(sc.Name + " failed")
		})
	s.EqualError(err, "a failed\nb failed")
	s.Equal([]string{"a", "b"}, ran)
}

func (s *BuzzScriptsSuite) TestAProfileIsWrittenOnceForEveryScript() {
	profile := filepath.Join(s.T().TempDir(), "lcov.info")
	var collectors []*buzz.LineCoverage
	err := BuzzTestScripts(context.Background(), profile, []BuzzScript{{Name: "a"}, {Name: "b"}},
		func(_ context.Context, _ BuzzScript, cov *buzz.LineCoverage) error {
			collectors = append(collectors, cov)
			return nil
		})
	s.Require().NoError(err)
	s.Require().Len(collectors, 2)
	s.NotNil(collectors[0])
	s.Same(collectors[0], collectors[1], "one shared collector")
	_, statErr := os.Stat(profile)
	s.NoError(statErr)
}

func (s *BuzzScriptsSuite) TestAProfileThatCannotBeWrittenFails() {
	profile := filepath.Join(s.T().TempDir(), "missing", "lcov.info")
	err := BuzzTestScripts(context.Background(), profile, nil,
		func(context.Context, BuzzScript, *buzz.LineCoverage) error { return nil })
	s.ErrorContains(err, "magus buzz: write --coverprofile: ")
}
