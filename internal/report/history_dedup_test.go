package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/ci/forecast"
)

type historyDedupSuite struct {
	suite.Suite
	dir string
}

func TestHistoryDedupSuite(t *testing.T) {
	suite.Run(t, new(historyDedupSuite))
}

func (s *historyDedupSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

func (s *historyDedupSuite) write(name, body string) string {
	path := filepath.Join(s.dir, name)
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
	return path
}

var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

const (
	missLine   = `{"type":"run.target.result","project":"api","target":"test","hash":"h1","status":"ok","duration_ms":400}` + "\n"
	hitLine    = `{"type":"run.target.result","project":"api","target":"lint","hash":"h2","status":"ok","cache_hit":true,"duration_ms":9}` + "\n"
	failedLine = `{"type":"run.target.result","project":"api","target":"vet","hash":"h3","status":"failed","duration_ms":5}` + "\n"
	otherLine  = `{"type":"summary"}` + "\n"
)

func (s *historyDedupSuite) TestReadMissesKeepsOnlyFreshSuccessfulRuns() {
	path := s.write("a.jsonl", missLine+hitLine+failedLine+otherLine+"not json\n\n")

	got, err := readMisses(s.T().Context(), path)

	s.Require().NoError(err)
	s.Equal([]MissEvent{{Project: "api", Target: "test", Hash: "h1", DurationMs: 400, File: path}}, got)
}

func (s *historyDedupSuite) TestReadMissesOfAMissingFileIsEmpty() {
	got, err := readMisses(s.T().Context(), filepath.Join(s.dir, "absent.jsonl"))

	s.Require().NoError(err)
	s.Empty(got)
}

func (s *historyDedupSuite) TestReadMissFilesTagsEachEventWithItsShard() {
	one := s.write("shard-1.jsonl", missLine)
	two := s.write("shard-2.jsonl", missLine)

	got, err := ReadMissFiles(s.T().Context(), []string{filepath.Join(s.dir, "shard-*.jsonl")})

	s.Require().NoError(err)
	s.Require().Len(got, 2)
	s.Equal([]string{one, two}, []string{got[0].File, got[1].File})
}

func (s *historyDedupSuite) TestReadMissFilesRefusesABadGlob() {
	_, err := ReadMissFiles(s.T().Context(), []string{"["})

	s.ErrorContains(err, `bad glob "["`)
}

func (s *historyDedupSuite) TestImportHistoriesMergesEveryMatch() {
	dest := filepath.Join(s.dir, "history.jsonl")
	for _, name := range []string{"one.jsonl", "two.jsonl"} {
		path := filepath.Join(s.dir, name)
		err := forecast.UpdateHistory(s.T().Context(), path, func(h *forecast.History) error {
			h.RecordRun(forecast.Run{Commit: name, Ref: "main", Target: "ci", Status: forecast.RunPassed}, testNow)
			return nil
		})
		s.Require().NoError(err)
	}

	files, _, err := ImportHistories(s.T().Context(), dest, []string{filepath.Join(s.dir, "*.jsonl")})

	s.Require().NoError(err)
	s.Equal(2, files)
}

func TestRecordPassedWritesTheRun(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "history.jsonl")
	run := forecast.Run{Commit: "abc", Ref: "main", Target: "ci", Status: forecast.RunPassed}

	require.NoError(t, RecordPassed(t.Context(), dest, run, testNow))

	var h forecast.History
	require.NoError(t, h.Load(t.Context(), dest))
	assert.NotEmpty(t, h.Runs)
}
