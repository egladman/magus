package broker

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// waiters is the broker's parked requests as its status reply lists them, with the
// moments cleared.
func waiters(t *testing.T, addr string) []types.MachineWaiter {
	t.Helper()
	st, err := QueryStatus(t.Context(), addr)
	require.NoError(t, err)
	for i := range st.Waiting {
		st.Waiting[i].Since, st.Waiting[i].WaitedMS = time.Time{}, 0
	}
	return st.Waiting
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
	assert.Equal(t, []types.MachineWaiter{{Kind: "claim", Project: "docs", Target: "lint", PID: os.Getpid(), MemoryMB: 900, Slots: 1, Dir: "/w", Command: waiters(t, addr)[0].Command}},
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
	assert.True(t, second.seatWaiting.Load(), "the parked reply names the seat's holder")

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
