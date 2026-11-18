package types

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

type JobViewSuite struct{ suite.Suite }

func TestJobView(t *testing.T) { suite.Run(t, new(JobViewSuite)) }

const (
	viewCheck     = `{"target":"test","project":"."}`
	viewGoalCheck = `{"target":"lint","project":"libs/x","no_default_charms":true}`
)

func (s *JobViewSuite) row() job.Job {
	return job.Job{
		ID: "w", State: job.StateRunning, Paths: job.Paths{Write: []string{"a.go"}},
		Check: codec.RawMessage(viewCheck),
		Goals: []job.Goal{
			{ID: "lint", Kind: job.GoalKindCheck, Expect: job.ExpectPassed, Check: codec.RawMessage(viewGoalCheck), Accept: []job.Basis{job.BasisWitnessed}},
			{ID: "names", Kind: job.GoalKindSymbol, Expect: job.ExpectPresent, Symbols: []string{"X"}},
		},
	}
}

// The view's members hide the row's, so its JSON is the row's JSON exactly: a reader of
// either sees one format.
func (s *JobViewSuite) TestTheViewEncodesAsTheRowDoes() {
	row := s.row()
	view, err := NewJob(row)
	s.Require().NoError(err)

	want, err := codec.Marshal(row)
	s.Require().NoError(err)
	got, err := codec.Marshal(view)
	s.Require().NoError(err)
	s.JSONEq(string(want), string(got))
}

func (s *JobViewSuite) TestTheViewReadsEachCheck() {
	view, err := NewJob(s.row())
	s.Require().NoError(err)

	s.Equal(&LeaseCheck{Target: "test", Project: "."}, view.Check)
	s.Equal(LeaseCheck{Target: "lint", Project: "libs/x", NoDefaultCharms: true}, view.Goals[0].Check)
	s.Equal(LeaseCheck{}, view.Goals[1].Check)
	s.Nil(view.Job.Check, "the row's own member is hidden, never read twice")
}

func (s *JobViewSuite) TestRowGivesTheRowBack() {
	row := s.row()
	view, err := NewJob(row)
	s.Require().NoError(err)

	back, err := view.Row()

	s.Require().NoError(err)
	s.JSONEq(viewCheck, string(back.Check))
	s.JSONEq(viewGoalCheck, string(back.Goals[0].Check))
	s.Nil(back.Goals[1].Check, "a goal with no check stays one")
	back.Check, back.Goals[0].Check = row.Check, row.Goals[0].Check
	s.Equal(row, back)
}

func (s *JobViewSuite) TestACheckThatDoesNotReadIsRefusedNamingWhereItSits() {
	row := s.row()
	row.Goals[0].Check = codec.RawMessage(`{"target":"lint","noDefaultCharms":true}`)

	_, err := NewJob(row)

	var decode *CheckDecodeError
	s.Require().ErrorAs(err, &decode)
	s.Equal("w", decode.Job)
	s.Equal("goals[lint].check", decode.Field)
}

func (s *JobViewSuite) TestARowWithNoCheckHasANilOne() {
	view, err := NewJob(job.Job{ID: "w", Check: codec.RawMessage("null")})
	s.Require().NoError(err)
	s.Nil(view.Check)
	back, err := view.Row()
	s.Require().NoError(err)
	s.Nil(back.Check)
}

func (s *JobViewSuite) TestAListReadsEveryRow() {
	list, err := NewJobList(job.List{Jobs: []job.Job{s.row()}, Stale: []string{"w"}})
	s.Require().NoError(err)

	s.Require().Len(list.Jobs, 1)
	s.Equal("test", list.Jobs[0].Check.Target)
	s.Equal([]string{"w"}, list.Stale)
	s.Nil(list.List.Jobs)
}

// yaml cannot inline the row beside the members that hide its own, so a row prints
// through its JSON: the check is an object, never the bytes of one.
func (s *JobViewSuite) TestYAMLPrintsTheCheckAsAnObject() {
	view, err := NewJob(s.row())
	s.Require().NoError(err)
	var buf bytes.Buffer
	s.Require().NoError(yaml.NewEncoder(&buf).Encode(view))

	var back map[string]any
	s.Require().NoError(yaml.Unmarshal(buf.Bytes(), &back))
	s.Equal(map[string]any{"target": "test", "project": "."}, back["check"])
	s.Equal("w", back["id"])
}

func (s *JobViewSuite) TestTheDeclarationReadsAndWritesAsTheModulesDoes() {
	text := `{"api_version":"safetyscissors.job/v1alpha1","id":"w","check":{"target":"test","project":"."},` +
		`"goals":[{"id":"lint","kind":"check","check":{"target":"lint"},"accept":["witnessed"]}],"later":true}`
	var view Declaration
	s.Require().NoError(codec.Unmarshal([]byte(text), &view))

	s.Equal(&LeaseCheck{Target: "test", Project: "."}, view.Check)
	s.Equal("lint", view.Goals[0].Check.Target)
	s.Contains(view.Unknown, "later", "a member the declaration does not declare is kept")

	var module job.Declaration
	s.Require().NoError(codec.Unmarshal([]byte(text), &module))
	want, err := codec.Marshal(module)
	s.Require().NoError(err)
	got, err := codec.Marshal(view)
	s.Require().NoError(err)
	s.JSONEq(string(want), string(got))
}

func (s *JobViewSuite) TestTheDeclarationRefusesACheckThatDoesNotRead() {
	var view Declaration
	err := codec.Unmarshal([]byte(`{"id":"w","check":{"target":"test","bogus":1}}`), &view)

	var decode *CheckDecodeError
	s.Require().ErrorAs(err, &decode)
	s.Equal("check", decode.Field)
}

func (s *JobViewSuite) TestAGoalWithNoCheckIsAModuleGoalWithNone() {
	g, err := Goal{Goal: job.Goal{ID: "names", Kind: job.GoalKindSymbol}}.Module()
	s.Require().NoError(err)
	s.Nil(g.Check)
}
