package eval

import (
	"path/filepath"
)

func (s *evalSuite) TestPinReplacesByNameAndSorts() {
	path := filepath.Join(s.T().TempDir(), "gen", "split.digest")
	s.Require().NoError(Pin(path, Digest{Name: "b", SHA256: "1", Rows: 1, Rev: "r"}))
	s.Require().NoError(Pin(path, Digest{Name: "a", SHA256: "2", Rows: 2, Rev: "r"}))
	s.Require().NoError(Pin(path, Digest{Name: "b", SHA256: "3", Rows: 3, Rev: "r"}))
	got, err := ReadDigests(path)
	s.Require().NoError(err)
	s.Equal([]Digest{{"a", "2", 2, "r"}, {"b", "3", 3, "r"}}, got)
}
