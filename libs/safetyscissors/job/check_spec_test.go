package job

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// specCheck is the check the tests' [WithCheckSpec] reads: a target and the project it
// runs in, "." when it names none.
type specCheck struct {
	Target  string   `json:"target"`
	Project string   `json:"project,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func readSpecCheck(c Check) (specCheck, error) {
	var sc specCheck
	if err := c.Decode(&sc); err != nil {
		return specCheck{}, err
	}
	if sc.Target == "" {
		return specCheck{}, errors.New("names no target")
	}
	if sc.Project == "" {
		sc.Project = "."
	}
	return sc, nil
}

// specChecks is the [CheckSpec] every test store is opened with.
type specChecks struct{}

func (specChecks) Validate(c Check) (Check, error) {
	sc, err := readSpecCheck(c)
	if err != nil {
		return Check{}, err
	}
	return NewCheck(sc)
}

func (specChecks) Equal(a, b Check) (bool, error) {
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

func (specChecks) Render(c Check) (string, error) {
	sc, err := readSpecCheck(c)
	if err != nil {
		return "", err
	}
	line := sc.Target + " " + sc.Project
	if len(sc.Args) > 0 {
		line += " -- " + strings.Join(sc.Args, " ")
	}
	return line, nil
}

// testCheckSpec is the [WithCheckSpec] every test store is opened with.
func testCheckSpec() StoreOption { return WithCheckSpec(specChecks{}) }

// rawCheck is the Check json spells, as its author spelled it: the tests hand the store
// spellings no decode would keep.
func rawCheck(json string) Check { return storedCheck([]byte(strings.TrimSpace(json))) }

// checkJSON is a check as specChecks stores it, for names Go and JSON quote alike.
func checkJSON(target, project string, args ...string) Check {
	line := `{"target":` + strconv.Quote(target) + `,"project":` + strconv.Quote(project)
	if len(args) > 0 {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = strconv.Quote(a)
		}
		line += `,"args":[` + strings.Join(quoted, ",") + `]`
	}
	return rawCheck(line + "}")
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

func (s *CheckSpecSuite) TestOpenRefusesANilSpec() {
	_, err := Open(s.dir, WithCheckSpec(nil))

	_, ok := errors.AsType[*CheckSpecError](err)
	s.True(ok, err)
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
		j.Check = rawCheck(`{ "target": "test" }`)
		j.Postconditions = []Postcondition{{ID: "lint", Kind: PostconditionKindCheck, Check: rawCheck(`{"project":"p","target":"lint"}`), Accept: []Basis{BasisWitnessed}}}
	})

	s.Require().NoError(err)
	s.Equal(`{"target":"test","project":"."}`, row.Check.String())
	s.Equal(`{"target":"lint","project":"p"}`, row.Postconditions[0].Check.String())
	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	s.Equal(`{"target":"test","project":"."}`, rows[0].Check.String())
}

func (s *CheckSpecSuite) TestACheckTheSpecRefusesIsNotWritten() {
	st := s.open()

	_, err := st.Update(s.T().Context(), "a", func(j *Job) {
		j.Postconditions = []Postcondition{{ID: "lint", Kind: PostconditionKindCheck, Check: rawCheck(`{"target":"lint","shell":"rm"}`), Accept: []Basis{BasisWitnessed}}}
	})

	refused, ok := errors.AsType[*CheckRefusedError](err)
	s.Require().True(ok, err)
	s.Equal("a", refused.Job)
	s.Equal("postconditions[lint].check", refused.Field)
	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *CheckSpecSuite) TestARowCheckThatIsNullDeclaresNone() {
	st := s.open()

	row, err := st.Update(s.T().Context(), "a", func(j *Job) { j.Check = rawCheck(" null ") })

	s.Require().NoError(err)
	s.True(row.Check.IsZero())
}

func (s *CheckSpecSuite) TestChangedFieldsComparesChecksThroughTheSpec() {
	prev := Job{ID: "a", Postconditions: []Postcondition{{ID: "t", Kind: PostconditionKindCheck, Expect: ExpectPassed, Check: checkJSON("test", ".")}}}
	spelled := prev.Clone()
	spelled.Postconditions[0].Check = rawCheck(`{"target":"test"}`)
	other := prev.Clone()
	other.Postconditions[0].Check = checkJSON("test", ".", "-run", "X")

	same, err := ChangedFields(prev, spelled, specChecks{})
	s.Require().NoError(err)
	s.Empty(same, "one check spelled two ways is one check")
	changed, err := ChangedFields(prev, other, specChecks{})
	s.Require().NoError(err)
	s.Equal([]string{"postconditions"}, changed)
}

func (s *CheckSpecSuite) TestChangedFieldsRefusesACheckTheSpecCannotRead() {
	prev := Job{ID: "a", Postconditions: []Postcondition{{ID: "t", Kind: PostconditionKindCheck, Expect: ExpectPassed, Check: checkJSON("test", ".")}}}
	next := prev.Clone()
	next.Postconditions[0].Check = rawCheck(`{"target":"test","x":1}`)

	_, err := ChangedFields(prev, next, specChecks{})

	refused, ok := errors.AsType[*CheckRefusedError](err)
	s.Require().True(ok, err)
	s.Equal("postconditions[t].check", refused.Field)
}

func (s *CheckSpecSuite) TestChangedFieldsNeedsTheSpecOnlyToCompareTwoChecks() {
	prev := Job{ID: "a", Postconditions: []Postcondition{{ID: "t", Kind: PostconditionKindPaths, Expect: ExpectChanged, Paths: []string{"x"}}}}

	changed, err := ChangedFields(prev, prev.Clone(), nil)
	s.Require().NoError(err)
	s.Empty(changed)

	withCheck := Job{ID: "a", Postconditions: []Postcondition{{ID: "t", Kind: PostconditionKindCheck, Expect: ExpectPassed, Check: checkJSON("test", ".")}}}
	_, err = ChangedFields(withCheck, withCheck.Clone(), nil)
	refused, ok := errors.AsType[*UnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithCheckSpec", refused.Option)
}

func (s *CheckSpecSuite) TestFaultsJudgeAChecksPresenceAndNeverItsContent() {
	d := Declaration{Schema: Schema{Version: APIVersion}, ID: "a", Check: rawCheck(`{"anything":1}`), Postconditions: []Postcondition{
		{ID: "docs", Kind: PostconditionKindPaths, Paths: []string{"docs"}, Check: checkJSON("lint", ".")},
		{ID: "unit", Kind: PostconditionKindCheck, Accept: []Basis{BasisWitnessed}},
	}}

	var fields []string
	for _, f := range d.Faults() {
		fields = append(fields, f.Field)
	}

	s.ElementsMatch([]string{"postconditions[0].check", "postconditions[1].check"}, fields)
}
