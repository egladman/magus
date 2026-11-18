package stream

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

type WriterSuite struct{ suite.Suite }

func TestWriter(t *testing.T) { suite.Run(t, new(WriterSuite)) }

func rec[B journal.Body](ms int64, body B) journal.Record {
	return journal.NewRecord(time.UnixMilli(ms), "inv1", body)
}

// mapped is fromJournal over in, which must map.
func (s *WriterSuite) mapped(in journal.Record) Event {
	got, ok := fromJournal("/repo", in)
	s.Require().True(ok)
	return got
}

func (s *WriterSuite) TestStartedCarriesTheCommandLineage() {
	got := s.mapped(rec(100, journal.Started{
		Version: "0.5.0",
		Command: journal.Command{Arguments: []string{"run", "build"}, Trigger: journal.TriggerRun},
	}))
	s.Equal(Event{Ts: 100, Workspace: "/repo", Inv: "inv1", Body: Run{
		Phase: "started", Command: []string{"run", "build"}, Trigger: "run", MagusVersion: "0.5.0",
	}}, got)
}

func (s *WriterSuite) TestFinishedCarriesTheOutcome() {
	got := s.mapped(rec(200, journal.Finished{Status: journal.StatusFail}))
	s.Equal(Event{Ts: 200, Workspace: "/repo", Inv: "inv1", Body: Run{Phase: "finished", Status: "fail"}}, got)
}

func (s *WriterSuite) TestResultCarriesTheFetchableRef() {
	got := s.mapped(rec(150, journal.Run{
		Project: "api", Target: "test",
		Status: journal.StatusPass, Ref: "out_abc", Duration: 1200 * time.Millisecond,
	}))
	s.Equal(Event{Ts: 150, Workspace: "/repo", Inv: "inv1", Body: Target{
		Project: "api", Target: "test", Status: "ok", CacheHit: false, Ref: "out_abc", DurationMs: 1200,
	}}, got)
}

func (s *WriterSuite) TestAPermittedResultNeverReadsAsOK() {
	got := s.mapped(rec(155, journal.Run{
		Project: "proto", Target: "ci",
		Status: journal.StatusPermitted, Ref: "out_abc", Duration: 1200 * time.Millisecond,
	}))
	s.Equal(Event{Ts: 155, Workspace: "/repo", Inv: "inv1", Body: Target{
		Project: "proto", Target: "ci", Status: "permitted", CacheHit: false, Ref: "out_abc", DurationMs: 1200,
	}}, got)
}

func (s *WriterSuite) TestOutputCarriesTheStreamItCameFrom() {
	got := s.mapped(rec(160, journal.Output{Project: "api", Target: "test", Stream: journal.Stderr, Text: "FAIL"}))
	s.Equal(Event{Ts: 160, Workspace: "/repo", Inv: "inv1", Body: Output{
		Project: "api", Target: "test", Stream: "stderr", Text: "FAIL",
	}}, got)
}

// TestSplitsCachedFromSucceeded is the point of the adapter: the journal
// conflates "succeeded" and "ran" into one three-valued status, and a
// subscriber asks those separately.
func (s *WriterSuite) TestSplitsCachedFromSucceeded() {
	got := s.mapped(rec(1, journal.Run{Status: journal.StatusCached}))
	s.Equal(Target{Status: "ok", CacheHit: true}, got.Body)
}

// TestAnUnknownStatusIsNotGreen: a status magus grows later must not
// present as a silent pass to every existing subscriber.
func (s *WriterSuite) TestAnUnknownStatusIsNotGreen() {
	got := s.mapped(rec(1, journal.Run{Status: "quarantined"}))
	s.Equal(Target{Status: "failed"}, got.Body)
}

// TestSkipsKindsWithNoStreamEquivalent: skipping is the designed
// behavior, so a kind gaining a mapping by accident should fail here.
func (s *WriterSuite) TestSkipsKindsWithNoStreamEquivalent() {
	var unknown journal.Record
	s.Require().NoError(codec.Unmarshal([]byte(`{"ts":1,"kind":"warn","text":"x"}`), &unknown))
	for name, r := range map[string]journal.Record{
		"exec":   rec(1, journal.Exec{Command: "x"}),
		"scope":  rec(1, journal.Scope{}),
		"secret": rec(1, journal.Secret{Text: "x"}),
		"warn":   unknown,
	} {
		_, ok := fromJournal("/repo", r)
		s.False(ok, name)
	}
}

func (s *WriterSuite) TestCarriesTheFailureReason() {
	got := s.mapped(rec(1, journal.Run{
		Project: "api", Target: "build", Status: journal.StatusFail, Error: "exit status 2",
	}))
	s.Equal(Target{Project: "api", Target: "build", Status: "failed", Error: "exit status 2"}, got.Body)
}

func (s *WriterSuite) TestEmitsOneLinePerEvent() {
	var buf bytes.Buffer
	w := NewWriter(&buf, Filter{})
	s.Require().NoError(w.Write(Event{Ts: 1, Body: Run{Phase: "started"}}))
	s.Require().NoError(w.Write(Event{Ts: 2, Body: Run{Phase: "finished", Status: "pass"}}))
	s.Require().NoError(w.Close())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	s.Require().Len(lines, 2)
	for _, line := range lines {
		_, err := decode([]byte(line))
		s.Require().NoError(err, "line %q", line)
	}
}

// TestDropsFilteredTypes: the default filter excludes target.output, and
// the Writer is where that promise is kept for every transport.
func (s *WriterSuite) TestDropsFilteredTypes() {
	var buf bytes.Buffer
	w := NewWriter(&buf, Filter{})
	s.Require().NoError(w.Write(Event{Ts: 1, Body: Output{Stream: "stdout", Text: "noisy"}}))
	s.Require().NoError(w.Close())
	s.Empty(buf.String())
}

// TestIsConcurrencySafe: a run fans out one goroutine per project, so an
// unsynchronized write would interleave two objects on one line and every line
// after it would fail to parse.
func (s *WriterSuite) TestIsConcurrencySafe() {
	var buf bytes.Buffer
	w := NewWriter(&buf, Filter{})

	const writers, each = 8, 50
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range each {
				_ = w.Write(Event{Ts: 1, Body: Target{Project: "p", Target: "build", Status: "ok"}})
			}
		})
	}
	wg.Wait()
	s.Require().NoError(w.Close())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	s.Require().Len(lines, writers*each)
	for _, line := range lines {
		_, err := decode([]byte(line))
		s.Require().NoError(err, "interleaved line %q", line)
	}
}
