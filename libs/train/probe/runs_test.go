package probe

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

// tempDir is the base of every suite in this package: a fresh directory per test.
type tempDir struct {
	suite.Suite
	dir string
}

func (s *tempDir) SetupTest() { s.dir = s.T().TempDir() }

func (s *tempDir) write(name, body string) string {
	path := filepath.Join(s.dir, name)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
	return path
}

func sum(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

type RunsSuite struct{ tempDir }

func TestRuns(t *testing.T) { suite.Run(t, new(RunsSuite)) }

func (s *RunsSuite) TestReadInputsRefusesAnAbsolutePath() {
	path := s.write("inputs.yaml", "- name: m/a\n  source: https://x\n  sha256: \""+sum("")+"\"\n  path: /abs\n")
	_, err := ReadInputs(path)
	s.ErrorContains(err, "relative path")
}

func (s *RunsSuite) TestWithPrefixNamesAPrefixNothingMatches() {
	_, err := WithPrefix([]Input{{Name: "a/b"}}, "c/")
	s.ErrorContains(err, "c/*")
}

func (s *RunsSuite) TestPresentRefusesAWrongSize() {
	s.write("cache/m/a", "abc")
	_, err := Present(filepath.Join(s.dir, "cache"), []Input{{Name: "m/a", Path: "m/a", Size: 4, SHA256: sum("abcd")}})
	s.ErrorContains(err, "is 3 bytes, inputs.yaml pins 4")
}

func (s *RunsSuite) TestPresentReturnsThePinnedSums() {
	s.write("cache/m/a", "abc")
	got, err := Present(filepath.Join(s.dir, "cache"), []Input{{Name: "m/a", Path: "m/a", Size: 3, SHA256: sum("abc")}})
	s.Require().NoError(err)
	s.Equal(map[string]string{"m/a": sum("abc")}, got)
}

func (s *RunsSuite) TestAppendAddsOneLinePerRow() {
	path := filepath.Join(s.dir, "runs.jsonl")
	s.Require().NoError(Append(path, map[string]int{"a": 1}))
	s.Require().NoError(Append(path, map[string]int{"a": 2}))
	b, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal("{\"a\":1}\n{\"a\":2}\n", string(b))
}

func (s *RunsSuite) TestParseLoadavgReadsTheOneMinuteAverage() {
	got, err := ParseLoadavg("{ 11.67 19.64 24.66 }\n")
	s.Require().NoError(err)
	s.InDelta(11.67, got, 1e-9)
}

func (s *RunsSuite) TestParseLoadavgRefusesOtherOutput() {
	_, err := ParseLoadavg("unknown oid")
	s.ErrorContains(err, "three load averages")
}
