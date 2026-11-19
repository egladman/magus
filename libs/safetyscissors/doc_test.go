package safetyscissors

import (
	"bufio"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// allowedRequires is every module the package doc lets this module import. testify is
// here for the tests alone.
var allowedRequires = []string{
	"github.com/bmatcuk/doublestar/v4",
	"github.com/stretchr/testify",
	"golang.org/x/sync",
	"golang.org/x/sys",
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
	replaced := map[string]bool{}
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
		case strings.HasPrefix(line, "replace "):
			if mod, target, ok := strings.Cut(strings.TrimPrefix(line, "replace "), " => "); ok && strings.HasPrefix(target, "../") {
				replaced[strings.Fields(mod)[0]] = true
			}
			continue
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
		if strings.HasPrefix(mod, "github.com/egladman/magus/libs/") {
			// A sibling resolves through its replace, tagged or not; the release refuses
			// a libs/ require no tag answers.
			assert.True(t, replaced[mod], "go.mod requires %s with no replace to its directory beside this module", mod)
			continue
		}
		assert.Contains(t, allowedRequires, mod, "go.mod requires %s, which the package doc does not allow", mod)
	}
	assert.NotContains(t, direct, "github.com/egladman/magus", "the module never imports magus")
}

// The journal's packages import only what their docs name: session above it,
// or a magus package, would close a cycle.
func (s *DocSuite) TestJournalImportsOnlyWhatItsDocNames() {
	s.Equal([]string{
		"github.com/egladman/magus/libs/codec",
		"github.com/egladman/magus/libs/safetyscissors/job",
	}, s.nonStandardImports("journal"))
	s.Equal([]string{
		"github.com/egladman/magus/libs/apiversion",
		"github.com/egladman/magus/libs/codec",
		"github.com/egladman/magus/libs/safetyscissors/journal",
	}, s.nonStandardImports(filepath.Join("journal", "stream")))
}

// nonStandardImports lists the imports of dir's non-test files that are not
// in the standard library, sorted.
func (s *DocSuite) nonStandardImports(dir string) []string {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	s.Require().NoError(err)
	seen := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		s.Require().NoError(err)
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			s.Require().NoError(err)
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
				seen[path] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}
