package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waiters is the broker's parked requests as its status reply lists them, with the
// moments cleared.
func waiters(t *testing.T, addr string) []types.MachineWaiter {
	t.Helper()
	st, err := QueryStatus(t.Context(), addr)
	require.NoError(t, err)
	w := st.Capacity.Waiting
	for i := range w {
		w[i].Since, w[i].WaitedMS = time.Time{}, 0
	}
	return w
}

type claimed struct {
	v   types.MachineVerdict
	err error
}

// claimAsync runs c.Claim on its own goroutine and returns once the broker answered or
// parked it, so the next request queues behind it.
func claimAsync(t *testing.T, ctx context.Context, addr string, c *Client, claim types.MachineClaim) <-chan claimed {
	t.Helper()
	before := len(waiters(t, addr))
	done := make(chan claimed, 1)
	go func() {
		v, err := c.Claim(ctx, claim)
		done <- claimed{v, err}
	}()
	require.Eventually(t, func() bool { return len(done) == 1 || len(waiters(t, addr)) > before }, 5*time.Second, 5*time.Millisecond)
	return done
}

func requirePending(t *testing.T, done <-chan claimed) {
	t.Helper()
	select {
	case got := <-done:
		t.Fatalf("answered (%+v) while it should be parked", got)
	default:
	}
}

func requireAnswer(t *testing.T, done <-chan claimed) claimed {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("never answered")
		return claimed{}
	}
}

func TestAClaimKeptOutByItsOwnProcessIsParkedUntilARelease(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	first, err := c.Claim(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 900})
	require.NoError(t, err)
	require.True(t, first.Granted)

	second := claimAsync(t, t.Context(), addr, c, types.MachineClaim{Project: "docs", Target: "lint", MemoryMB: 900, Dir: "/w"})
	requirePending(t, second)
	assert.Equal(t, []types.MachineWaiter{{Kind: "claim", Project: "docs", Target: "lint", PID: os.Getpid(), MemoryMB: 900, Slots: 1, Dir: "/w", Command: waiters(t, addr)[0].Command, Owner: "unleased"}},
		waiters(t, addr), "status lists the parked claim")

	stranger := dial(t, addr)
	refused, err := stranger.Claim(t.Context(), types.MachineClaim{Project: "x", Target: "build", MemoryMB: 900, PID: 1})
	require.NoError(t, err)
	assert.Equal(t, []bool{false, true, false}, []bool{refused.Granted, refused.Fits, refused.OwnRun},
		"another invocation's claim is refused at once, never parked")

	c.Release(t.Context(), first.ID)
	got := requireAnswer(t, second)
	require.NoError(t, got.err)
	assert.True(t, got.v.Granted, "the release seats the parked claim")
	assert.Empty(t, waiters(t, addr))
}

// TestAParkedQueueLetsSmallClaimsByUntilTheHeadStarves: a release seats the claims that
// fit the room it freed, ahead of a wider head, maxOvertakes times; after that nothing
// behind the head is seated until the head is.
func TestAParkedQueueLetsSmallClaimsByUntilTheHeadStarves(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, maxOvertakes))
	c := dial(t, addr)
	one := types.MachineClaim{Target: "small", Slots: 1}
	var held []string
	for range maxOvertakes {
		v, err := c.Claim(t.Context(), one)
		require.NoError(t, err)
		require.True(t, v.Granted)
		held = append(held, v.ID)
	}
	head := claimAsync(t, t.Context(), addr, c, types.MachineClaim{Target: "wide", Slots: maxOvertakes - 1})
	var small []<-chan claimed
	for range maxOvertakes + 1 {
		small = append(small, claimAsync(t, t.Context(), addr, c, one))
	}

	var overtook []string
	for i, id := range held {
		c.Release(t.Context(), id)
		got := requireAnswer(t, small[i])
		require.True(t, got.v.Granted)
		overtook = append(overtook, got.v.ID)
	}
	requirePending(t, head)

	c.Release(t.Context(), overtook[0])
	requirePending(t, head)
	requirePending(t, small[maxOvertakes])
	c.Release(t.Context(), overtook[1])
	c.Release(t.Context(), overtook[2])
	got := requireAnswer(t, head)
	require.True(t, got.v.Granted, "the starved head is seated before the claim behind it")
	requirePending(t, small[maxOvertakes])

	c.Release(t.Context(), overtook[3])
	assert.True(t, requireAnswer(t, small[maxOvertakes]).v.Granted)
}

func TestAParkedSeatWaitIsSeatedWhenTheSeatsRunEnds(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	first := leasedClient(t, addr, "plan/a", "plan", 1)
	second := leasedClient(t, addr, "plan/b", "plan", 1)
	v, err := first.Claim(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "1:a"})
	require.NoError(t, err)
	require.True(t, v.Granted)

	waiting := claimAsync(t, t.Context(), addr, second, types.MachineClaim{Target: "go-test", Invocation: "2:b"})
	requirePending(t, waiting)
	require.Eventually(t, second.seatWaiting.Load, 5*time.Second, 5*time.Millisecond, "the parked reply names the seat's holder")

	require.NoError(t, first.Close())
	got := requireAnswer(t, waiting)
	require.NoError(t, got.err)
	assert.True(t, got.v.Granted)
	assert.False(t, second.seatWaiting.Load())
}

