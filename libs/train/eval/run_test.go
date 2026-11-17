package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type runSuite struct{ suite.Suite }

func TestRunSuite(t *testing.T) { suite.Run(t, new(runSuite)) }

func (s *runSuite) TestReadCutoffTakesTheCutoffRow() {
	path := filepath.Join(s.T().TempDir(), "inputs.jsonl")
	sha := strings.Repeat("a", 40)
	s.Require().NoError(os.WriteFile(path, []byte(`{"name":"x/y","revision":"no"}`+"\n"+`{"name":"cutoff/first","revision":"`+sha+`","date":"d","status":"proposed"}`+"\n"), 0o644))
	c, err := ReadCutoff(path)
	s.Require().NoError(err)
	s.Equal(Cutoff{Commit: sha, Date: "d", Status: "proposed"}, c)
}
