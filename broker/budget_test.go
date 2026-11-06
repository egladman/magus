package broker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
		v, err := c.Claim(ctx, claim, nil)
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
	first, err := c.Claim(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 900}, nil)
	require.NoError(t, err)
	require.True(t, first.Granted)

	second := claimAsync(t, t.Context(), addr, c, types.MachineClaim{Project: "docs", Target: "lint", MemoryMB: 900, Dir: "/w"})
	requirePending(t, second)
	assert.Equal(t, []types.MachineWaiter{{Project: "docs", Target: "lint", PID: os.Getpid(), MemoryMB: 900, Slots: 1, Dir: "/w", Command: waiters(t, addr)[0].Command, Owner: "unleased"}},
		waiters(t, addr), "status lists the parked claim")

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
		v, err := c.Claim(t.Context(), one, nil)
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

// holdOnlySlot fills a one-slot broker, so whatever c claims next parks on it.
func holdOnlySlot(t *testing.T) (addr string, c *Client, held string) {
	t.Helper()
	addr = testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	c = dial(t, addr)
	v, err := c.Claim(t.Context(), types.MachineClaim{Target: "holder", Slots: 1}, nil)
	require.NoError(t, err)
	require.True(t, v.Granted)
	return addr, c, v.ID
}

// seatInOrder passes the one slot along, and returns who took it each time.
func seatInOrder(t *testing.T, c *Client, held string, parked map[string]<-chan claimed) []string {
	t.Helper()
	var order []string
	for range len(parked) {
		c.Release(t.Context(), held)
		var seated string
		require.Eventually(t, func() bool {
			for target, done := range parked {
				select {
				case got := <-done:
					require.True(t, got.v.Granted)
					seated, held = target, got.v.ID
					return true
				default:
				}
			}
			return false
		}, 5*time.Second, time.Millisecond)
		order = append(order, seated)
		delete(parked, seated)
	}
	return order
}

func TestAParkedQueueSeatsTheHeavierJobFirst(t *testing.T) {
	addr, c, held := holdOnlySlot(t)
	parked := map[string]<-chan claimed{}
	for _, w := range []struct {
		target string
		weight float64
	}{{"light", 0.25}, {"unbound", 0}, {"heavy", 1}, {"named-default", types.DefaultJobWeight}} {
		parked[w.target] = claimAsync(t, t.Context(), addr, c, types.MachineClaim{Target: w.target, Slots: 1, Weight: w.weight})
	}
	assert.Equal(t, []string{"heavy", "unbound", "named-default", "light"}, seatInOrder(t, c, held, parked),
		"no weight weighs the default, and equal weights stay in the order they asked")
}

func TestAParkedQueueStopsLettingHeavierJobsPastAfterMaxOvertakes(t *testing.T) {
	addr, c, held := holdOnlySlot(t)
	parked := map[string]<-chan claimed{
		"light": claimAsync(t, t.Context(), addr, c, types.MachineClaim{Target: "light", Slots: 1, Weight: 0.25}),
	}
	want := make([]string, 0, maxOvertakes+2)
	for i := range maxOvertakes + 1 {
		target := fmt.Sprintf("heavy-%d", i)
		parked[target] = claimAsync(t, t.Context(), addr, c, types.MachineClaim{Target: target, Slots: 1, Weight: 1})
		want = append(want, target)
	}
	want = slices.Insert(want, maxOvertakes, "light")
	assert.Equal(t, want, seatInOrder(t, c, held, parked))
}

func TestAGateStillGoesAheadOfAHeavierJob(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	c := dial(t, addr)
	v, err := c.Claim(t.Context(), types.MachineClaim{Target: "holder", Slots: 1}, nil)
	require.NoError(t, err)
	require.True(t, v.Granted)
	heavy := claimAsync(t, t.Context(), addr, c, types.MachineClaim{Target: "heavy", Slots: 1, Weight: 1})

	gate := NewClient(addr, WithGate(true))
	t.Cleanup(func() { _ = gate.Close() })
	gated := claimAsync(t, t.Context(), addr, gate, types.MachineClaim{Target: "ci", Slots: 1})

	c.Release(t.Context(), v.ID)
	assert.True(t, requireAnswer(t, gated).v.Granted)
	requirePending(t, heavy)
}