func TestAParkedClaimEndsWithItsContextOrItsConnection(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	_, err := c.Claim(t.Context(), types.MachineClaim{Target: "test", MemoryMB: 900})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := claimAsync(t, ctx, addr, c, types.MachineClaim{Target: "lint", MemoryMB: 900})
	cancel()
	require.ErrorIs(t, requireAnswer(t, done).err, context.Canceled)
	assert.Eventually(t, func() bool { return len(waiters(t, addr)) == 0 }, 5*time.Second, 5*time.Millisecond,
		"a cancelled wait leaves the queue")

	other := dial(t, addr)
	parked := claimAsync(t, t.Context(), addr, other, types.MachineClaim{Target: "build", MemoryMB: 900})
	require.NoError(t, other.Close())
	require.Error(t, requireAnswer(t, parked).err)
	assert.Eventually(t, func() bool { return len(waiters(t, addr)) == 0 }, 5*time.Second, 5*time.Millisecond,
		"a closed connection's wait leaves the queue")
}

func TestStoppingTheBrokerRefusesEveryParkedRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(stop func(), drain chan struct{})
		want string
	}{
		{"shutdown", func(stop func(), _ chan struct{}) { stop() }, "stopped"},
		{"drain", func(_ func(), drain chan struct{}) { close(drain) }, "seats nothing new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := testAddr(t)
			drain := make(chan struct{})
			stop, _ := serve(t, addr, WithCapacity(1000, 4), WithDrain(drain, time.Minute))
			c := dial(t, addr)
			_, err := c.Claim(t.Context(), types.MachineClaim{Target: "test", MemoryMB: 900})
			require.NoError(t, err)
			done := claimAsync(t, t.Context(), addr, c, types.MachineClaim{Target: "lint", MemoryMB: 900})

			tc.stop(stop, drain)
			got := requireAnswer(t, done)
			var refused *Error
			require.True(t, errors.As(got.err, &refused), "%v", got.err)
			assert.Equal(t, CodeDraining, refused.Code)
			assert.Contains(t, refused.Message, tc.want)
			assert.Contains(t, refused.Message, "this claim waited")
		})
	}
}

func TestAReservationAndAnAwaitAreParkedUntilTheBudgetHasRoom(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 4))
	reader := startReader(t)
	holder := dial(t, addr)
	held, err := holder.Request(t.Context(), types.MachineClaim{Target: "test", Slots: 4, PID: 1})
	require.NoError(t, err)
	require.True(t, held.Granted)

	type answer struct {
		v       types.MachineVerdict
		blocked types.MachineClaim
		err     error
	}
	// The await parks first: a seat reserved ahead of it would keep it out in turn.
	asker := dial(t, addr)
	parkedOn := make(chan types.MachineClaim, 1)
	awaited := make(chan answer, 1)
	go func() {
		blocked, v, err := asker.Await(t.Context(), []types.MachineClaim{{Target: "go-test", Slots: 2}},
			func(blocked types.MachineClaim, _ types.MachineVerdict) { parkedOn <- blocked })
		awaited <- answer{v, blocked, err}
	}()
	assert.Equal(t, types.MachineClaim{Target: "go-test", Slots: 2}, <-parkedOn, "the await says what it is parked on")
	reserved := make(chan answer, 1)
	go func() {
		v, err := asker.Reserve(t.Context(), types.MachineClaim{Target: "go-test", Slots: 4}, reader.Process.Pid,
			func(types.MachineVerdict) { parkedOn <- types.MachineClaim{Target: "reserve"} })
		reserved <- answer{v: v, err: err}
	}()
	assert.Equal(t, "reserve", (<-parkedOn).Target)
	assert.Len(t, waiters(t, addr), 2)

	holder.Release(t.Context(), held.ID)
	for _, ch := range []chan answer{awaited, reserved} {
		select {
		case a := <-ch:
			require.NoError(t, a.err)
			assert.True(t, a.v.Granted)
		case <-time.After(5 * time.Second):
			t.Fatal("never answered after the release")
		}
	}
	h := holders(t, addr)
	require.Len(t, h, 1)
	assert.True(t, h[0].Reserved, "the reservation holds the seat; the await took nothing")
}

func TestAnAwaitLargerThanTheBudgetIsRefusedAtOnce(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 4))
	blocked, v, err := dial(t, addr).Await(t.Context(), []types.MachineClaim{{Target: "test", Slots: 8}}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.MachineClaim{Target: "test", Slots: 8}, blocked)
	assert.False(t, v.Fits)
	assert.False(t, v.Granted)
}

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
