package eval

import (
	"os"
	"path/filepath"
	"strings"
)

func (s *evalSuite) TestDevTailStartReservesTheLastFifthOfTheLines() {
	text := strings.Repeat("line\n", 10)
	s.Equal(len("line\n")*8, DevTailStart(text))
	s.Equal(0, DevTailStart(""))
}

func (s *evalSuite) TestReadSplitRefusesADevTestFile() {
	path := filepath.Join(s.T().TempDir(), "split.jsonl")
	s.Require().NoError(os.WriteFile(path, []byte(`{"path":"a.go","language":"go","bytes":1,"group":"g","side":"test","dev":true}`+"\n"), 0o644))
	_, err := ReadSplit(path)
	s.Error(err)
}
