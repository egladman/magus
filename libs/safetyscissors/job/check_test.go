package job

import (
	"encoding/json"
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
