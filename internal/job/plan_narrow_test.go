package job

import (
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// resolveSymbolGoalsNarrowsSuite pins the claims derived on a file that a row
// holds whole by a literal entry, which the fork then narrows.
type resolveSymbolGoalsNarrowsSuite struct {
	suite.Suite
	read SymbolReader
}

func TestResolveSymbolGoalsNarrows(t *testing.T) { suite.Run(t, new(resolveSymbolGoalsNarrowsSuite)) }

func (s *resolveSymbolGoalsNarrowsSuite) SetupTest() {
	s.read = symbolReader(map[string]SymbolFact{
		"A": {DefinedIn: []string{"run.go"}, Signature: "func A(ctx int)"},
	})
}

func (s *resolveSymbolGoalsNarrowsSuite) resolve(paths ...string) []string {
	row := types.Job{ID: "j", WritePaths: paths, Goals: []types.Goal{
		{ID: "moved", Kind: types.GoalKindSymbol, Expect: types.ExpectChanged, Symbols: []string{"A"}},
	}}
	_, claims, err := ResolveSymbolGoals(s.T().Context(), row, s.read)
	s.Require().NoError(err)
	return claims
}

func (s *resolveSymbolGoalsNarrowsSuite) TestALiteralWholeFileEntryYieldsTheDeclarationClaim() {
	s.Equal([]string{"run.go#func A"}, s.resolve("run.go"))
	s.Equal([]string{"run.go#func A"}, s.resolve("./run.go"), "a leading ./ names the same file")
}

func (s *resolveSymbolGoalsNarrowsSuite) TestAClaimAlreadyHeldIsNotDerivedAgain() {
	s.Empty(s.resolve("run.go", "run.go#func A"))
}

func (s *resolveSymbolGoalsNarrowsSuite) TestAPatternEntryYieldsNoClaim() {
	s.Empty(s.resolve("*.go"))
	s.Empty(s.resolve("**/*.go"))
}

func (s *resolveSymbolGoalsNarrowsSuite) TestAnEntryThatIsNotTheFileItselfYieldsNoClaim() {
	s.Empty(s.resolve("."))
}

func (s *resolveSymbolGoalsNarrowsSuite) TestWithoutIndexEvidenceTheFileStaysWhole() {
	s.read = symbolReader(map[string]SymbolFact{})
	s.Empty(s.resolve("run.go"), "a symbol the index cannot read claims nothing")
	s.read = nil
	s.Empty(s.resolve("run.go"), "a door with no symbol graph claims nothing")
}

func (s *resolveSymbolGoalsNarrowsSuite) TestAForkNarrowsTheWholeHoldToTheClaim() {
	root := testkit.NewRepository(s.T(), map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": claimGo}).Root()
	store := NewStore(tempLocation(s.T(), root))
	goals := []types.Goal{{ID: "moved", Kind: types.GoalKindSymbol, Symbols: []string{"A"}}}

	stored, err := ForkMerge(s.T().Context(), store, "wave/job", func(u *types.Job) {
		u.Check, u.State, u.WritePaths, u.Goals = forkCheck(), types.StateDeclared, []string{"run.go"}, goals
	}, config.Jobs{}, s.read)
	s.Require().NoError(err)
	s.Equal([]string{"run.go", "run.go#func A"}, stored.WritePaths)

	narrowed, added, err := NarrowHold(s.T().Context(), store, stored.ID, "run.go", []string{"func A"}, "")
	s.Require().NoError(err)
	s.Empty(added, "the claim is already held, so the hold only records that it was narrowed")
	s.True(Narrowed(narrowed, "run.go"))
}
