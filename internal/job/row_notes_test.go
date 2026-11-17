package job

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// A checkout whose magusfile this magus cannot load keeps one line naming the code, never
// the spell dump a load failure renders.
func TestUnloadableMagusfileIsOneLine(t *testing.T) {
	t.Parallel()
	coded := fmt.Errorf("loading magusfile: %w", types.DiagnosticErrorf(types.NoCITarget, "no ci target\n  at magusfile.buzz:1\n  spell dump"))
	assert.EqualError(t, UnloadableMagusfile(coded), fmt.Sprintf("this magus cannot load its magusfile (%s)", types.NoCITarget))
	assert.EqualError(t, UnloadableMagusfile(errors.New("parse failed\n  spell dump")), "parse failed")
}

type ForkJudgedPathSuite struct {
	suite.Suite
	store *Store
}

func TestForkJudgedPathSuite(t *testing.T) {
	suite.Run(t, new(ForkJudgedPathSuite))
}

func (s *ForkJudgedPathSuite) SetupTest() {
	root := testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n"}).Root()
	s.store = NewStore(Location{StateBase: s.T().TempDir(), CacheDir: s.T().TempDir(), Root: root})
}

func (s *ForkJudgedPathSuite) seed(state types.JobState) {
	_, err := s.store.Update(s.T().Context(), "w", func(u *types.Job) {
		*u = types.Job{ID: "w", State: state, ReadOnly: true}
	})
	s.Require().NoError(err)
}

func (s *ForkJudgedPathSuite) TestALiveRowIsJudgedAsAnUpdate() {
	s.seed(types.StateDeclared)
	s.True(NoteForkJudgedAsUpdate(s.T().Context(), s.store, "w"))
}

func (s *ForkJudgedPathSuite) TestAnEndedRowIsJudgedWhole() {
	s.seed(types.StateNoReturn)
	s.False(NoteForkJudgedAsUpdate(s.T().Context(), s.store, "w"))
}

func (s *ForkJudgedPathSuite) TestAnUnknownRowIsJudgedWhole() {
	s.False(NoteForkJudgedAsUpdate(s.T().Context(), s.store, "nobody"))
}

func TestConsoleSkew(t *testing.T) {
	require.Empty(t, ConsoleSkew("v1", "v1"))
	require.Empty(t, ConsoleSkew("", "v1"), "a server that did not say is not skew")
	got := ConsoleSkew("v1", "v2")
	require.Equal(t, "the console serves build v1; this command uses build v2", got)
}
