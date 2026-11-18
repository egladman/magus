package job

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

// CheckSuite runs under both JSON builds (the test and test-jsonv-2 targets): a Check
// writes the same bytes whichever one wrote it.
type CheckSuite struct{ suite.Suite }

func TestCheckSuite(t *testing.T) { suite.Run(t, new(CheckSuite)) }

type holdsCheck struct {
	ID    string `json:"id"`
	Check Check  `json:"check,omitzero"`
}

func (s *CheckSuite) TestNewCheckIsTheCodecsEncoding() {
	c, err := NewCheck(specCheck{Target: "a<b", Project: "."})

	s.Require().NoError(err)
	s.Equal(`{"target":"a<b","project":"."}`, c.String())
	s.False(c.IsZero())
}

func (s *CheckSuite) TestNewCheckOfNilIsTheZeroCheck() {
	c, err := NewCheck(nil)

	s.Require().NoError(err)
	s.True(c.IsZero())
	s.Empty(c.String())
}

func (s *CheckSuite) TestDecodeIsStrict() {
	var sc specCheck
	s.Require().NoError(rawCheck(`{"target":"t"}`).Decode(&sc))
	s.Equal(specCheck{Target: "t"}, sc)

	s.ErrorIs(rawCheck(`{"target":"t","shell":"rm"}`).Decode(&sc), codec.ErrUnknownField)
	s.ErrorIs(Check{}.Decode(&sc), ErrNoCheck)
}

func (s *CheckSuite) TestTheZeroCheckIsNullAndOmitted() {
	b, err := Check{}.MarshalJSON()
	s.Require().NoError(err)
	s.Equal("null", string(b))

	out, err := codec.Marshal(holdsCheck{ID: "a"})
	s.Require().NoError(err)
	s.Equal(`{"id":"a"}`, string(out))
}

func (s *CheckSuite) TestNullAndEmptyReadAsTheZeroCheck() {
	for _, in := range []string{"null", " null\n", "", "  "} {
		c := checkJSON("t", ".")
		s.Require().NoError(c.UnmarshalJSON([]byte(in)), "%q", in)
		s.True(c.IsZero(), "%q", in)
	}
	var h holdsCheck
	s.Require().NoError(codec.Unmarshal([]byte(`{"id":"a","check":null}`), &h))
	s.True(h.Check.IsZero())
}

func (s *CheckSuite) TestADecodeKeepsTheCodecsCanonicalForm() {
	var h holdsCheck
	s.Require().NoError(codec.Unmarshal([]byte(`{"id":"a","check":{ "z" : "< ", "a": [1, 2] }}`), &h))

	s.Equal("{\"z\":\"< \",\"a\":[1,2]}", h.Check.String(), "member order kept, whitespace dropped, escapes the codec's")
	out, err := codec.Marshal(h)
	s.Require().NoError(err)
	s.Equal("{\"id\":\"a\",\"check\":{\"z\":\"< \",\"a\":[1,2]}}", string(out))
}

func (s *CheckSuite) TestARoundTripIsByteForByte() {
	in := `{"id":"a","check":{"target":"test","project":".","args":["-run","X"]}}`
	var h holdsCheck
	s.Require().NoError(codec.Unmarshal([]byte(in), &h))

	out, err := codec.Marshal(h)

	s.Require().NoError(err)
	s.Equal(in, string(out))
}

// encoding/json called directly, not through the codec, still reads and writes a Check.
func (s *CheckSuite) TestEncodingJSONRoundTripsACheck() {
	in := `{"id":"a","check":{"target":"t"}}`
	var h holdsCheck
	s.Require().NoError(json.Unmarshal([]byte(in), &h))

	out, err := json.Marshal(h)

	s.Require().NoError(err)
	s.Equal(in, string(out))
}

func (s *CheckSuite) TestAnInvalidCheckIsRefused() {
	var c Check
	s.Error(c.UnmarshalJSON([]byte(`{"target":`)))
	s.True(c.IsZero())
}

func (s *CheckSuite) TestMarshalJSONHandsOutACopy() {
	c := checkJSON("t", ".")
	b, err := c.MarshalJSON()
	s.Require().NoError(err)

	b[0] = 'x'

	s.Equal(`{"target":"t","project":"."}`, c.String())
}

// A stored row whose checks its author spelled loosely is written back in the codec's
// form, and that form reads and writes again unchanged.
func (s *CheckSuite) TestAStoredRowsChecksRewriteToOneLine() {
	stored := []byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},` +
		`"spec":{"check":{ "target" : "test", "project": "." },"postconditions":[{"id":"lint","kind":"check","expect":"passed",` +
		`"check":{"target":"lint","project":"p"},"accept":["witnessed"]}]},"status":{"created":10,"updated":20}}`)
	row, ok, err := ReadRecord(stored)
	s.Require().NoError(err)
	s.Require().True(ok)

	once, err := MarshalRecord(row, nil)
	s.Require().NoError(err)
	again, ok, err := ReadRecord(once)
	s.Require().NoError(err)
	s.Require().True(ok)
	twice, err := MarshalRecord(again, nil)
	s.Require().NoError(err)

	s.Contains(string(once), `"check":{"target":"test","project":"."},"postconditions":[{"id":"lint","kind":"check","expect":"passed","check":{"target":"lint","project":"p"},"accept":["witnessed"]}]`)
	s.Equal(string(once), string(twice))
}

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
func testCheckSpec() Option { return WithCheckSpec(specChecks{}) }

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

func (s *CheckSpecSuite) open(opts ...Option) *Store {
	st, err := Open(s.T().Context(), Config{Dir: s.dir}, writable(opts...)...)
	s.Require().NoError(err)
	return st
}

func (s *CheckSpecSuite) TestOpenRefusesANilSpec() {
	_, err := Open(s.T().Context(), Config{Dir: s.dir}, WithCheckSpec(nil))

	refused, ok := errors.AsType[*OptionError](err)
	s.Require().True(ok, err)
	s.Equal("WithCheckSpec", refused.Option)
}

func (s *CheckSpecSuite) TestAStoreWithNoCheckSpecRefusesARowWrite() {
	st, err := Open(s.T().Context(), Config{Dir: s.dir}, WithActor(func() Actor { return Actor{} }), WithNotices(func(Notice) {}))
	s.Require().NoError(err)

	_, err = st.Update(s.T().Context(), "a", func(*Job) {})

	refused, ok := errors.AsType[*UnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithCheckSpec", refused.Option)
}

func (s *CheckSpecSuite) TestAStoreWithNoCheckSpecRefusesToGrade() {
	st, err := Open(s.T().Context(), Config{Dir: s.dir}, WithTree(func(_ context.Context) (string, error) { return "tree", nil }))
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
