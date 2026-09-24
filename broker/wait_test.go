// cross-cutting: a real Client waiting in line on a real Serve over a socket

package broker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// waitAsync runs c.Wait on its own goroutine and reports its answer.
func waitAsync(ctx context.Context, c *Client, claim types.MachineClaim, onWait func(types.MachineWait)) <-chan waitResult {
	done := make(chan waitResult, 1)
	go func() {
		v, err := c.Wait(ctx, claim, onWait)
		done <- waitResult{v, err}
	}()
	return done
}

type waitResult struct {
	v   types.MachineVerdict
	err error
}

// awaitLine polls status until n claims wait in line, and is safe off the test
// goroutine.
func awaitLine(addr string, n int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := QueryStatus(context.Background(), addr)
		if err == nil && len(st.Waiting) == n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// requireWaiting polls status until n claims wait in line, so a test orders arrivals.
func requireWaiting(t *testing.T, addr string, n int) []types.MachineWait {
	t.Helper()
	var st types.StatusBroker
	require.Eventually(t, func() bool {
		var err error
		st, err = QueryStatus(t.Context(), addr)
		return err == nil && len(st.Waiting) == n
	}, 5*time.Second, 5*time.Millisecond, "want %d waiting", n)
	return st.Waiting
}

func requireSeated(t *testing.T, done <-chan waitResult) types.MachineVerdict {
	t.Helper()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.True(t, r.v.Granted, "seated: %+v", r.v)
		return r.v
	case <-time.After(5 * time.Second):
		t.Fatal("never seated")
		return types.MachineVerdict{}
	}
}

func requirePending(t *testing.T, done <-chan waitResult) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("answered while it should wait: %+v %v", r.v, r.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func claim(project string, pid, slots, mb int) types.MachineClaim {
	return types.MachineClaim{Project: project, Target: "test", PID: pid, Slots: slots, MemoryMB: mb}
}

func TestTheLineSeatsWaitersInArrivalOrder(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	holder := dial(t, addr)
	v, err := holder.Request(t.Context(), claim("h", 1, 1, 0))
	require.NoError(t, err)
	require.True(t, v.Granted)

	var dones []<-chan waitResult
	var clients []*Client
	for i, project := range []string{"first", "second", "third"} {
		c := dial(t, addr)
		clients = append(clients, c)
		dones = append(dones, waitAsync(t.Context(), c, claim(project, 10+i, 1, 0), nil))
		waiting := requireWaiting(t, addr, i+1)
		assert.Equal(t, project, waiting[i].Claim.Project)
		assert.Equal(t, i+1, waiting[i].Position)
	}

	holder.Release(t.Context(), v.ID)
	for i, done := range dones {
		got := requireSeated(t, done)
		for _, later := range dones[i+1:] {
			requirePending(t, later)
		}
		clients[i].Release(t.Context(), got.ID)
	}
	assert.Empty(t, requireWaiting(t, addr, 0))
}

// TestAWaiterHearsEachHolderChangeAndNothingElse pins graybeard Q2: a report when the
// wait starts and one each time the holders change, never one on a timer.
func TestAWaiterHearsEachHolderChangeAndNothingElse(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 2))
	a, b, c := dial(t, addr), dial(t, addr), dial(t, addr)
	va, err := a.Request(t.Context(), claim("a", 1, 1, 0))
	require.NoError(t, err)
	vb, err := b.Request(t.Context(), claim("b", 2, 1, 0))
	require.NoError(t, err)

	var mu sync.Mutex
	var heard [][]int
	onWait := func(w types.MachineWait) {
		pids := []int{}
		for _, h := range w.BlockedBy {
			pids = append(pids, h.PID)
		}
		mu.Lock()
		heard = append(heard, pids)
		mu.Unlock()
	}
	reports := func() [][]int {
		mu.Lock()
		defer mu.Unlock()
		return append([][]int(nil), heard...)
	}
	waiter := dial(t, addr)
	done := waitAsync(t.Context(), waiter, claim("w", 9, 2, 0), onWait)
	require.Eventually(t, func() bool { return len(reports()) == 1 }, 5*time.Second, time.Millisecond)

	b.Release(t.Context(), vb.ID)
	require.Eventually(t, func() bool { return len(reports()) == 2 }, 5*time.Second, time.Millisecond)
	vc, err := c.Request(t.Context(), claim("c", 3, 1, 0))
	require.NoError(t, err)
	require.True(t, vc.Granted)
	require.Eventually(t, func() bool { return len(reports()) == 3 }, 5*time.Second, time.Millisecond)

	_, err = QueryStatus(t.Context(), addr)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, [][]int{{1, 2}, {1}, {1, 3}}, reports(), "one report per holder set, none on a timer or a status read")

	a.Release(t.Context(), va.ID)
	c.Release(t.Context(), vc.ID)
	requireSeated(t, done)
}

