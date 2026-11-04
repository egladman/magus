package knowledge

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type StoredPathIDsSuite struct {
	suite.Suite
	store *Store
}

func TestStoredPathIDsSuite(t *testing.T) {
	suite.Run(t, new(StoredPathIDsSuite))
}

func (s *StoredPathIDsSuite) SetupTest() {
	s.store = NewStore(s.T().TempDir(), false, 0, nil, nil)
}

func pathIDShard(id string) Shard {
	return Shard{
		Name:  docsShardName,
		Nodes: []types.KnowledgeNode{{ID: id, Kind: types.KindFile, Label: id}},
	}
}

func (s *StoredPathIDsSuite) sync(sh Shard) {
	s.T().Helper()
	fps := map[string]string{sh.Name: fingerprintShardContent(sh)}
	require.NoError(s.T(), s.store.syncClasses(context.Background(), []Shard{sh}, fps, syncPlan{}))
}

func (s *StoredPathIDsSuite) TestReportsAManifestThatMovedUnderIt() {
	s.sync(pathIDShard("file:old.go"))
	held := s.store.readManifestOrNil()
	s.sync(pathIDShard("file:new.go"))

	_, err := s.store.storedPathIDs(context.Background(), held, nil)

	s.Require().ErrorIs(err, errShardMoved)
	s.Contains(err.Error(), "differs from the manifest")
}

func (s *StoredPathIDsSuite) TestAnswersFromAnUnmovedManifest() {
	s.sync(pathIDShard("file:a.go"))

	got, err := s.store.storedPathIDs(context.Background(), s.store.readManifestOrNil(), nil)

	s.Require().NoError(err)
	s.Equal(map[string]bool{"file:a.go": true}, got)
}

type EnsureRestartSuite struct {
	suite.Suite
	cacheDir string
	in       Inputs
	store    *Store
	gathered int
}

func TestEnsureRestartSuite(t *testing.T) {
	suite.Run(t, new(EnsureRestartSuite))
}

func (s *EnsureRestartSuite) SetupTest() {
	s.cacheDir, s.in = stampFixture(s.T())
	ensure(s.T(), s.cacheDir, stampsAll("v1"), AllClasses, s.in)
	s.store = NewStore(s.cacheDir, false, 0, nil, nil)
	s.gathered = 0
}

// ensureSessionStale moves the session stamp, so the overlay is rebuilt over
// the stored classes. moveStore runs once Ensure holds its manifest.
func (s *EnsureRestartSuite) ensureSessionStale(moveStore func()) error {
	stamps := stampsAll("v1")
	stamps[ClassSession] = "v2session"
	want := append([]ShardClass{ClassSession}, DefaultClasses...)
	_, err := Ensure(context.Background(), s.cacheDir, BuildOptions{Stamps: stamps}, want,
		func([]ShardClass) (Inputs, error) {
			s.gathered++
			moveStore()
			return s.in, nil
		}, nil)
	return err
}

func (s *EnsureRestartSuite) TestRestartsOnceFromAFreshManifest() {
	moved := Shard{
		Name:  coverageShardName,
		Nodes: []types.KnowledgeNode{{ID: "file:moved.go", Kind: types.KindFile, Label: "moved.go"}},
	}

	err := s.ensureSessionStale(func() {
		fps := map[string]string{moved.Name: fingerprintShardContent(moved)}
		s.Require().NoError(s.store.syncClasses(context.Background(), []Shard{moved}, fps, syncPlan{}))
		s.Require().NoError(os.RemoveAll(s.store.pathIDsPath()))
	})

	s.Require().NoError(err)
	s.Equal(2, s.gathered, "the restart gathers again")
}

func (s *EnsureRestartSuite) TestFailsWhenTheStoreStillDisagreesOnTheSecondPass() {
	err := s.ensureSessionStale(func() {
		bad := Shard{Name: coverageShardName}
		s.Require().NoError(s.store.writeShard(context.Background(), bad, "feedfacefeedface"))
		s.Require().NoError(os.RemoveAll(s.store.pathIDsPath()))
	})

	s.Require().ErrorIs(err, errShardMoved)
	s.Contains(err.Error(), "holds feedfacefeed")
	s.Equal(2, s.gathered, "one restart, no more")
}
