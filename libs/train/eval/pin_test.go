package eval

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type pinSuite struct{ suite.Suite }

func TestPinSuite(t *testing.T) { suite.Run(t, new(pinSuite)) }

func (s *pinSuite) TestPinReplacesByNameAndSorts() {
	path := filepath.Join(s.T().TempDir(), "gen", "split.digest")
	s.Require().NoError(Pin(path, Digest{Name: "b", SHA256: "1", Rows: 1, Rev: "r"}))
	s.Require().NoError(Pin(path, Digest{Name: "a", SHA256: "2", Rows: 2, Rev: "r"}))
	s.Require().NoError(Pin(path, Digest{Name: "b", SHA256: "3", Rows: 3, Rev: "r"}))
	got, err := ReadDigests(path)
	s.Require().NoError(err)
	s.Equal([]Digest{{"a", "2", 2, "r"}, {"b", "3", 3, "r"}}, got)
}
