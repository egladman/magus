package verdict

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type JudgmentSuite struct{ suite.Suite }

func TestJudgment(t *testing.T) { suite.Run(t, new(JudgmentSuite)) }

// The hook glue of every host parses this envelope, so its keys and their order are the
// contract.
func (s *JudgmentSuite) TestWireShapeKeepsEveryKeyInOrder() {
	full := Judgment{
		APIVersion: "magus.guard/v1alpha3",
		Decision:   "deny",
		Reason:     "no <b>",
		Context:    "fyi",
		Rule:       "some-rule",
		Lease:      "job-a",
		LeaseFrom:  "flag",
		NextSteps: []NextStep{{
			ID:    "deny-remedy-some-rule",
			Run:   "magus run test . && echo done",
			Argv:  []string{"magus", "run", "test", "."},
			Why:   "because",
			Reads: true,
		}},
	}
	got, err := codec.Marshal(full)
	s.Require().NoError(err)
	s.Equal(`{"api_version":"magus.guard/v1alpha3","decision":"deny","reason":"no <b>","context":"fyi","rule":"some-rule",`+
		`"lease":"job-a","lease_from":"flag","next_steps":[{"id":"deny-remedy-some-rule","run":"magus run test . && echo done",`+
		`"argv":["magus","run","test","."],"why":"because","reads":true}]}`, string(got))

	var back Judgment
	s.Require().NoError(codec.Unmarshal(got, &back))
	s.Equal(full, back)
}

func (s *JudgmentSuite) TestAPassNamesOnlyItsDecision() {
	pass, err := codec.Marshal(Judgment{APIVersion: APIVersion, Decision: "pass"})
	s.Require().NoError(err)
	s.Equal(`{"api_version":"magus.guard/v1alpha1","decision":"pass"}`, string(pass))
}

// A refused call reports the deny itself: its reason as the message, and the whole
// judgment for a caller that reads the rule or the remedy.
func (s *JudgmentSuite) TestDeniedErrorCarriesTheJudgment() {
	deny := Judgment{Decision: "deny", Reason: "run it through the target", Rule: "raw-tool"}
	var err error = &DeniedError{Judgment: deny}

	s.Equal(deny.Reason, err.Error())
	var denied *DeniedError
	s.Require().ErrorAs(fmt.Errorf("run: %w", err), &denied)
	s.Equal(deny, denied.Judgment)
}
