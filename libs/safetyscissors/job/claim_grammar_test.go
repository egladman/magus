package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type ClaimGrammarSuite struct{ suite.Suite }

func TestClaimGrammar(t *testing.T) { suite.Run(t, new(ClaimGrammarSuite)) }

func (s *ClaimGrammarSuite) split(entry string) [2]string {
	path, declaration := SplitClaim(entry)
	return [2]string{path, declaration}
}

func (s *ClaimGrammarSuite) TestAnEntryWithoutAHashIsAWholeFile() {
	s.Equal([2]string{"run.go", ""}, s.split("run.go"))
	s.Equal([2]string{"./run.go", ""}, s.split("./run.go"))
	s.Equal([2]string{"", ""}, s.split(""))
}

func (s *ClaimGrammarSuite) TestAHashClaimsADeclarationTrimmedOfSpace() {
	s.Equal([2]string{"run.go", "executeStages"}, s.split("run.go#executeStages"))
	s.Equal([2]string{"run.go", "executeStages"}, s.split(" run.go # executeStages "))
	s.Equal([2]string{"docs/scope.md", "The knobs"}, s.split("docs/scope.md#The knobs"))
	s.Equal([2]string{"", "A"}, s.split("#A"))
	s.Equal([2]string{"run.go", ""}, s.split("run.go#"))
}

func (s *ClaimGrammarSuite) TestTheFirstHashSplitsAMarkdownHeading() {
	s.Equal([2]string{"docs/scope.md", "## Usage"}, s.split("docs/scope.md### Usage"))
}

func (s *ClaimGrammarSuite) TestAnEscapedOrDotSlashHashIsPartOfThePath() {
	s.Equal([2]string{"notes/a#b.md", ""}, s.split("./notes/a#b.md"))
	s.Equal([2]string{"notes/a#b.md", ""}, s.split(`notes/a\#b.md`))
	s.Equal([2]string{"notes/a#b.md", "Intro"}, s.split(`notes/a\#b.md#Intro`))
}

func (s *ClaimGrammarSuite) TestHasClaimSeesOnlyAnUnescapedHash() {
	s.True(HasClaim("run.go#A"))
	s.True(HasClaim("run.go#"))
	s.True(HasClaim(`a\#b.md#A`))
	s.False(HasClaim("run.go"))
	s.False(HasClaim("./a#b.md"))
	s.False(HasClaim(`a\#b.md`))
}

const claimedMethod = "func (m *Magus) executeStages(ctx context.Context) error {"

func (s *ClaimGrammarSuite) TestAClaimNamesADeclarationByAnIdentifierBoundedRun() {
	s.True(NamesDeclaration("executeStages", claimedMethod))
	s.True(NamesDeclaration("Magus) executeStages", claimedMethod))
	s.True(NamesDeclaration(claimedMethod, claimedMethod))
	s.True(NamesDeclaration("Magus", claimedMethod))
	s.True(NamesDeclaration("executeStages(ctx", claimedMethod))
	s.False(NamesDeclaration("execute", claimedMethod))
	s.False(NamesDeclaration("Stages", claimedMethod))
}

func (s *ClaimGrammarSuite) TestAClaimNamesHeadingsAndOtherLanguagesTheSameWay() {
	s.True(NamesDeclaration("The knobs", "## The knobs"))
	s.False(NamesDeclaration("knob", "## The knobs"))
	s.True(NamesDeclaration("$init", "const $init = () => {"))
	s.False(NamesDeclaration("init", "const $init = () => {"))
	s.True(NamesDeclaration("target", `test "target" {`))
}

func (s *ClaimGrammarSuite) TestThePreambleIsNamedOnlyByItself() {
	s.True(NamesDeclaration(Preamble, Preamble))
	s.False(NamesDeclaration("preamble", Preamble))
}

func (s *ClaimGrammarSuite) TestAnEmptySideNamesNothing() {
	s.False(NamesDeclaration("", claimedMethod))
	s.False(NamesDeclaration("executeStages", ""))
}

var claimedKeys = []string{"project.name", "project.targets", "project.targets.release", "project.targets.release-build", "project.targets.test"}

func (s *ClaimGrammarSuite) TestAnExactKeyWinsOverASiblingSharingItsPrefix() {
	named := NamedDeclarations("project.targets.release", claimedKeys)
	s.Equal([]string{"project.targets.release"}, named)
	s.True(ClaimsMap("project.targets.release", named))
}

func (s *ClaimGrammarSuite) TestAMapKeyNamesEveryEntryUnderIt() {
	named := NamedDeclarations("project.targets", claimedKeys)
	s.Equal(claimedKeys[1:], named)
	s.True(ClaimsMap("project.targets", named))
	s.Equal(claimedKeys, NamedDeclarations("project", claimedKeys), "the root key claims the whole map")
	s.True(ClaimsMap("project", claimedKeys))
}

func (s *ClaimGrammarSuite) TestASegmentRunNamesKeysWithoutClaimingTheMap() {
	named := NamedDeclarations("targets.test", claimedKeys)
	s.Equal([]string{"project.targets.test"}, named)
	s.False(ClaimsMap("targets.test", named))
	s.Equal(claimedKeys[1:], NamedDeclarations("targets", claimedKeys), "a mid-path word shared by keys is several")
	s.False(ClaimsMap("targets", claimedKeys[1:]))
}

func (s *ClaimGrammarSuite) TestAKeyNotWrittenYetNamesNone() {
	s.Nil(NamedDeclarations("project.targets.lint", claimedKeys))
	s.False(ClaimsMap("project.targets.lint", nil))
}

