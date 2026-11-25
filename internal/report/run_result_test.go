package report

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func run(r journal.Run) journal.Record { return journal.NewRecord(time.Now(), "inv1", r) }

func TestResultRefsLine(t *testing.T) {
	cases := []struct {
		name    string
		records []journal.Record
		wantOut string
	}{
		{"no results prints nothing", nil, ""},
		{
			"one result prints its ref",
			[]journal.Record{run(journal.Run{Ref: "out1a2b3c4d5e6f"})},
			"ref  out1a2b3c4d5e6f",
		},
		{
			"several results print one line",
			[]journal.Record{
				run(journal.Run{Ref: "out111111111111"}),
				run(journal.Run{Ref: "out222222222222"}),
				run(journal.Run{Ref: "out111111111111"}),
			},
			"refs  out111111111111 out222222222222",
		},
		{
			"events that are not results or carry no ref are skipped",
			[]journal.Record{
				journal.NewRecord(time.Now(), "inv1", journal.Output{Text: "out333333333333"}),
				run(journal.Run{}),
			},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := &ResultRefs{}
			for _, r := range tc.records {
				refs.Handle(context.Background(), r)
			}

			assert.Equal(t, tc.wantOut, RefLine(refs.List()))
		})
	}
}

type ResultStatusesSuite struct{ suite.Suite }

func TestResultStatusesSuite(t *testing.T) { suite.Run(t, new(ResultStatusesSuite)) }

func (s *ResultStatusesSuite) record(target string, records ...journal.Record) *ResultStatuses {
	statuses := NewResultStatuses(target)
	for _, r := range records {
		statuses.Handle(context.Background(), r)
	}
	return statuses
}

func (s *ResultStatusesSuite) TestKeepsEachProjectsStatus() {
	statuses := s.record("ci",
		run(journal.Run{Project: "proto", Target: "ci", Status: journal.StatusPermitted}),
		run(journal.Run{Project: "console", Target: "ci", Status: journal.StatusCached}),
	)
	s.Equal(string(journal.StatusPermitted), statuses.Of("proto"))
	s.Equal(string(journal.StatusCached), statuses.Of("console"))
}

func (s *ResultStatusesSuite) TestMatchesACharmedStep() {
	statuses := s.record("bundle", run(journal.Run{Project: ".", Target: "bundle:rw", Status: journal.StatusPass}))
	s.Equal(string(journal.StatusPass), statuses.Of("."))
}

func (s *ResultStatusesSuite) TestLeavesOutADependencysResult() {
	statuses := s.record("ci", run(journal.Run{Project: "proto", Target: "lint", Status: journal.StatusFail}))
	s.Empty(statuses.Of("proto"))
}

func (s *ResultStatusesSuite) TestSkipsEventsThatAreNotResults() {
	statuses := s.record("ci", journal.NewRecord(time.Now(), "inv1", journal.Output{Project: "proto", Target: "ci"}))
	s.Empty(statuses.Of("proto"))
}

func (s *ResultStatusesSuite) TestANilCollectorReportsNothing() {
	var statuses *ResultStatuses
	s.Empty(statuses.Of("proto"))
}
