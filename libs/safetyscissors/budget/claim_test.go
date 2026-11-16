package budget

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ClaimSuite struct{ suite.Suite }

func TestClaim(t *testing.T) { suite.Run(t, new(ClaimSuite)) }

func (s *ClaimSuite) TestAdmissionRoundTripKeepsEveryField() {
	t := s.T()
	in := Admission{
		At: time.Date(2026, 11, 7, 1, 2, 3, 0, time.UTC), Outcome: AdmissionSeated,
		PID: 7, Project: "svc", Target: "lint", Under: "svc:ci", MemoryMB: 4096, Slots: 2,
		Dir: "/w", Command: "run ci && echo <done>", Owner: "job-a", Fits: true,
		WaitedMS: 1500, WaitBoundMS: 3000,
		Sizing:   Sizing{Samples: 12},
		BudgetMB: 24000, HeldMB: 8000, BudgetSlots: 8, HeldSlots: 3,
		Holders: []Claimant{{Project: "svc", Target: "test", PID: 9, Under: "svc:ci"}},
	}
	b, err := in.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(b), `"command":"run ci && echo <done>"`, "the line leaves HTML escaping to the caller's codec")
	var out Admission
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func (s *ClaimSuite) TestAdmissionOmitsZeroNumbersAndEmptyStrings() {
	b, err := json.Marshal(Admission{PID: 1, Project: "p", Target: "t"})
	require.NoError(s.T(), err)
	assert.JSONEq(s.T(), `{"at":"0001-01-01T00:00:00Z","pid":1,"project":"p","target":"t","fits":false}`, string(b))
}

func (s *ClaimSuite) TestAdmissionUnmarshalNamesTheTypeOnError() {
	var a Admission
	err := json.Unmarshal([]byte(`{"pid":"x"}`), &a)
	require.Error(s.T(), err)
	assert.ErrorContains(s.T(), err, "machine admission")
}

func (s *ClaimSuite) TestClaimRoundTrips() {
	in := Claim{Project: "p", Target: "t", Under: "p:ci", SlotsFloor: 1, WaitBoundMS: 5, WaitedMS: 4}
	b, err := json.Marshal(in)
	require.NoError(s.T(), err)
	var out Claim
	require.NoError(s.T(), json.Unmarshal(b, &out))
	assert.Equal(s.T(), in, out)
}

func (s *ClaimSuite) TestClaimRunIsItsOutermostInvocation() {
	assert.Equal(s.T(), "1:root", Claim{Invocation: "3:me", Ancestors: []string{"1:root", "2:mid"}}.Run())
	assert.Equal(s.T(), "3:me", Claim{Invocation: "3:me"}.Run())
	assert.Empty(s.T(), Claim{}.Run())
}

func (s *ClaimSuite) TestVerdictAddsOnlySlotsAndAheadWhenSeated() {
	t := s.T()
	zero, err := json.Marshal(Verdict{Granted: true, Fits: true})
	require.NoError(t, err)
	seated, err := json.Marshal(Verdict{Granted: true, Fits: true, Slots: 3, Ahead: 2})
	require.NoError(t, err)
	var base, got map[string]any
	require.NoError(t, json.Unmarshal(zero, &base))
	require.NoError(t, json.Unmarshal(seated, &got))
	base["slots"], base["ahead"] = float64(3), float64(2)
	assert.Equal(t, base, got, "slots and ahead are the only keys a seated verdict adds")
}

func (s *ClaimSuite) TestHolderListNamesCommandOrTarget() {
	now := time.Date(2026, 11, 7, 0, 1, 0, 0, time.UTC)
	snap := Snapshot{Holders: []Claimant{
		{PID: 1, Project: ".", Target: "test", Since: now.Add(-30 * time.Second)},
		{PID: 2, Command: "go test ./...", Since: now.Add(-time.Minute)},
	}}
	assert.Equal(s.T(), "pid 1 (test .) for 30s, pid 2 (`go test ./...`) for 1m0s", snap.HolderList(now))
}
