package job

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type NewDirsSuite struct {
	suite.Suite
	root string
}

func TestNewDirs(t *testing.T) { suite.Run(t, new(NewDirsSuite)) }

func (s *NewDirsSuite) SetupTest() {
	s.root = s.T().TempDir()
	for _, rel := range []string{"internal/journal/j.go", "internal/record/r.go", "internal/replay/x.go", "testdata/keep.txt"} {
		p := filepath.Join(s.root, filepath.FromSlash(rel))
		s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
		s.Require().NoError(os.WriteFile(p, nil, 0o644))
	}
}

func (s *NewDirsSuite) graphDirs() ([]string, error) {
	return []string{"internal", "dir:internal/journal", "internal/record", "internal/replay", "libs/host_call", "libs/callGraph"}, nil
}

func (s *NewDirsSuite) TestADirectoryNeitherTheGraphNorTheTreeHoldsIsNew() {
	got := MeasureNewDirs(s.root, []string{"internal/hostcall/call.go", "internal/journal/new.go", "testdata/new.txt"}, s.graphDirs)

	s.Equal(job.NewDirs{Dirs: []job.NewDir{{Dir: "internal/hostcall"}}}, got,
		"journal is a graph node and testdata is on disk, so neither is created")
}

func (s *NewDirsSuite) TestNeighborsShareAWordOfTheLastSegment() {
	got := MeasureNewDirs(s.root, []string{"internal/replay_record/a.go"}, s.graphDirs)

	s.Equal([]job.NewDir{{Dir: "internal/replay_record", Neighbors: []string{"internal/record", "internal/replay"}}}, got.Dirs)
}

func (s *NewDirsSuite) TestWordsSplitOnCaseDashesAndUnderscores() {
	got := MeasureNewDirs(s.root, []string{"cmd/host-call/main.go"}, s.graphDirs)

	s.Equal([]job.NewDir{{Dir: "cmd", Neighbors: nil}, {Dir: "cmd/host-call", Neighbors: []string{"libs/callGraph", "libs/host_call"}}}, got.Dirs,
		"every directory a write path creates is listed, its parents included")
}

func (s *NewDirsSuite) TestAGlobIsMeasuredAtItsLiteralDirectory() {
	got := MeasureNewDirs(s.root, []string{"internal/hostcall/**/*.go", "**/*.go", "!internal/gone/x.go"}, s.graphDirs)

	s.Equal([]job.NewDir{{Dir: "internal/hostcall"}}, got.Dirs)
}

func (s *NewDirsSuite) TestAnUnreadIndexIsUnknownNotEmpty() {
	failed := MeasureNewDirs(s.root, []string{"internal/hostcall/a.go"}, func() ([]string, error) { return nil, errors.New("no graph built") })
	s.Equal("the graph's directories could not be read: no graph built", failed.Unknown)
	s.Empty(failed.Dirs)

	empty := MeasureNewDirs(s.root, []string{"internal/hostcall/a.go"}, func() ([]string, error) { return nil, nil })
	s.Equal("the graph holds no directory nodes; `magus graph build` builds them", empty.Unknown)

	s.Equal("no directory index was supplied", MeasureNewDirs(s.root, []string{"internal/hostcall/a.go"}, nil).Unknown)
}

func (s *NewDirsSuite) TestWritesIntoExistingDirectoriesReadNoIndex() {
	unread := func() ([]string, error) {
		s.Fail("the index was read for directories already on disk")
		return nil, nil
	}

	s.Equal(job.NewDirs{}, MeasureNewDirs(s.root, []string{"internal/journal/new.go", "testdata/keep.txt"}, unread))
}

func (s *NewDirsSuite) TestLinesNameEachDirectoryAndItsNeighbours() {
	got := MeasureNewDirs(s.root, []string{"internal/replay_record/a.go", "internal/zzz/b.go"}, s.graphDirs)

	s.Equal([]string{
		"new directory internal/replay_record: no dir node; directories sharing its words: internal/record, internal/replay",
		"new directory internal/zzz: no dir node; directories sharing its words: none",
	}, got.Lines())
	s.Empty(MeasureNewDirs(s.root, []string{"internal/journal/x.go"}, s.graphDirs).Lines(), "a fork creating nothing prints nothing")
	s.Equal([]string{"new directories: unknown, no directory index was supplied"}, MeasureNewDirs(s.root, []string{"internal/zzz/b.go"}, nil).Lines())
}

func (s *NewDirsSuite) TestACreatesEntryThatExistsIsAProblem() {
	store := NewStore(tempLocation(s.T(), s.root))

	ps := existingCreatesProblems(store, []string{"internal/hostcall", "internal/journal", "../out"}, nil)

	s.Equal(types.JobProblems{{
		Code:    types.JobRecordInvalid,
		Field:   "creates[1]",
		Message: `creates[1] "internal/journal" already exists, and creates names only directories the job makes`,
		Hint:    "drop the entry: a job may write under an existing directory without approval",
	}}, ps)
	s.Empty(existingCreatesProblems(store, []string{"internal/journal"}, []string{"internal/journal"}), "an entry the row held already is not judged again")
}
