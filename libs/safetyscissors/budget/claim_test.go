package budget

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type ClaimSuite struct{ suite.Suite }

func TestClaim(t *testing.T) { suite.Run(t, new(ClaimSuite)) }

func (s *ClaimSuite) TestAdmissionRoundTripKeepsEveryField() {
	t := s.T()
	in := Admission{
		At: time.Date(2026, 11, 7, 1, 2, 3, 0, time.UTC), Outcome: AdmissionSeated,
		PID: 7, Project: "svc", Target: "lint", Under: "svc:ci", MemoryMB: 4096, Slots: 2,
		Dir: "/w", Command: "run ci && echo <done>", Owner: "job-a", Fits: true,
		WaitedMs: 1500, WaitBound: 3 * time.Second,
		Sizing:   Sizing{Samples: 12},
		BudgetMB: 24000, HeldMB: 8000, BudgetSlots: 8, HeldSlots: 3,
		Holders: []Claimant{{Project: "svc", Target: "test", PID: 9, Under: "svc:ci"}},
	}
	b, err := in.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(b), `"command":"run ci && echo <done>"`, "the line leaves HTML escaping to the caller's codec")
	var out Admission
	require.NoError(t, codec.Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func (s *ClaimSuite) TestAdmissionOmitsZeroNumbersAndEmptyStrings() {
	b, err := codec.Marshal(Admission{PID: 1, Project: "p", Target: "t"})
	require.NoError(s.T(), err)
	assert.JSONEq(s.T(), `{"at":"0001-01-01T00:00:00Z","pid":1,"project":"p","target":"t","fits":false}`, string(b))
}

func (s *ClaimSuite) TestAdmissionUnmarshalNamesTheTypeOnError() {
	var a Admission
	err := codec.Unmarshal([]byte(`{"pid":"x"}`), &a)
	require.Error(s.T(), err)
	assert.ErrorContains(s.T(), err, "machine admission")
}

func (s *ClaimSuite) TestClaimRoundTrips() {
	in := Claim{Project: "p", Target: "t", Under: "p:ci", SlotsFloor: 1, WaitBound: 5 * time.Millisecond, WaitedMs: 4}
	b, err := codec.Marshal(in)
	require.NoError(s.T(), err)
	var out Claim
	require.NoError(s.T(), codec.Unmarshal(b, &out))
	assert.Equal(s.T(), in, out)
}

func (s *ClaimSuite) TestClaimRunIsItsOutermostInvocation() {
	assert.Equal(s.T(), "1:root", Claim{Invocation: "3:me", Ancestors: []string{"1:root", "2:mid"}}.Run())
	assert.Equal(s.T(), "3:me", Claim{Invocation: "3:me"}.Run())
	assert.Empty(s.T(), Claim{}.Run())
}

func (s *ClaimSuite) TestVerdictAddsOnlySlotsAndAheadWhenSeated() {
	t := s.T()
	zero, err := codec.Marshal(Verdict{Granted: true, Fits: true})
	require.NoError(t, err)
	seated, err := codec.Marshal(Verdict{Granted: true, Fits: true, Slots: 3, Ahead: 2})
	require.NoError(t, err)
	var base, got map[string]any
	require.NoError(t, codec.Unmarshal(zero, &base))
	require.NoError(t, codec.Unmarshal(seated, &got))
	base["slots"], base["ahead"] = float64(3), float64(2)
	assert.Equal(t, base, got, "slots and ahead are the only keys a seated verdict adds")
}

// The bound crosses the wire as whole milliseconds, under the key it always had.
func (s *ClaimSuite) TestWaitBoundIsMillisecondsOnTheWire() {
	b, err := codec.Marshal(Claim{Project: "p", Target: "t", WaitBound: 1500 * time.Millisecond})
	s.Require().NoError(err)
	s.JSONEq(`{"project":"p","target":"t","pid":0,"wait_bound_ms":1500}`, string(b))
	b, err = codec.Marshal(Admission{PID: 1, Project: "p", Target: "t", WaitBound: 2 * time.Second})
	s.Require().NoError(err)
	s.Contains(string(b), `"wait_bound_ms":2000`)
}

func (s *ClaimSuite) TestClaimUnmarshalNamesTheTypeOnError() {
	var c Claim
	s.ErrorContains(codec.Unmarshal([]byte(`{"pid":"x"}`), &c), "budget: decode machine claim")
}
