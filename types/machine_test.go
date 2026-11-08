package types

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MachineAdmissionSuite struct{ suite.Suite }

func TestMachineAdmission(t *testing.T) { suite.Run(t, new(MachineAdmissionSuite)) }

func (s *MachineAdmissionSuite) TestRoundTripKeepsEveryField() {
	in := MachineAdmission{
		At: time.Date(2026, 11, 7, 1, 2, 3, 0, time.UTC), Outcome: AdmissionSeated,
		PID: 7, Project: "svc", Target: "lint", Under: "svc:ci", MemoryMB: 4096, Slots: 2,
		Dir: "/w", Command: "magus run ci", Owner: "job-a", Fits: true,
		WaitedMS: 1500, WaitBoundMS: 3000,
		Sizing:   MemorySizing{Samples: 12},
		BudgetMB: 24000, HeldMB: 8000, BudgetSlots: 8, HeldSlots: 3,
		Holders: []MachineClaimant{{Project: "svc", Target: "test", PID: 9, Under: "svc:ci"}},
	}
	b, err := json.Marshal(in)
	require.NoError(s.T(), err)
	var out MachineAdmission
	require.NoError(s.T(), json.Unmarshal(b, &out))
	assert.Equal(s.T(), in, out)
}

func (s *MachineAdmissionSuite) TestZeroNumbersAndEmptyStringsAreOmitted() {
	b, err := json.Marshal(MachineAdmission{PID: 1, Project: "p", Target: "t"})
	require.NoError(s.T(), err)
	assert.JSONEq(s.T(), `{"at":"0001-01-01T00:00:00Z","pid":1,"project":"p","target":"t","fits":false}`, string(b))
}

func (s *MachineAdmissionSuite) TestRefusalLineFromBeforeTheRenameStillReads() {
	var got MachineAdmission
	line := `{"at":"2026-11-07T00:00:00Z","pid":3,"project":"p","target":"t","memory_mb":10,"slots":1,"fits":true}`
	require.NoError(s.T(), json.Unmarshal([]byte(line), &got))
	want := MachineAdmission{
		At: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), PID: 3, Project: "p", Target: "t",
		MemoryMB: 10, Slots: 1, Fits: true,
	}
	assert.Equal(s.T(), want, got)
}

func (s *MachineAdmissionSuite) TestUnmarshalNamesTheTypeOnError() {
	var a MachineAdmission
	err := json.Unmarshal([]byte(`{"pid":"x"}`), &a)
	require.Error(s.T(), err)
	assert.ErrorContains(s.T(), err, "machine admission")
}

func (s *MachineAdmissionSuite) TestClaimNewFieldsRideTheWire() {
	in := MachineClaim{Project: "p", Target: "t", Under: "p:ci", SlotsFloor: 1, WaitBoundMS: 5, WaitedMS: 4}
	b, err := json.Marshal(in)
	require.NoError(s.T(), err)
	var out MachineClaim
	require.NoError(s.T(), json.Unmarshal(b, &out))
	assert.Equal(s.T(), in, out)
}

func (s *MachineAdmissionSuite) TestClaimWithoutNewFieldsKeepsItsOldShape() {
	b, err := json.Marshal(MachineClaim{Project: "p", Target: "t"})
	require.NoError(s.T(), err)
	assert.JSONEq(s.T(), `{"project":"p","target":"t","pid":0}`, string(b))
}

func (s *MachineAdmissionSuite) TestVerdictNewFieldsAreOmittedWhenZero() {
	zero, err := json.Marshal(MachineVerdict{Granted: true, Fits: true})
	require.NoError(s.T(), err)
	seated, err := json.Marshal(MachineVerdict{Granted: true, Fits: true, Slots: 3, Ahead: 2})
	require.NoError(s.T(), err)
	var base, got map[string]any
	require.NoError(s.T(), json.Unmarshal(zero, &base))
	require.NoError(s.T(), json.Unmarshal(seated, &got))
	base["slots"], base["ahead"] = float64(3), float64(2)
	assert.Equal(s.T(), base, got, "slots and ahead are the only keys a seated verdict adds")
}

func TestFormatMB(t *testing.T) {
	assert.Equal(t, "512 MiB", FormatMB(512))
	assert.Equal(t, "1.0 GiB", FormatMB(1024))
	assert.Equal(t, "10.0 GiB", FormatMB(10240))
}

func TestMemorySizingBreakdown(t *testing.T) {
	assert.Equal(t, "lint 4.8 GiB: own peak over 48 runs", MemorySizing{Samples: 48}.Breakdown("lint", "", 4915))
	assert.Equal(t, "conventions 4.2 GiB under lint: own peak over 12 runs", MemorySizing{Samples: 12}.Breakdown("conventions", "lint", 4300))
	assert.Equal(t, "lint 512 MiB: declared", MemorySizing{}.Breakdown("lint", "", 512))
	assert.Equal(t, "lint 1.0 GiB: own peak over 3 runs of any shape", MemorySizing{Samples: 3, AnyShape: true}.Breakdown("lint", "", 1024))
	assert.Equal(t, "lint 1.0 GiB: declared; no memory curve; held flat", MemorySizing{Fallback: "no memory curve; held flat"}.Breakdown("lint", "", 1024))
}
