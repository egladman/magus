package broker

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func ledgerConns(t *testing.T, sessions ...*session) map[net.Conn]*session {
	t.Helper()
	conns := map[net.Conn]*session{}
	for _, s := range sessions {
		a, b := net.Pipe()
		t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
		conns[a] = s
	}
	return conns
}

func ledgerSession(pid int, lease string) *session {
	return &session{hello: hello{PID: pid, Lease: lease, Dir: "/w"}, holds: map[string]*heldWork{}}
}

func TestSessionOwnerNamesLeaseThenSpawnerThenUnleased(t *testing.T) {
	leased := ledgerSession(1, "fix-cache")
	assert.Equal(t, "fix-cache", sessionOwner(leased))

	exec := ledgerSession(2, "")
	exec.holds["c"] = &heldWork{work: Work{Kind: WorkExec, Spawner: "integrate"}}
	assert.Equal(t, "spawned by integrate", sessionOwner(exec))

	assert.Equal(t, "unleased", sessionOwner(ledgerSession(3, "")))
}

func TestLedgerRecordsNeitherAGrantNorAWaitAsARefusal(t *testing.T) {
	var l budgetLedger
	sess := ledgerSession(40, "job-a")
	conns := ledgerConns(t, sess)
	c := types.MachineClaim{Project: ".", Target: "go-test", PID: 40}
	l.claimed(conns, sess, c, types.MachineVerdict{Fits: true, OwnRun: true}, time.Unix(1000, 0))
	l.claimed(conns, sess, c, types.MachineVerdict{Granted: true, Fits: true, ID: "40.1"}, time.Unix(1001, 0))
	assert.Empty(t, l.capacity(conns, types.MachineSnapshot{}).Refused)
}

func TestLedgerRemembersTheLastRefusalsNewestFirstWithTheirHolders(t *testing.T) {
	var l budgetLedger
	holder, asker := ledgerSession(60, "holder-job"), ledgerSession(61, "")
	conns := ledgerConns(t, holder, asker)
	t0 := time.Unix(1000, 0)
	v := types.MachineVerdict{Fits: true, Holders: []types.MachineClaimant{{Project: ".", Target: "go-test", PID: 60, Slots: 8}}}
	for i := range refusedKeep + 5 {
		c := types.MachineClaim{Project: ".", Target: fmt.Sprintf("t%d", i), PID: 61, Dir: "/w"}
		l.claimed(conns, asker, c, v, t0.Add(time.Duration(i)*time.Second))
	}
	r := l.capacity(conns, types.MachineSnapshot{}).Refused
	require.Len(t, r, refusedKeep)
	assert.Equal(t, fmt.Sprintf("t%d", refusedKeep+4), r[0].Target)
	assert.Equal(t, "t5", r[refusedKeep-1].Target)
	assert.Equal(t, types.MachineRefusal{
		At: t0.Add(time.Duration(refusedKeep+4) * time.Second), PID: 61, Project: ".", Target: r[0].Target, Slots: 1,
		Dir: "/w", Owner: "unleased", Fits: true,
		Holders: []types.MachineClaimant{{Project: ".", Target: "go-test", PID: 60, Slots: 8, Owner: "holder-job"}},
	}, r[0])
}

func TestLedgerOwnsAReservedSeatAndRefusesOneThatCannotFit(t *testing.T) {
	var l budgetLedger
	waiter := ledgerSession(70, "job-c")
	conns := ledgerConns(t, waiter)
	now := time.Unix(1000, 0)
	c := types.MachineClaim{Target: "ci", PID: 70, Command: "magus affected ci"}

	l.reserved(conns, waiter, c, 99, types.MachineVerdict{Fits: true}, now)
	assert.Empty(t, l.seats, "a reservation still waiting holds no seat")

	l.reserved(conns, waiter, c, 99, types.MachineVerdict{Granted: true, Fits: true}, now)
	snap := l.capacity(conns, types.MachineSnapshot{Holders: []types.MachineClaimant{{Reserved: true, Target: "ci", PID: 99}}})
	assert.Equal(t, "job-c", snap.Holders[0].Owner)

	l.capacity(conns, types.MachineSnapshot{})
	assert.Empty(t, l.seats, "a seat no longer held is forgotten")

	l.reserved(conns, waiter, types.MachineClaim{Target: "huge", MemoryMB: 1 << 20}, 99, types.MachineVerdict{}, now)
	r := l.capacity(conns, types.MachineSnapshot{}).Refused
	require.Len(t, r, 1)
	assert.Equal(t, 99, r[0].PID)
	assert.False(t, r[0].Fits)
}

func TestLedgerBoundsTheSeatsItRemembers(t *testing.T) {
	var l budgetLedger
	now := time.Unix(1000, 0)
	for pid := 1; pid <= reservedKeep+10; pid++ {
		l.reserve(pid, "o", now.Add(time.Duration(pid)*time.Second))
	}
	assert.Len(t, l.seats, reservedKeep)
	assert.NotContains(t, l.seats, 1)
	assert.Contains(t, l.seats, reservedKeep+10)
}

func TestCapacityJSONCarriesOwnersWaitersAndRefusals(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 40, 55, 0, time.UTC)
	holder := types.MachineClaimant{Project: ".", Target: "go-test", PID: 1, Slots: 8, Owner: "job-a", Since: at}
	b, err := json.Marshal(types.MachineSnapshot{
		BudgetSlots: 10, HeldSlots: 8,
		Holders: []types.MachineClaimant{holder},
		Waiting: []types.MachineWaiter{{Kind: "claim", PID: 2, Project: ".", Target: "lint", Slots: 1, Dir: "/w", Owner: "unleased", Since: at, WaitedMS: 1500}},
		Refused: []types.MachineRefusal{{At: at, PID: 3, Project: ".", Target: "ci", Slots: 4, Owner: "spawned by root", Fits: true,
			Holders: []types.MachineClaimant{holder}}},
	})
	require.NoError(t, err)
	const holderJSON = `{"project": ".", "target": "go-test", "pid": 1, "slots": 8, "owner": "job-a", "since": "2026-10-24T09:40:55Z"}`
	assert.JSONEq(t, fmt.Sprintf(`{
		"budget_slots": 10, "held_slots": 8,
		"holders": [%[1]s],
		"waiting": [{"kind": "claim", "pid": 2, "project": ".", "target": "lint", "slots": 1, "dir": "/w", "owner": "unleased", "since": "2026-10-24T09:40:55Z", "waited_ms": 1500}],
		"refused": [{"at": "2026-10-24T09:40:55Z", "pid": 3, "project": ".", "target": "ci", "slots": 4, "owner": "spawned by root", "fits": true,
			"holders": [%[1]s]}]
	}`, holderJSON), string(b))
}
