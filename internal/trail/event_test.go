package trail

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	trailtypes "github.com/egladman/magus/libs/safetyscissors/trail"
)

type StoredEventSuite struct {
	suite.Suite
	base string
}

func TestStoredEventSuite(t *testing.T) { suite.Run(t, new(StoredEventSuite)) }

func (s *StoredEventSuite) SetupTest() { s.base = s.T().TempDir() }

func (s *StoredEventSuite) plant(lines ...string) {
	path := eventsPath(s.base)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	var body []byte
	for _, l := range lines {
		body = append(append(body, l...), '\n')
	}
	s.Require().NoError(os.WriteFile(path, body, 0o600))
}

func (s *StoredEventSuite) TestAnEventWrittenAsDurMsKeepsItsDuration() {
	var e storedEvent
	s.Require().NoError(codec.Unmarshal([]byte(`{"ts":1,"kind":"job","action":"a","outcome":"ok","dur_ms":42}`), &e))
	s.Equal(int64(42), e.DurationMs)
}

func (s *StoredEventSuite) TestTheCurrentSpellingWins() {
	var e storedEvent
	s.Require().NoError(codec.Unmarshal([]byte(`{"ts":1,"kind":"job","action":"a","outcome":"ok","duration_ms":7,"dur_ms":42}`), &e))
	s.Equal(int64(7), e.DurationMs)
}

// The scheduler reads a job's last finish as Ts plus DurationMs, so an older row must keep it.
func (s *StoredEventSuite) TestLastRunReadsAnOlderDuration() {
	s.plant(`{"ts":100,"kind":"job","action":"graph build","outcome":"ok","dur_ms":42}`)

	got, ok := LastRun(s.T().Context(), s.base, "graph build")

	s.Require().True(ok)
	s.Equal(int64(42), got.DurationMs)
}

func (s *StoredEventSuite) TestReadRecentKeepsTheTailNewestFirst() {
	s.plant(
		`{"ts":1,"kind":"job","action":"a","outcome":"ok"}`,
		`not json`,
		`{"ts":2,"kind":"job","action":"b","outcome":"ok"}`,
		`{"ts":3,"kind":"job","action":"c","outcome":"ok","dur_ms":5}`,
	)

	got, err := readRecent(s.T().Context(), s.base, 3)

	s.Require().NoError(err)
	s.Require().Len(got, 2, "a corrupt line in the window is skipped")
	s.Equal([]string{"c", "b"}, []string{got[0].Action, got[1].Action})
	s.Equal(int64(5), got[0].DurationMs)
}

func (s *StoredEventSuite) TestReadRecentOfAMissingTrailIsEmpty() {
	got, err := readRecent(s.T().Context(), s.base, 10)

	s.Require().NoError(err)
	s.Empty(got)
}

func (s *StoredEventSuite) TestReadRecentRefusesNoBaseOrNoLimit() {
	var invalid *trailtypes.InvalidReadError
	_, err := readRecent(s.T().Context(), "", 10)
	s.True(errors.As(err, &invalid))
	_, err = readRecent(s.T().Context(), s.base, 0)
	s.True(errors.As(err, &invalid))
}