func TestAClosedConnectionLeavesTheLine(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	holder := dial(t, addr)
	v, err := holder.Request(t.Context(), claim("h", 1, 1, 0))
	require.NoError(t, err)

	gone := dial(t, addr)
	goneDone := waitAsync(t.Context(), gone, claim("gone", 2, 1, 0), nil)
	requireWaiting(t, addr, 1)
	next := dial(t, addr)
	nextDone := waitAsync(t.Context(), next, claim("next", 3, 1, 0), nil)
	requireWaiting(t, addr, 2)

	require.NoError(t, gone.Close())
	r := <-goneDone
	require.ErrorIs(t, r.err, ErrUnavailable)
	waiting := requireWaiting(t, addr, 1)
	assert.Equal(t, "next", waiting[0].Claim.Project, "EOF took its place, and the one behind moved up")
	assert.Equal(t, 1, waiting[0].Position)

	holder.Release(t.Context(), v.ID)
	requireSeated(t, nextDone)
}

func TestAWaitCtxEndsLeavesNothingBehind(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	holder := dial(t, addr)
	v, err := holder.Request(t.Context(), claim("h", 1, 1, 0))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	waiter := dial(t, addr)
	_, err = waiter.Wait(ctx, claim("w", 2, 1, 0), nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	requireWaiting(t, addr, 0)

	holder.Release(t.Context(), v.ID)
	assert.Empty(t, holders(t, addr), "nothing was seated for a waiter that left")
}

func TestAcquireGivesUpAtItsBoundNamingTheHolder(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	holder := dial(t, addr)
	_, err := holder.Request(t.Context(), claim("h", 1, 1, 0))
	require.NoError(t, err)
	c := dial(t, addr)

	_, err = c.Acquire(t.Context(), claim("w", 2, 1, 0))
	var we *WaitError
	require.ErrorAs(t, err, &we, "no WithWait refuses at once, as MGS3009 does")
	assert.Zero(t, we.Waited)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	var reports int
	started := time.Now()
	_, err = c.Acquire(t.Context(), claim("w", 2, 1, 0), WithWait(150*time.Millisecond),
		WithWaitFunc(func(types.MachineWait) { reports++ }))
	require.ErrorAs(t, err, &we)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, time.Since(started), 150*time.Millisecond)
	assert.GreaterOrEqual(t, we.Waited, 150*time.Millisecond)
	require.Len(t, we.Holders, 1)
	assert.Equal(t, 1, we.Holders[0].PID)
	assert.Contains(t, err.Error(), "pid 1 (h test)")
	assert.Equal(t, 75, we.ExitCode())
	assert.Equal(t, 1, reports, "one report when it started waiting")
	requireWaiting(t, addr, 0)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		awaitLine(addr, 1)
		cancel()
	}()
	_, err = c.Acquire(ctx, claim("w", 2, 1, 0), WithWait(time.Minute))
	require.ErrorAs(t, err, &we)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestAcquireSeatsWithinItsBound(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 1))
	holder := dial(t, addr)
	v, err := holder.Request(t.Context(), claim("h", 1, 1, 0))
	require.NoError(t, err)
	go func() {
		awaitLine(addr, 1)
		holder.Release(context.Background(), v.ID)
	}()
	release, err := dial(t, addr).Acquire(t.Context(), claim("w", 2, 1, 0), WithWait(5*time.Second))
	require.NoError(t, err)
	require.Len(t, holders(t, addr), 1)
	release()
	release()
	assert.Empty(t, holders(t, addr), "release hands it back, once")

	_, err = dial(t, addr).Acquire(t.Context(), claim("huge", 3, 2, 0))
	var dnf *DoesNotFitError
	require.ErrorAs(t, err, &dnf)
	assert.Equal(t, 78, dnf.ExitCode())
}

