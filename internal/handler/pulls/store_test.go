package pulls

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type AuthorsSuite struct {
	suite.Suite
	path  string
	store Store
}

func TestAuthorsSuite(t *testing.T) { suite.Run(t, new(AuthorsSuite)) }

func (s *AuthorsSuite) SetupTest() {
	s.path = filepath.Join(s.T().TempDir(), "pulls", "authors.jsonl")
	s.store = OpenStore(s.path)
}

func (s *AuthorsSuite) TestAbsentFileIsTheViewerByDefault() {
	f, err := s.store.Filter()
	s.Require().NoError(err)

	s.False(f.Saved)
	s.Equal([]string{"ada"}, f.Effective("ada"))
	s.True(f.proto("ada").IsDefault)
	s.False(f.proto("ada").EveryAuthor)
}

func (s *AuthorsSuite) TestExplicitAuthorsReplaceTheDefault() {
	s.Require().NoError(s.store.Set(s.T().Context(), []string{"grace", "@bob", " grace "}))

	f, err := s.store.Filter()
	s.Require().NoError(err)
	s.True(f.Saved)
	s.Equal([]string{"grace", "bob"}, f.Effective("ada"))
	s.False(f.proto("ada").IsDefault)
}

func (s *AuthorsSuite) TestPresentFileWithNoLiveEntryIsEveryAuthor() {
	s.Require().NoError(s.store.Set(s.T().Context(), nil))

	f, err := s.store.Filter()
	s.Require().NoError(err)
	s.True(f.Saved)
	s.Nil(f.Effective("ada"))
	s.True(f.proto("ada").EveryAuthor)
	s.FileExists(s.path)
}

func (s *AuthorsSuite) TestClearingAfterTrackingEndsEveryLogin() {
	ctx := s.T().Context()
	s.Require().NoError(s.store.Set(ctx, []string{"grace", "bob"}))
	s.Require().NoError(s.store.Set(ctx, nil))

	f, err := s.store.Filter()
	s.Require().NoError(err)
	s.True(f.proto("ada").EveryAuthor)
}

func (s *AuthorsSuite) TestSettingTheSameAuthorsAgainAppendsNothing() {
	ctx := s.T().Context()
	s.Require().NoError(s.store.Set(ctx, []string{"grace"}))
	before, err := os.ReadFile(s.path)
	s.Require().NoError(err)

	s.Require().NoError(s.store.Set(ctx, []string{"grace"}))

	after, err := os.ReadFile(s.path)
	s.Require().NoError(err)
	s.Equal(string(before), string(after))
}

func (s *AuthorsSuite) TestDroppingOneAuthorKeepsTheOther() {
	ctx := s.T().Context()
	s.Require().NoError(s.store.Set(ctx, []string{"grace", "bob"}))
	s.Require().NoError(s.store.Set(ctx, []string{"bob"}))

	f, err := s.store.Filter()
	s.Require().NoError(err)
	s.Equal([]string{"bob"}, f.Authors)
}

func (s *AuthorsSuite) TestAnUnreadableFileIsAnError() {
	s.Require().NoError(os.MkdirAll(filepath.Dir(s.path), 0o755))
	s.Require().NoError(os.WriteFile(s.path, []byte("not json\n"), 0o644))

	_, err := s.store.Filter()

	s.Error(err)
}
