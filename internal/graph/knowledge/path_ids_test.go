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

func (s *StoredPathIDsSuite) TestRereadsAManifestThatMovedUnderIt() {
	s.sync(pathIDShard("file:old.go"))
	held := s.store.readManifestOrNil()
	s.sync(pathIDShard("file:new.go"))

	got, err := s.store.storedPathIDs(context.Background(), held, nil)

	s.Require().NoError(err)
	s.Equal(map[string]bool{"file:new.go": true}, got, "the answer is the new build alone")
}

func (s *StoredPathIDsSuite) TestFailsWhenTheStoreKeepsMoving() {
	s.sync(pathIDShard("file:old.go"))
	held := s.store.readManifestOrNil()
	s.sync(pathIDShard("file:new.go"))
	s.Require().NoError(s.store.writeShard(context.Background(), pathIDShard("file:other.go"), "feedfacefeedface"))
	s.Require().NoError(os.Remove(s.store.pathIDsPath()))

	_, err := s.store.storedPathIDs(context.Background(), held, nil)

	s.Require().ErrorIs(err, errShardMoved)
	s.Contains(err.Error(), "holds fingerprint feedfacefeed")
}

func (s *StoredPathIDsSuite) TestAnswersFromAnUnmovedManifest() {
	s.sync(pathIDShard("file:a.go"))

	got, err := s.store.storedPathIDs(context.Background(), s.store.readManifestOrNil(), nil)

	s.Require().NoError(err)
	s.Equal(map[string]bool{"file:a.go": true}, got)
}
