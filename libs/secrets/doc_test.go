package secrets

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// allowedRequires is every module the package doc lets this module import, plus the
// modules its tests alone import: testify, and the YAML and TOML encoders a Secret's
// masking is checked against.
var allowedRequires = []string{
	"golang.org/x/sync",
	"github.com/stretchr/testify",
	"gopkg.in/yaml.v3",
	"github.com/pelletier/go-toml/v2",
}

type DocSuite struct{ suite.Suite }

func TestDoc(t *testing.T) { suite.Run(t, new(DocSuite)) }

// The dependency promise in the package doc holds only while go.mod keeps it, so a new
// direct requirement fails here and names itself.
func (s *DocSuite) TestGoModRequiresOnlyWhatTheDocAllows() {
	t := s.T()
	f, err := os.Open("go.mod")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var direct []string
	inRequire := false
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		switch {
		case line == "require (":
			inRequire = true
			continue
		case inRequire && line == ")":
			inRequire = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimPrefix(line, "require ")
		case !inRequire:
			continue
		}
		if line == "" || strings.HasSuffix(line, "// indirect") {
			continue
		}
		direct = append(direct, strings.Fields(line)[0])
	}
	require.NoError(t, scan.Err())

	for _, mod := range direct {
		assert.Contains(t, allowedRequires, mod, "go.mod requires %s, which the package doc does not allow", mod)
	}
}
