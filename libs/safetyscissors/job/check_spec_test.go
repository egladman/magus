package job

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

// specCheck is the check the tests' [WithCheckSpec] reads: a target and the project it
// runs in, "." when it names none.
type specCheck struct {
	Target  string   `json:"target"`
	Project string   `json:"project,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func readSpecCheck(raw codec.RawMessage) (specCheck, error) {
	var c specCheck
	if err := codec.UnmarshalStrict(raw, &c); err != nil {
		return specCheck{}, err
	}
	if c.Target == "" {
		return specCheck{}, errors.New("names no target")
	}
	if c.Project == "" {
		c.Project = "."
	}
	return c, nil
}

func validateSpecCheck(raw codec.RawMessage) (codec.RawMessage, error) {
	c, err := readSpecCheck(raw)
	if err != nil {
		return nil, err
	}
	return codec.Marshal(c)
}

func equalSpecCheck(a, b codec.RawMessage) (bool, error) {
	ca, err := readSpecCheck(a)
	if err != nil {
		return false, err
	}
	cb, err := readSpecCheck(b)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(ca, cb), nil
}

func renderSpecCheck(raw codec.RawMessage) (string, error) {
	c, err := readSpecCheck(raw)
	if err != nil {
		return "", err
	}
	line := c.Target + " " + c.Project
	if len(c.Args) > 0 {
		line += " -- " + strings.Join(c.Args, " ")
	}
	return line, nil
}

// testCheckSpec is the [WithCheckSpec] every test store is opened with.
func testCheckSpec() StoreOption {
	return WithCheckSpec(validateSpecCheck, equalSpecCheck, renderSpecCheck)
}

// checkJSON is a check as validateSpecCheck stores it, for names Go and JSON quote alike.
func checkJSON(target, project string, args ...string) codec.RawMessage {
	line := `{"target":` + strconv.Quote(target) + `,"project":` + strconv.Quote(project)
	if len(args) > 0 {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = strconv.Quote(a)
		}
		line += `,"args":[` + strings.Join(quoted, ",") + `]`
	}
	return codec.RawMessage(line + "}")
}

type CheckSpecSuite struct {
	suite.Suite
	dir string
}

func TestCheckSpecSuite(t *testing.T) { suite.Run(t, new(CheckSpecSuite)) }

func (s *CheckSpecSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *CheckSpecSuite) open(opts ...StoreOption) *Store {
	st, err := Open(s.dir, writable(opts...)...)
	s.Require().NoError(err)
	return st
}

func (s *CheckSpecSuite) TestOpenRefusesANilFunction() {
	for name, opt := range map[string]StoreOption{
		"validate": WithCheckSpec(nil, equalSpecCheck, renderSpecCheck),
		"equal":    WithCheckSpec(validateSpecCheck, nil, renderSpecCheck),
		"render":   WithCheckSpec(validateSpecCheck, equalSpecCheck, nil),
	} {
		_, err := Open(s.dir, opt)

		refused, ok := errors.AsType[*CheckSpecError](err)
		s.Require().True(ok, "%s: %v", name, err)
		s.Equal(name, refused.Func)
	}
}

func (s *CheckSpecSuite) TestAStoreWithNoCheckSpecRefusesARowWrite() {
	st, err := Open(s.dir, WithActor(func() Actor { return Actor{} }), WithNotices(func(Notice) {}))
	s.Require().NoError(err)

	_, err = st.Update(s.T().Context(), "a", func(*Job) {})

	refused, ok := errors.AsType[*UnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithCheckSpec", refused.Option)
}

func (s *CheckSpecSuite) TestAStoreWithNoCheckSpecRefusesToGrade() {
	st, err := Open(s.dir, WithTree(func(_ context.Context) (string, error) { return "tree", nil }))
	s.Require().NoError(err)

	_, err = st.Grade(s.T().Context(), "a", nil)

	refused, ok := errors.AsType[*GradingUnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithCheckSpec", refused.Option)
}

func (s *CheckSpecSuite) TestAWriteStoresTheBytesValidateReturns() {
	st := s.open()

	row, err := st.Update(s.T().Context(), "a", func(j *Job) {
		j.Check = codec.RawMessage(`{ "target": "test" }`)
		j.Goals = []Goal{{ID: "lint", Kind: GoalKindCheck, Check: codec.RawMessage(`{"project":"p","target":"lint"}`), Accept: []Basis{BasisWitnessed}}}
	})

	s.Require().NoError(err)
	s.Equal(`{"target":"test","project":"."}`, string(row.Check))
	s.Equal(`{"target":"lint","project":"p"}`, string(row.Goals[0].Check))
	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	s.Equal(`{"target":"test","project":"."}`, string(rows[0].Check))
}

func (s *CheckSpecSuite) TestACheckValidateRefusesIsNotWritten() {
	st := s.open()

	_, err := st.Update(s.T().Context(), "a", func(j *Job) {
		j.Goals = []Goal{{ID: "lint", Kind: GoalKindCheck, Check: codec.RawMessage(`{"target":"lint","shell":"rm"}`), Accept: []Basis{BasisWitnessed}}}
	})

	refused, ok := errors.AsType[*CheckRefusedError](err)
	s.Require().True(ok, err)
	s.Equal("a", refused.Job)
	s.Equal("goals[lint].check", refused.Field)
	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *CheckSpecSuite) TestARowCheckThatIsNullDeclaresNone() {
	st := s.open()

	row, err := st.Update(s.T().Context(), "a", func(j *Job) { j.Check = codec.RawMessage(" null ") })

	s.Require().NoError(err)
	s.Nil(row.Check)
}

func (s *CheckSpecSuite) TestHasCheck() {
	s.False(HasCheck(nil))
	s.False(HasCheck(codec.RawMessage("")))
	s.False(HasCheck(codec.RawMessage(" null\n")))
	s.True(HasCheck(codec.RawMessage(`{}`)))
}

func (s *CheckSpecSuite) TestChangedFieldsComparesChecksThroughEqual() {
	prev := Job{ID: "a", Goals: []Goal{{ID: "t", Kind: GoalKindCheck, Expect: ExpectPassed, Check: checkJSON("test", ".")}}}
	spelled := prev.Clone()
	spelled.Goals[0].Check = codec.RawMessage(`{"target":"test"}`)
	other := prev.Clone()
	other.Goals[0].Check = checkJSON("test", ".", "-run", "X")

	same, err := ChangedFields(prev, spelled, equalSpecCheck)
	s.Require().NoError(err)
	s.Empty(same, "one check spelled two ways is one check")
	changed, err := ChangedFields(prev, other, equalSpecCheck)
	s.Require().NoError(err)
	s.Equal([]string{"goals"}, changed)
}

func (s *CheckSpecSuite) TestChangedFieldsRefusesACheckEqualCannotRead() {
	prev := Job{ID: "a", Goals: []Goal{{ID: "t", Kind: GoalKindCheck, Expect: ExpectPassed, Check: checkJSON("test", ".")}}}
	next := prev.Clone()
	next.Goals[0].Check = codec.RawMessage(`{"target":"test","x":1}`)

	_, err := ChangedFields(prev, next, equalSpecCheck)

	refused, ok := errors.AsType[*CheckRefusedError](err)
	s.Require().True(ok, err)
	s.Equal("goals[t].check", refused.Field)
}

func (s *CheckSpecSuite) TestChangedFieldsNeedsEqualOnlyToCompareTwoChecks() {
	prev := Job{ID: "a", Goals: []Goal{{ID: "t", Kind: GoalKindPaths, Expect: ExpectChanged, Paths: []string{"x"}}}}

	changed, err := ChangedFields(prev, prev.Clone(), nil)
	s.Require().NoError(err)
	s.Empty(changed)

	withCheck := Job{ID: "a", Goals: []Goal{{ID: "t", Kind: GoalKindCheck, Expect: ExpectPassed, Check: checkJSON("test", ".")}}}
	_, err = ChangedFields(withCheck, withCheck.Clone(), nil)
	refused, ok := errors.AsType[*UnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithCheckSpec", refused.Option)
}

func (s *CheckSpecSuite) TestFaultsJudgeAChecksPresenceAndNeverItsContent() {
	d := Declaration{Schema: Schema{Version: APIVersion}, ID: "a", Check: codec.RawMessage(`{"anything":1}`), Goals: []Goal{
		{ID: "docs", Kind: GoalKindPaths, Paths: []string{"docs"}, Check: checkJSON("lint", ".")},
		{ID: "unit", Kind: GoalKindCheck, Accept: []Basis{BasisWitnessed}},
	}}

	var fields []string
	for _, f := range d.Faults() {
		fields = append(fields, f.Field)
	}

	s.ElementsMatch([]string{"goals[0].check", "goals[1].check"}, fields)
}