func (s *ClaimGrammarSuite) TestADeclarationLineKeepsTheIdentifierBoundedRule() {
	s.Equal([]string{"func A(ctx int) {"}, NamedDeclarations("A", []string{"func A(ctx int) {", "func B() {"}))
}

func (s *ClaimGrammarSuite) TestAFileChangeLocatesAsItsWholeFile() {
	loc := FileChange{Path: "run.go", Status: ChangeModified}.Location()
	s.Equal(Location{Path: "run.go"}, loc)
	s.Equal("run.go", loc.String())
}

func (s *ClaimGrammarSuite) TestARegionLocatesAsItsDeclaration() {
	loc := RegionChange{File: FileChange{Path: "run.go"}, Side: RegionNew, Lines: [2]int{3, 9}, Declaration: "func executeStages", Driver: "golang"}.Location()
	s.Equal(Location{Path: "run.go", Declaration: "func executeStages"}, loc)
	s.Equal("run.go#func executeStages", loc.String())
}

func (s *ClaimGrammarSuite) TestARegionAboveTheFirstDeclarationIsThePreamble() {
	loc := RegionChange{File: FileChange{Path: "run.go"}, Side: RegionNew, Lines: [2]int{3, 4}, Driver: "golang"}.Location()
	s.Equal(Location{Path: "run.go", Declaration: Preamble}, loc)
	s.Equal("run.go#(preamble)", loc.String())
}

func (s *ClaimGrammarSuite) TestARegionNoDriverPlacedIsItsWholeFile() {
	loc := RegionChange{File: FileChange{Path: "notes.txt"}, Side: RegionOld, Lines: [2]int{1, 2}}.Location()
	s.Equal(Location{Path: "notes.txt"}, loc)
	s.Equal("notes.txt", loc.String())
}

func region(path, decl string, side RegionSide) Locator {
	return RegionChange{File: FileChange{Path: path}, Side: side, Lines: [2]int{1, 2}, Declaration: decl, Driver: "golang"}
}

func undriven(path string, side RegionSide) Locator {
	return RegionChange{File: FileChange{Path: path}, Side: side, Lines: [2]int{1, 2}}
}

func wholeFile(path string) Locator { return FileChange{Path: path} }

// collide asserts Collisions of a and b both ways round, since it is symmetric.
func (s *ClaimGrammarSuite) collide(a, b []Locator, want []Location) {
	s.T().Helper()
	s.Equal(want, Collisions(a, b))
	s.Equal(want, Collisions(b, a), "Collisions is symmetric")
}

func (s *ClaimGrammarSuite) TestEqualDeclarationsCollideWhateverTheirSide() {
	s.collide([]Locator{region("a.go", "func X() {", RegionNew)}, []Locator{region("a.go", "func X() {", RegionOld)},
		[]Location{{Path: "a.go", Declaration: "func X() {"}})
}

func (s *ClaimGrammarSuite) TestDifferentDeclarationsInOneFileDoNotCollide() {
	s.collide([]Locator{region("a.go", "func X() {", RegionNew)}, []Locator{region("a.go", "func Y() {", RegionNew)}, nil)
}

func (s *ClaimGrammarSuite) TestAFileCollidesWithEveryRegionOfItsFile() {
	s.collide([]Locator{wholeFile("a.go")},
		[]Locator{region("a.go", "func X() {", RegionNew), region("a.go", "func Y() {", RegionOld), region("b.go", "func Z() {", RegionNew)},
		[]Location{{Path: "a.go", Declaration: "func X() {"}, {Path: "a.go", Declaration: "func Y() {"}})
}

func (s *ClaimGrammarSuite) TestARegionNoDriverPlacedCoversItsFile() {
	s.collide([]Locator{region("a.go", "func X() {", RegionNew), region("b.go", "func Z() {", RegionNew)},
		[]Locator{undriven("a.go", RegionNew)},
		[]Location{{Path: "a.go", Declaration: "func X() {"}})
	s.collide([]Locator{wholeFile("a.go")}, []Locator{undriven("a.go", RegionOld)}, []Location{{Path: "a.go"}})
}

func (s *ClaimGrammarSuite) TestPreambleEditsCollideOnlyWithEachOther() {
	s.collide([]Locator{region("a.go", "", RegionNew), region("b.go", "", RegionNew)},
		[]Locator{region("a.go", "", RegionOld), region("b.go", "func Y() {", RegionNew)},
		[]Location{{Path: "a.go", Declaration: Preamble}})
}

func (s *ClaimGrammarSuite) TestDifferentFilesNeverCollide() {
	s.collide([]Locator{wholeFile("a.go"), region("b.go", "func X() {", RegionNew)},
		[]Locator{wholeFile("c.go"), region("d.go", "func X() {", RegionNew)}, nil)
	s.collide([]Locator{wholeFile("a.go")}, nil, nil)
}

func (s *ClaimGrammarSuite) TestCollisionsAreSortedAndDeduplicated() {
	s.collide(
		[]Locator{region("z.go", "func Z() {", RegionNew), region("a.go", "func A() {", RegionNew), region("a.go", "func A() {", RegionOld)},
		[]Locator{region("a.go", "func A() {", RegionNew), region("a.go", "func A() {", RegionNew), region("z.go", "func Z() {", RegionNew)},
		[]Location{{Path: "a.go", Declaration: "func A() {"}, {Path: "z.go", Declaration: "func Z() {"}})
}
