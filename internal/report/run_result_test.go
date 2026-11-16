package report

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

func TestResultRefsLine(t *testing.T) {
	cases := []struct {
		name    string
		events  []journal.Event
		wantOut string
	}{
		{"no results prints nothing", nil, ""},
		{
			"one result prints its ref",
			[]journal.Event{{Kind: journal.KindResult, Ref: "out1a2b3c4d5e6f"}},
			"ref  out1a2b3c4d5e6f",
		},
		{
			"several results print one line",
			[]journal.Event{
				{Kind: journal.KindResult, Ref: "out111111111111"},
				{Kind: journal.KindResult, Ref: "out222222222222"},
				{Kind: journal.KindResult, Ref: "out111111111111"},
			},
			"refs  out111111111111 out222222222222",
		},
		{
			"events that are not results or carry no ref are skipped",
			[]journal.Event{
				{Kind: journal.KindOutput, Ref: "out333333333333"},
				{Kind: journal.KindResult},
			},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := &ResultRefs{}
			ctx := journal.WithLogger(context.Background(), journal.NewLogger(refs))
			for _, e := range tc.events {
				journal.Emit(ctx, e)
			}

			assert.Equal(t, tc.wantOut, RefLine(refs.List()))
		})
	}
}

type ResultStatusesSuite struct{ suite.Suite }

func TestResultStatusesSuite(t *testing.T) { suite.Run(t, new(ResultStatusesSuite)) }

func (s *ResultStatusesSuite) record(target string, events ...journal.Event) *ResultStatuses {
	statuses := NewResultStatuses(target)
	ctx := journal.WithLogger(context.Background(), journal.NewLogger(statuses))
	for _, e := range events {
		journal.Emit(ctx, e)
	}
	return statuses
}

func (s *ResultStatusesSuite) TestKeepsEachProjectsStatus() {
	statuses := s.record("ci",
		journal.Event{Kind: journal.KindResult, Project: "proto", Target: "ci", Status: journal.StatusPermitted},
		journal.Event{Kind: journal.KindResult, Project: "console", Target: "ci", Status: journal.StatusCached},
	)
	s.Equal(journal.StatusPermitted, statuses.Of("proto"))
	s.Equal(journal.StatusCached, statuses.Of("console"))
}

func (s *ResultStatusesSuite) TestMatchesACharmedStep() {
	statuses := s.record("bundle", journal.Event{Kind: journal.KindResult, Project: ".", Target: "bundle:rw", Status: journal.StatusPass})
	s.Equal(journal.StatusPass, statuses.Of("."))
}

func (s *ResultStatusesSuite) TestLeavesOutADependencysResult() {
	statuses := s.record("ci", journal.Event{Kind: journal.KindResult, Project: "proto", Target: "lint", Status: journal.StatusFail})
	s.Empty(statuses.Of("proto"))
}

func (s *ResultStatusesSuite) TestSkipsEventsThatAreNotResults() {
	statuses := s.record("ci", journal.Event{Kind: journal.KindOutput, Project: "proto", Target: "ci", Status: journal.StatusPass})
	s.Empty(statuses.Of("proto"))
}

func (s *ResultStatusesSuite) TestANilCollectorReportsNothing() {
	var statuses *ResultStatuses
	s.Empty(statuses.Of("proto"))
}
