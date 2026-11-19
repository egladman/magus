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
	path := filepath.Join(s.T().TempDir(), "inputs.yaml")
	sha := strings.Repeat("a", 40)
	s.Require().NoError(os.WriteFile(path, []byte(row("x/y", "no", "")+row("cutoff/first", sha, "  date: d\n  status: proposed\n")), 0o644))
	c, err := ReadCutoff(path)
	s.Require().NoError(err)
	s.Equal(Cutoff{Commit: sha, Date: "d", Status: "proposed"}, c)
}

// row is one inputs.yaml entry with the fields ReadInputs requires; extra holds more
// indented lines.
func row(name, revision, extra string) string {
	return "- name: " + name + "\n  source: https://x\n  revision: \"" + revision + "\"\n  sha256: \"" +
		strings.Repeat("b", 64) + "\"\n  path: p/" + name + "\n" + extra
}
