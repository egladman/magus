package trail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

type ReadSuite struct {
	suite.Suite
	dir string
}

func TestRead(t *testing.T) { suite.Run(t, new(ReadSuite)) }

func (s *ReadSuite) SetupTest() { s.dir = s.T().TempDir() }

// writeLines lays down the events file as a producer would have left it.
func (s *ReadSuite) writeLines(lines ...string) {
	path := eventsPath(s.dir)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	s.Require().NoError(os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
}

func (s *ReadSuite) eventLine(e Event) string {
	b, err := codec.Marshal(e)
	s.Require().NoError(err)
	return string(b)
}

func (s *ReadSuite) TestReadRecentIsNewestFirst() {
	s.writeLines(
		s.eventLine(Event{Ts: 1, Kind: KindJob, Action: "one", Outcome: OutcomeOK}),
		s.eventLine(Event{Ts: 2, Kind: KindJob, Action: "two", Outcome: OutcomeOK}),
		s.eventLine(Event{Ts: 3, Kind: KindJob, Action: "three", Outcome: OutcomeOK}),
	)
	got, err := ReadRecent(s.T().Context(), s.dir, 10)
	s.Require().NoError(err)
	s.Require().Len(got, 3)
	s.Equal([]string{"three", "two", "one"}, []string{got[0].Action, got[1].Action, got[2].Action})
}

func (s *ReadSuite) TestReadRecentLimitKeepsTheTail() {
	lines := make([]string, 0, 10)
	for i := 1; i <= 10; i++ {
		lines = append(lines, s.eventLine(Event{Ts: int64(i), Kind: KindJob, Action: fmt.Sprint(i)}))
	}
	s.writeLines(lines...)
	got, err := ReadRecent(s.T().Context(), s.dir, 3)
	s.Require().NoError(err)
	s.Require().Len(got, 3)
	s.Equal([]int64{10, 9, 8}, []int64{got[0].Ts, got[1].Ts, got[2].Ts})
}

func (s *ReadSuite) TestReadRecentOfAMissingTrailIsEmptyAndNoError() {
	got, err := ReadRecent(s.T().Context(), s.dir, 10)
	s.Require().NoError(err)
	s.Empty(got, "a missing trail")

	s.NotNil(got, "a missing trail is empty, not nil")
}

func (s *ReadSuite) TestReadRecentRefusesNoDirAndNoLimit() {
	var invalid *InvalidReadError
	_, err := ReadRecent(s.T().Context(), "", 10)
	s.Require().ErrorAs(err, &invalid, "an empty dir")

	s.writeLines(s.eventLine(Event{Ts: 1, Kind: KindJob}))
	_, err = ReadRecent(s.T().Context(), s.dir, 0)
	s.Require().ErrorAs(err, &invalid, "a zero limit")
	s.Equal(0, invalid.Limit)
}

func (s *ReadSuite) TestReadRecentSkipsACorruptLine() {
	s.writeLines(
		s.eventLine(Event{Ts: 1, Kind: KindJob, Action: "good-one"}),
		"this is not json",
		s.eventLine(Event{Ts: 2, Kind: KindJob, Action: "good-two"}),
	)
	got, err := ReadRecent(s.T().Context(), s.dir, 10)
	s.Require().NoError(err)
	s.Require().Len(got, 2)
	s.Equal("good-two", got[0].Action)
	s.Equal("good-one", got[1].Action)
}

func (s *ReadSuite) TestEventReadsTheOriginInline() {
	s.writeLines(`{"ts":1,"kind":"job","user":"eli","host":"claude","action":"a","outcome":"ok"}`)
	got, err := ReadRecent(s.T().Context(), s.dir, 1)
	s.Require().NoError(err)
	s.Require().Len(got, 1)
	s.Equal(job.Origin{User: "eli", Host: "claude"}, got[0].Origin)
}

func (s *ReadSuite) TestReadBlobReturnsTheStoredPayload() {
	ref := "mcp0123456789abcdef"
	path := filepath.Join(blobsPath(s.dir), ref)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	s.Require().NoError(os.WriteFile(path, []byte("payload"), 0o600))

	got, err := ReadBlob(s.dir, ref)
	s.Require().NoError(err)
	s.Equal("payload", string(got))
}

func (s *ReadSuite) TestReadBlobRefusesAnUnsafeRef() {
	for _, bad := range []string{"", "..", "mcp/../x", "mcpZZZZZZZZZZZZZZZZ", "mcp0123", "MCP0123456789abcd"} {
		_, err := ReadBlob(s.dir, bad)
		var invalid *InvalidRefError
		s.Require().ErrorAs(err, &invalid, "ReadBlob(%q)", bad)
		s.Equal(bad, invalid.Ref)
	}
	_, err := os.Stat(filepath.Join(s.dir, activityDir))
	s.ErrorIs(err, os.ErrNotExist, "a refused read creates nothing")
}

func (s *ReadSuite) TestValidRefSplitsTheHashFromTheEnd() {
	s.True(ValidRef("mcp0123456789abcdef"))
	s.True(ValidRef("agentabcdefabcdefabcd"), "hex letters after a letter prefix")
	s.True(ValidRef("ab0123456789abcdef"), "a two-letter prefix")
	s.False(ValidRef("a0123456789abcdef"), "a one-letter prefix")
	s.False(ValidRef("abcdefghi0123456789abcdef"), "a nine-letter prefix")
	s.False(ValidRef("mcp0123456789abcdeF"), "an upper-case hex digit")
}
