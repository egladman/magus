package langservice

import (
	"context"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/suite"
)

func labels(cs []Completion) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Label
	}
	return out
}

func findLabel(cs []Completion, label string) (Completion, bool) {
	for _, c := range cs {
		if c.Label == label {
			return c, true
		}
	}
	return Completion{}, false
}

type completeSuite struct {
	suite.Suite
	svc *Service
}

func TestCompleteSuite(t *testing.T) { suite.Run(t, new(completeSuite)) }

func (s *completeSuite) SetupTest() { s.svc = New(fixture()) }

func (s *completeSuite) complete(src string) []Completion {
	return s.svc.Complete(src, len(src))
}

func (s *completeSuite) TestImportPath() {
	s.Equal([]Completion{
		{Label: "fmt", Kind: KindModule, Doc: "Formatting.", Replace: 1},
		{Label: "fs", Kind: KindModule, Doc: "Filesystem access.", Replace: 1},
	}, s.complete(`import "f`))
}

func (s *completeSuite) TestModuleMembers() {
	got := s.complete("import \"fs\";\nfs\\")
	s.Equal([]string{"glob", "join", "sep", "stat", "writeFile"}, labels(got))
	for _, c := range got {
		s.Equal(0, c.Replace)
	}
	sep, _ := findLabel(got, "sep")
	s.Equal(Completion{Label: "sep", Kind: KindField, Detail: `fs\sep: str`, Doc: "The path separator."}, sep)
}

func (s *completeSuite) TestModuleMembersPartial() {
	s.Equal([]Completion{{
		Label:   "glob",
		Kind:    KindMethod,
		Detail:  `fs\glob(pattern: str) > [str] !> any`,
		Doc:     "Glob returns the paths matching pattern.\n\nOrder is lexical.",
		Replace: 2,
	}}, s.complete(`fs\gl`))
}

// A dot reaches a value's member, never a module's: offering fs's methods there
// would complete a spelling the checker rejects.
func (s *completeSuite) TestDotOffersNoHostMembers() {
	s.Empty(s.complete("import \"fs\";\nfs.gl"))
	s.Empty(s.complete("final xs = [1];\nxs.le"))
}

func (s *completeSuite) TestNonModuleBackslashOffersNothing() {
	s.Empty(s.complete(`nope\`))
}

func (s *completeSuite) TestChainedAccessOffersNothing() {
	s.Empty(s.complete(`a\fs\`))
}

func (s *completeSuite) TestAliasedImport() {
	_, ok := findLabel(s.complete("import \"fs\" as f;\nf\\"), "glob")
	s.True(ok)
}

func (s *completeSuite) TestBuzzSchemeImport() {
	_, ok := findLabel(s.complete("import \"buzz:fs\";\nfs\\"), "glob")
	s.True(ok)
}

func (s *completeSuite) TestWordOffersDeclarationsImportsBuiltinsKeywords() {
	src := "import \"fs\";\nexport fun build(args: [str]) > void {}\nfinal fsRoot = \"/\";\nb"
	got := s.complete(src)
	build, ok := findLabel(got, "build")
	s.Require().True(ok)
	s.Equal(Completion{Label: "build", Kind: KindFunction, Detail: "fun build(args: [str])", Replace: 1}, build)
	_, ok = findLabel(got, "bool")
	s.True(ok, "builtin bool: %v", labels(got))
	_, ok = findLabel(got, "break")
	s.True(ok, "keyword break: %v", labels(got))
	for _, c := range got {
		s.True(strings.HasPrefix(c.Label, "b"), c.Label)
	}

	fsWords := s.complete(src[:strings.LastIndex(src, "\n")+1] + "fs")
	fs, ok := findLabel(fsWords, "fs")
	s.Require().True(ok)
	s.Equal(Completion{Label: "fs", Kind: KindModule, Doc: "Filesystem access.", Replace: 2}, fs)
	_, ok = findLabel(fsWords, "fsRoot")
	s.True(ok)
}

func (s *completeSuite) TestEmptyPrefixIsQuiet() {
	s.Empty(s.svc.Complete("   ", 3))
}

func (s *completeSuite) TestOffsetClamped() {
	s.Equal(s.complete(`fs\`), s.svc.Complete(`fs\`, 999))
	s.Empty(s.svc.Complete(`fs\`, -5))
}

// TestBuiltinsAreTheCheckers pins builtins to checker.registerBuiltins: each
// name type-checks as a global, and a name the checker dropped does not.
func (s *completeSuite) TestBuiltinsAreTheCheckers() {
	for _, name := range builtins {
		s.Empty(checkErrors("final f = "+name+";\n"), name)
	}
	s.NotEmpty(checkErrors("final f = len;\n"), "len is a method, not a builtin")
}

func checkErrors(src string) []buzz.Diagnostic {
	sess := buzz.NewSession(context.Background())
	defer sess.Close()
	var errs []buzz.Diagnostic
	for _, d := range sess.Diagnostics(src) {
		if d.Severity == buzz.SeverityError {
			errs = append(errs, d)
		}
	}
	return errs
}

// BenchmarkFirstMemberComplete is the first `fs\` completion of a fresh
// service: the one that pays for parsing the module.
func BenchmarkFirstMemberComplete(b *testing.B) {
	table := fixture()
	src := "import \"fs\";\nfs\\"
	for b.Loop() {
		if len(New(table).Complete(src, len(src))) == 0 {
			b.Fatal("no completions")
		}
	}
}
