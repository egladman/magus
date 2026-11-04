package types

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
)

type MutantRunSuite struct{ suite.Suite }

func TestMutantRunSuite(t *testing.T) { suite.Run(t, new(MutantRunSuite)) }

func killed() MutantRun {
	return MutantRun{Key: "k1", ID: "m1", Outcome: MutantKilled, Seconds: 1.5, Checkout: "w0"}
}

func (s *MutantRunSuite) TestEntryWritesLowerCaseKeys() {
	b, err := json.Marshal(killed())
	s.Require().NoError(err)
	s.JSONEq(`{"key":"k1","id":"m1","outcome":"killed","seconds":1.5,"checkout":"w0"}`, string(b))
}

func (s *MutantRunSuite) TestEntryRoundTrips() {
	b, err := json.Marshal(killed())
	s.Require().NoError(err)
	var got MutantRun
	s.Require().NoError(json.Unmarshal(b, &got))
	s.Equal(killed(), got)
}

func (s *MutantRunSuite) TestWritingAnEntryWithoutAKeyIsRefused() {
	r := killed()
	r.Key = ""
	_, err := json.Marshal(r)
	s.ErrorContains(err, "names its key, id and checkout")
}

func (s *MutantRunSuite) TestWritingAnExceptedOutcomeIsRefused() {
	r := killed()
	r.Outcome = MutantExcepted
	_, err := json.Marshal(r)
	s.ErrorContains(err, `not "excepted"`)
}

func (s *MutantRunSuite) TestReadingAnEntryWithoutACheckoutIsRefused() {
	var got MutantRun
	err := json.Unmarshal([]byte(`{"key":"k","id":"m","outcome":"survived","seconds":1}`), &got)
	s.ErrorContains(err, "names its key, id and checkout")
}

func (s *MutantRunSuite) TestReadingAnUnknownOutcomeIsRefused() {
	var got MutantRun
	err := json.Unmarshal([]byte(`{"key":"k","id":"m","outcome":"lost","seconds":1,"checkout":"c"}`), &got)
	s.ErrorContains(err, `not "lost"`)
}

func (s *MutantRunSuite) TestReadingAMalformedEntryFails() {
	var got MutantRun
	s.Error(json.Unmarshal([]byte(`{"key":`), &got))
}

func (s *MutantRunSuite) TestRefusedReadLeavesTheTargetUntouched() {
	got := killed()
	s.Error(json.Unmarshal([]byte(`{"key":"k","id":"m","outcome":"lost","checkout":"c"}`), &got))
	s.Equal(killed(), got)
}