// TestBackfillIsBounded pins the order: a claim that fits goes past a waiter that does
// not, until that waiter has been passed BackfillLimit times; then only the waiter's
// own run may pass it, and capacity drains toward it.
func TestBackfillIsBounded(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 0))
	holder := dial(t, addr)
	held, err := holder.Request(t.Context(), claim("held", 1, 1, 600))
	require.NoError(t, err)
	big := dial(t, addr)
	bigDone := waitAsync(t.Context(), big, claim("big", 2, 1, 800), nil)
	requireWaiting(t, addr, 1)

	small := dial(t, addr)
	for i := range cache.BackfillLimit {
		v, err := small.Request(t.Context(), claim("small", 100+i, 1, 10))
		require.NoError(t, err)
		require.True(t, v.Granted, "pass %d fits and goes past", i+1)
		small.Release(t.Context(), v.ID)
	}
	waiting := requireWaiting(t, addr, 1)
	assert.Equal(t, cache.BackfillLimit, waiting[0].PassedOver)

	v, err := small.Request(t.Context(), claim("small", 200, 1, 10))
	require.NoError(t, err)
	assert.False(t, v.Granted, "a waiter passed over enough times is a barrier")
	assert.True(t, v.Fits)
	assert.False(t, v.OwnRun)
	require.Len(t, v.Ahead, 1)
	assert.Equal(t, "big", v.Ahead[0].Project)

	own, err := small.Request(t.Context(), claim("big-sibling", 2, 1, 10))
	require.NoError(t, err)
	assert.True(t, own.Granted, "the barrier's own run is not held back by it")
	small.Release(t.Context(), own.ID)

	holder.Release(t.Context(), held.ID)
	requireSeated(t, bigDone)

	st, err := QueryStatus(t.Context(), addr)
	require.NoError(t, err)
	assert.Equal(t, cache.MachineOrder, st.Order)
	assert.Equal(t, cache.BackfillLimit, st.BackfillLimit)
}

// TestTwoRootsAtAFullBudgetDoNotDeadlock is graybeard's T3: two independent roots fill
// the host, each parent step blocked on a nested child that waits in line, and each
// root has more steps and a stranger waiting behind them. Every wait holds nothing, and
// every holder is a running step that ends, so everything finishes.
func TestTwoRootsAtAFullBudgetDoNotDeadlock(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(0, 4))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	type parent struct {
		root   string
		pid    int
		client *Client
		id     string
	}
	var parents []parent
	for i, root := range []string{"100:root-a", "200:root-b"} {
		pid := 100 * (i + 1)
		for range 2 {
			c := dial(t, addr)
			v, err := c.Request(ctx, types.MachineClaim{Project: root, Target: "ci", PID: pid, Invocation: root, Slots: 1})
			require.NoError(t, err)
			require.True(t, v.Granted, "the parents fill the host")
			parents = append(parents, parent{root: root, pid: pid, client: c, id: v.ID})
		}
	}
	require.Len(t, holders(t, addr), 4)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i, p := range parents {
		child := dial(t, addr)
		wg.Go(func() {
			c := types.MachineClaim{Project: p.root, Target: "nested", PID: 1000 + i, Slots: 1,
				Invocation: "child", Ancestors: []string{p.root}}
			v, err := child.Wait(ctx, c, nil)
			if err != nil || !v.Granted {
				errs <- errors.Join(err, errors.New("a nested child was never seated"))
				return
			}
			time.Sleep(20 * time.Millisecond)
			child.Release(ctx, v.ID)
			p.client.Release(ctx, p.id)
		})
	}
	for i, root := range []string{"100:root-a", "200:root-b", ""} {
		w := dial(t, addr)
		wg.Go(func() {
			pid := 100 * (i + 1)
			c := types.MachineClaim{Project: "more", Target: "step", PID: pid, Invocation: root, Slots: 2}
			v, err := w.Wait(ctx, c, nil)
			if err != nil || !v.Granted {
				errs <- errors.Join(err, errors.New("a waiter behind the roots was never seated"))
				return
			}
			w.Release(ctx, v.ID)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	require.NoError(t, ctx.Err(), "every wait finished before the deadline: no cycle")
	assert.Empty(t, holders(t, addr))
	requireWaiting(t, addr, 0)
}

// TestABrokerDyingUnderAWaiterIsUnavailableAndTheSuccessorTakesItAgain is the broker
// half of Q4: the line dies with its broker, the waiter hears ErrUnavailable, and a
// client with a starter joins the successor's line.
func TestABrokerDyingUnderAWaiterIsUnavailableAndTheSuccessorTakesItAgain(t *testing.T) {
	addr := testAddr(t)
	stop, _ := serve(t, addr, WithCapacity(0, 1))
	holder := dial(t, addr)
	_, err := holder.Request(t.Context(), claim("h", 1, 1, 0))
	require.NoError(t, err)

	var starts int
	waiter := NewClient(addr, WithStart(func(context.Context) bool {
		starts++
		serve(t, addr, WithCapacity(0, 1))
		return true
	}))
	t.Cleanup(func() { _ = waiter.Close() })
	done := waitAsync(t.Context(), waiter, claim("w", 2, 1, 0), nil)
	requireWaiting(t, addr, 1)

	stop()
	r := <-done
	require.ErrorIs(t, r.err, ErrUnavailable)
	// Hung up before the successor exists, so it cannot re-assert its claim there first.
	require.NoError(t, holder.Close())

	v, err := waiter.Wait(t.Context(), claim("w", 2, 1, 0), nil)
	require.NoError(t, err)
	assert.True(t, v.Granted, "the successor has no holder yet, so it seats the waiter")
	assert.Equal(t, 1, starts)
}