func TestAParkedSeatWaitIsSeatedWhenTheSeatsRunEnds(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 10))
	first := leasedClient(t, addr, "plan/a", "plan", 1)
	second := leasedClient(t, addr, "plan/b", "plan", 1)
	v, err := first.Claim(t.Context(), types.MachineClaim{Target: "go-test", Invocation: "1:a"}, nil)
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
	_, err := c.Claim(t.Context(), types.MachineClaim{Target: "test", MemoryMB: 900}, nil)
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
			_, err := c.Claim(t.Context(), types.MachineClaim{Target: "test", MemoryMB: 900}, nil)
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

func TestCapacityJSONCarriesOwnersWaitersAndRefusals(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 40, 55, 0, time.UTC)
	holder := types.MachineClaimant{Project: ".", Target: "go-test", PID: 1, Slots: 8, Owner: "job-a", Since: at}
	b, err := json.Marshal(types.MachineSnapshot{
		BudgetSlots: 10, HeldSlots: 8,
		Holders: []types.MachineClaimant{holder},
		Waiting: []types.MachineWaiter{{PID: 2, Project: ".", Target: "lint", Slots: 1, Dir: "/w", Owner: "unleased", Since: at, WaitedMS: 1500}},
		Refused: []types.MachineRefusal{{At: at, PID: 3, Project: ".", Target: "ci", Slots: 4, Owner: "spawned by root", Fits: true,
			Holders: []types.MachineClaimant{holder}}},
	})
	require.NoError(t, err)
	const holderJSON = `{"project": ".", "target": "go-test", "pid": 1, "slots": 8, "owner": "job-a", "since": "2026-10-24T09:40:55Z"}`
	assert.JSONEq(t, fmt.Sprintf(`{
		"budget_slots": 10, "held_slots": 8,
		"holders": [%[1]s],
		"waiting": [{"pid": 2, "project": ".", "target": "lint", "slots": 1, "dir": "/w", "owner": "unleased", "since": "2026-10-24T09:40:55Z", "waited_ms": 1500}],
		"refused": [{"at": "2026-10-24T09:40:55Z", "pid": 3, "project": ".", "target": "ci", "slots": 4, "owner": "spawned by root", "fits": true,
			"holders": [%[1]s]}]
	}`, holderJSON), string(b))
}

// StrangerQueueSuite pins the queue every claim that can fit waits in,
// whoever holds the room.
type StrangerQueueSuite struct {
	suite.Suite
	addr string
	// stranger holds 900 of 1000 MB under held.
	stranger *Client
	held     string
}

func TestStrangerQueueSuite(t *testing.T) { suite.Run(t, new(StrangerQueueSuite)) }

func (s *StrangerQueueSuite) SetupTest() {
	s.addr = testAddr(s.T())
	serve(s.T(), s.addr, WithCapacity(1000, 4))
	s.stranger = dial(s.T(), s.addr)
	v, err := s.stranger.Request(s.T().Context(), types.MachineClaim{Project: "x", Target: "build", MemoryMB: 900, PID: 1})
	s.Require().NoError(err)
	s.Require().True(v.Granted)
	s.held = v.ID
}

// park claims c on a client of its own, sending each parked verdict to frames.
func (s *StrangerQueueSuite) park(ctx context.Context, c types.MachineClaim, frames chan types.MachineVerdict) <-chan claimed {
	done := make(chan claimed, 1)
	cl := dial(s.T(), s.addr)
	go func() {
		v, err := cl.Claim(ctx, c, func(v types.MachineVerdict) { frames <- v })
		done <- claimed{v, err}
	}()
	return done
}

func (s *StrangerQueueSuite) TestAStrangerHeldClaimParksAndIsSeatedOnRelease() {
	frames := make(chan types.MachineVerdict, 8)
	done := s.park(s.T().Context(), types.MachineClaim{Project: ".", Target: "go-test", MemoryMB: 500}, frames)

	first := <-frames
	s.False(first.OwnRun, "another invocation keeps it out")
	s.Require().Len(first.Holders, 1)
	s.Equal(1, first.Holders[0].PID)
	requirePending(s.T(), done)

	s.stranger.Release(s.T().Context(), s.held)
	got := requireAnswer(s.T(), done)
	s.Require().NoError(got.err)
	s.True(got.v.Granted)
}

