package journal

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type RecordsSuite struct {
	suite.Suite
	dir string
}

func TestRecords(t *testing.T) { suite.Run(t, new(RecordsSuite)) }

func (s *RecordsSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *RecordsSuite) line(r Record) string {
	b, err := codec.Marshal(r)
	s.Require().NoError(err)
	return string(b)
}

func (s *RecordsSuite) write(id, content string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, id+journalExt), []byte(content), 0o600))
}

func (s *RecordsSuite) output(ms int64, text string) Record {
	return NewRecord(time.UnixMilli(ms), "inv1", Output{Project: "p", Target: "t", Stream: Stdout, Text: text})
}

// collect reads every pair, splitting the records from the errors.
func (s *RecordsSuite) collect(seq func(func(Record, error) bool)) ([]Record, []error) {
	var (
		records []Record
		errs    []error
	)
	for r, err := range seq {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		records = append(records, r)
	}
	return records, errs
}

func (s *RecordsSuite) TestABadLineIsALineErrorAndTheReadGoesOn() {
	s.write("inv1", s.line(s.output(1, "a"))+"\nnot json\n"+s.line(s.output(2, "b"))+"\n")
	records, errs := s.collect(Records(s.T().Context(), s.dir, "inv1"))
	s.Len(records, 2)
	s.Require().Len(errs, 1)
	var lineErr *codec.LineError
	s.Require().ErrorAs(errs[0], &lineErr)
	s.Equal(2, lineErr.Line)
	s.ErrorIs(errs[0], codec.ErrLineUndecodable)
}

func (s *RecordsSuite) TestALineNoNewlineClosesIsLeftOut() {
	s.write("inv1", s.line(s.output(1, "a"))+"\n"+`{"ts":2,"kind":"out`)
	records, errs := s.collect(Records(s.T().Context(), s.dir, "inv1"))
	s.Empty(errs)
	s.Len(records, 1)
}

func (s *RecordsSuite) TestARefusedIDTouchesNothing() {
	for _, id := range []string{"", "../x", "a/b", ".hidden", "inv.1"} {
		_, errs := s.collect(Records(s.T().Context(), s.dir, id))
		s.Require().Len(errs, 1, "id %q", id)
		s.ErrorIs(errs[0], fs.ErrNotExist, "id %q", id)
	}
}

func (s *RecordsSuite) TestAMissingJournalIsTheLastError() {
	records, errs := s.collect(Records(s.T().Context(), s.dir, "inv404"))
	s.Empty(records)
	s.Require().Len(errs, 1)
	s.ErrorIs(errs[0], fs.ErrNotExist)
}

func (s *RecordsSuite) TestACancelledReadEndsOnTheContextError() {
	s.write("inv1", s.line(s.output(1, "a"))+"\n")
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	_, errs := s.collect(Records(ctx, s.dir, "inv1"))
	s.Require().Len(errs, 1)
	s.ErrorIs(errs[0], context.Canceled)
}

func (s *RecordsSuite) TestRecordsSinceResumesAtNext() {
	first := s.line(s.output(1, "a")) + "\n"
	s.write("inv1", first+`{"ts":2,"ki`)
	records, next, err := RecordsSince(s.T().Context(), s.dir, "inv1", 0)
	s.Require().NoError(err)
	got, errs := s.collect(records)
	s.Empty(errs)
	s.Len(got, 1)
	s.Equal(int64(len(first)), next, "a torn line is left for the next call")

	s.write("inv1", first+s.line(s.output(2, "b"))+"\n")
	records, _, err = RecordsSince(s.T().Context(), s.dir, "inv1", next)
	s.Require().NoError(err)
	got, _ = s.collect(records)
	s.Require().Len(got, 1)
	out, _ := got[0].Output()
	s.Equal("b", out.Text)
}

func (s *RecordsSuite) TestRecordsSinceReadsAReplacedJournalFromItsStart() {
	s.write("inv1", s.line(s.output(1, "a"))+"\n")
	records, _, err := RecordsSince(s.T().Context(), s.dir, "inv1", 1<<20)
	s.Require().NoError(err)
	got, _ := s.collect(records)
	s.Len(got, 1)
}

func (s *RecordsSuite) TestReadInvocationReadsTheHeadAndTheTail() {
	started := NewRecord(time.UnixMilli(10), "inv1", Started{Command: Command{Arguments: []string{"run"}, Trigger: TriggerRun}, Version: "v1"})
	finished := NewRecord(time.UnixMilli(90), "inv1", Finished{Status: StatusFail})
	s.write("inv1", strings.Join([]string{s.line(started), s.line(s.output(50, "x")), s.line(finished)}, "\n")+"\n")
	inv, err := ReadInvocation(s.T().Context(), s.dir, "inv1")
	s.Require().NoError(err)
	s.Equal(Invocation{
		ID:       "inv1",
		Command:  Command{Arguments: []string{"run"}, Trigger: TriggerRun},
		Started:  time.UnixMilli(10),
		Finished: time.UnixMilli(90),
		Status:   StatusFail,
		Version:  "v1",
	}, inv)
}

func (s *RecordsSuite) TestAnInterruptedInvocationEndsAtItsLastRecord() {
	s.write("inv1", s.line(s.output(1, "a"))+"\n"+s.line(s.output(7, "b"))+"\n")
	inv, err := ReadInvocation(s.T().Context(), s.dir, "inv1")
	s.Require().NoError(err)
	s.True(inv.Started.IsZero(), "a first line that is not Started")
	s.Equal(time.UnixMilli(7), inv.Finished)
	s.Empty(inv.Status)
}

func (s *RecordsSuite) TestReadInvocationRefusesAnUnsafeID() {
	_, err := ReadInvocation(s.T().Context(), s.dir, "../etc/passwd")
	s.True(errors.Is(err, fs.ErrNotExist))
}

func (s *RecordsSuite) TestValidInvocationID() {
	s.True(ValidInvocationID("invmw6wj0wb1"))
	s.True(ValidInvocationID("invmw6wj0wb1-p4f2"))
	s.False(ValidInvocationID("-inv"))
	s.False(ValidInvocationID("inv/1"))
	s.False(ValidInvocationID("inv.1"))
}