func (s *StrangerQueueSuite) TestAHolderChangeResendsOneParkedFrameAndNoChangeSendsNone() {
	frames := make(chan types.MachineVerdict, 8)
	done := s.park(s.T().Context(), types.MachineClaim{Project: ".", Target: "go-test", MemoryMB: 500}, frames)
	s.Len((<-frames).Holders, 1)

	other := dial(s.T(), s.addr)
	v, err := other.Request(s.T().Context(), types.MachineClaim{Project: "y", Target: "lint", MemoryMB: 50, PID: 2})
	s.Require().NoError(err)
	s.Require().True(v.Granted)
	s.Len((<-frames).Holders, 2, "a new holder is news")

	ctx, cancel := context.WithCancel(s.T().Context())
	quiet := s.park(ctx, types.MachineClaim{Project: "z", Target: "test", MemoryMB: 900}, make(chan types.MachineVerdict, 8))
	s.Eventually(func() bool { return len(waiters(s.T(), s.addr)) == 2 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	s.Error(requireAnswer(s.T(), quiet).err)
	s.Never(func() bool { return len(frames) > 0 }, 200*time.Millisecond, 10*time.Millisecond,
		"a wake that changed no holder sends no frame")
	requirePending(s.T(), done)
}

func (s *StrangerQueueSuite) TestAParkedStrangerWaitIsNoRefusal() {
	frames := make(chan types.MachineVerdict, 8)
	done := s.park(s.T().Context(), types.MachineClaim{Project: ".", Target: "go-test", MemoryMB: 500}, frames)
	<-frames

	st, err := QueryStatus(s.T().Context(), s.addr)
	s.Require().NoError(err)
	s.Empty(st.Capacity.Refused, "a wait is not a refusal")
	s.Len(st.Capacity.Waiting, 1, "status lists it as waiting")
	requirePending(s.T(), done)
}

func (s *StrangerQueueSuite) TestAClaimLargerThanTheBudgetIsRefusedAtOnce() {
	got := requireAnswer(s.T(), s.park(s.T().Context(), types.MachineClaim{Project: ".", Target: "huge", MemoryMB: 2000}, nil))

	s.Require().NoError(got.err)
	s.False(got.v.Fits)
	s.False(got.v.Granted)
	st, err := QueryStatus(s.T().Context(), s.addr)
	s.Require().NoError(err)
	s.Len(st.Capacity.Refused, 1, "a claim that can never fit is the one refusal")
}

func (s *StrangerQueueSuite) TestAProtocol4ClientIsRefusedAtHello() {
	conn, err := endpoint.DialUnix(s.T().Context(), s.addr[len("unix://"):])
	s.Require().NoError(err)
	defer func() { _ = conn.Close() }()
	r, w := newFrameReader(conn), &frameWriter{w: conn}
	_, err = r.read()
	s.Require().NoError(err)
	s.Require().NoError(w.write(typeHello, 1, hello{Magic: helloMagic, Protocol: 4}))
	f, err := r.read()
	s.Require().NoError(err)
	var er errorReply
	s.Require().NoError(decodeBody(f, &er))
	s.Equal(CodeProtocol, er.Code, "a protocol 4 client would wait unbounded")
}

type ParkedFrameSuite struct {
	suite.Suite
}

func TestParkedFrameSuite(t *testing.T) { suite.Run(t, new(ParkedFrameSuite)) }

func (s *ParkedFrameSuite) TestAFrameOlderThanOneAlreadyWrittenIsDropped() {
	var out bytes.Buffer
	p := &parked{w: &frameWriter{w: &out}, id: 9}
	stale, fresh := p.nextFrameLocked(), p.nextFrameLocked()

	p.send(fresh, typeParked, parkedReply{Verdict: types.MachineVerdict{HeldSlots: 2}})
	p.send(stale, typeParked, parkedReply{Verdict: types.MachineVerdict{HeldSlots: 1}})

	r := newFrameReader(&out)
	f, err := r.read()
	s.Require().NoError(err)
	var got parkedReply
	s.Require().NoError(decodeBody(f, &got))
	s.Equal(2, got.Verdict.HeldSlots)
	_, err = r.read()
	s.Error(err, "the stale frame never went out")
}

func (s *ParkedFrameSuite) TestTheKeyCoversWhatTheClientPrints() {
	base := parkedReply{Verdict: types.MachineVerdict{Fits: true, BudgetMB: 1000, HeldMB: 500, BudgetSlots: 4, HeldSlots: 2,
		Holders: []types.MachineClaimant{{PID: 7, Project: ".", Target: "lint", Slots: 1, MemoryMB: 100}}}}
	more := base
	more.Verdict.HeldMB = 600
	s.NotEqual(base.key(), more.key(), "remaining memory")
	more = base
	more.Verdict.HeldSlots = 3
	s.NotEqual(base.key(), more.key(), "remaining slots")
	more = base
	more.Verdict.Holders = []types.MachineClaimant{{PID: 7, Project: ".", Target: "lint", Slots: 2, MemoryMB: 100}}
	s.NotEqual(base.key(), more.key(), "holder slots")
}
