package broker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

// leaveTimeout bounds taking a claim out of line once its wait has ended. The wait is
// already over, so this only decides whether a grant racing it is handed back promptly.
const leaveTimeout = 5 * time.Second

// pollWaitEvery paces waiting against a broker that predates the line, which has no
// grant to push and is asked again instead.
var pollWaitEvery = 100 * time.Millisecond

// Wait puts claim in the broker's line and blocks until the broker seats it, it turns
// out not to fit at all, or ctx ends. A granted verdict holds the claim on this
// connection until Release; a verdict with Fits false never will be granted. When ctx
// ends first, Wait leaves the line, hands back any grant that raced the leave, and
// returns ctx's error.
//
// onWait, when set, is called on Wait's goroutine when the claim starts waiting and
// again each time what keeps it out changes, never on a timer. It must not block for
// long: a grant is not read while it runs.
//
// The place in line rides this client's connection. A broker that goes away while the
// claim waits takes the line with it: Wait returns an error wrapping ErrUnavailable, and
// whether to wait again on its successor is the caller's call. Against a broker that
// predates the line, Wait asks again every 100 ms instead, in no order.
func (c *Client) Wait(ctx context.Context, claim types.MachineClaim, onWait func(types.MachineWait)) (types.MachineVerdict, error) {
	cn, err := c.open(ctx)
	if err != nil {
		return types.MachineVerdict{}, err
	}
	var (
		mu     sync.Mutex
		latest types.MachineWait
		signal = make(chan struct{}, 1)
	)
	onFrame := func(f frame) {
		var w types.MachineWait
		if decodeBody(f, &w) != nil {
			return
		}
		mu.Lock()
		latest = w
		mu.Unlock()
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	id := c.next()
	reply, err := cn.send(id, typeWait, claimRequest{Claim: claim}, onFrame)
	if err != nil {
		return types.MachineVerdict{}, err
	}
	finish := func(f frame) (types.MachineVerdict, error) {
		var r claimReply
		if err := decodeReply(f, typeWait, typeClaimReply, &r); err != nil {
			if errors.Is(err, ErrUnknownType) {
				return c.pollWait(ctx, claim, onWait)
			}
			return types.MachineVerdict{}, err
		}
		if r.Verdict.Granted {
			c.hold(claim, r.Verdict.ID)
		}
		return r.Verdict, nil
	}
	for {
		select {
		case <-signal:
			mu.Lock()
			w := latest
			mu.Unlock()
			if onWait != nil {
				onWait(w)
			}
		case f := <-reply:
			return finish(f)
		case <-cn.dead:
			select {
			case f := <-reply:
				return finish(f)
			default:
			}
			return types.MachineVerdict{}, fmt.Errorf("%w: the broker went away while %s %s waited in line", ErrUnavailable, claim.Project, claim.Target)
		case <-ctx.Done():
			c.leave(cn, id, reply)
			return types.MachineVerdict{}, ctx.Err()
		}
	}
}

// leave takes the claim.wait under waitID out of line. When the broker answers that it
// already seated it, the grant is in reply by then, since the broker wrote it first,
// and leave hands it straight back.
func (c *Client) leave(cn *conn, waitID uint64, reply <-chan frame) {
	ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancel()
	var lr leaveReply
	if err := cn.roundTrip(ctx, c.next(), typeLeave, leaveRequest{WaitID: waitID}, typeLeaveReply, &lr, nil); err != nil || lr.Left {
		cn.forget(waitID)
		return
	}
	select {
	case f := <-reply:
		var r claimReply
		if decodeReply(f, typeWait, typeClaimReply, &r) == nil && r.Verdict.Granted {
			_, _ = cn.call(ctx, c.next(), typeRelease, releaseRequest{ClaimID: r.Verdict.ID}, nil)
		}
	case <-cn.dead:
	case <-ctx.Done():
		cn.forget(waitID)
	}
}

// pollWait is Wait against a broker that predates the line: ask, and ask again, until
// the claim is seated or does not fit. onWait hears each change in who holds the host.
func (c *Client) pollWait(ctx context.Context, claim types.MachineClaim, onWait func(types.MachineWait)) (types.MachineVerdict, error) {
	since := time.Now()
	var last *types.MachineWait
	for {
		v, err := c.Request(ctx, claim)
		if err != nil || v.Granted || !v.Fits {
			return v, err
		}
		w := types.MachineWait{Claim: claim, Position: 1, Since: since, OwnRun: v.OwnRun, BlockedBy: v.Holders, Ahead: v.Ahead}
		if onWait != nil && (last == nil || !sameWaitReport(*last, w)) {
			onWait(w)
		}
		last = &w
		select {
		case <-ctx.Done():
			return types.MachineVerdict{}, ctx.Err()
		case <-time.After(pollWaitEvery):
		}
	}
}

// hold records a claim this client was granted, so a successor broker hears it
// re-asserted.
func (c *Client) hold(claim types.MachineClaim, id string) {
	c.mu.Lock()
	c.held[id] = &heldClaim{claim: claim, remote: id}
	c.mu.Unlock()
}

// sameWaitReport reports two states of one waiter a person would read as the same: the
// same claims keep it out, for the same reason. Its place in line is left out, since
// status shows that and a report per place would be a report per release.
func sameWaitReport(a, b types.MachineWait) bool {
	same := func(x, y types.MachineClaimant) bool {
		return x.PID == y.PID && x.Project == y.Project && x.Target == y.Target && x.Since.Equal(y.Since)
	}
	return a.OwnRun == b.OwnRun && slices.EqualFunc(a.BlockedBy, b.BlockedBy, same) && slices.EqualFunc(a.Ahead, b.Ahead, same)
}

// AcquireOption configures one Acquire.
type AcquireOption func(*acquireOptions)

type acquireOptions struct {
	wait   time.Duration
	onWait func(types.MachineWait)
}

// WithWait lets Acquire wait up to d in line behind other invocations. Without it, or
// with d <= 0, a claim kept out by another invocation fails at once with *WaitError.
// A claim kept out only by its own process or run waits for as long as ctx allows
// whatever d is, since what holds it is its own work finishing.
func WithWait(d time.Duration) AcquireOption {
	return func(o *acquireOptions) { o.wait = d }
}

// WithWaitFunc is called when the claim starts waiting and each time what keeps it out
// changes, never on a timer. It runs on Acquire's goroutine and must not block.
func WithWaitFunc(fn func(types.MachineWait)) AcquireOption {
	return func(o *acquireOptions) { o.onWait = fn }
}

// Acquire seats claim on the host's capacity and returns the function that hands it
// back. release is idempotent, safe to defer, and bounded: it never outlasts a wedged
// broker by more than a few seconds. Once granted, the claim is held until release or
// until this client's connection closes, whichever comes first; ctx bounds only the
// wait.
//
// A claim no state of the host can seat returns *DoesNotFitError. A claim kept out by
// other invocations past the WithWait bound, or at once without one, returns
// *WaitError naming who held the host; so does a wait ctx ends. Neither is retried
// here, and a broker that goes away mid-wait returns an error wrapping ErrUnavailable.
func (c *Client) Acquire(ctx context.Context, claim types.MachineClaim, opts ...AcquireOption) (release func(), err error) {
	var o acquireOptions
	for _, fn := range opts {
		fn(&o)
	}
	v, err := c.Request(ctx, claim)
	if err != nil {
		return nil, err
	}
	if v.Granted {
		return c.releaser(v.ID), nil
	}
	if !v.Fits {
		return nil, &DoesNotFitError{Claim: claim, Verdict: v}
	}
	if !v.OwnRun && o.wait <= 0 {
		return nil, &WaitError{Claim: claim, Holders: v.Holders, Err: context.DeadlineExceeded}
	}

	started := time.Now()
	wctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var (
		mu      sync.Mutex
		ownRun  = v.OwnRun
		expired bool
		holders = v.Holders
	)
	if o.wait > 0 {
		t := time.AfterFunc(o.wait, func() {
			mu.Lock()
			defer mu.Unlock()
			expired = true
			if !ownRun {
				cancel(context.DeadlineExceeded)
			}
		})
		defer t.Stop()
	}
	onWait := func(w types.MachineWait) {
		mu.Lock()
		ownRun, holders = w.OwnRun, w.BlockedBy
		if !ownRun && (expired || o.wait <= 0) {
			cancel(context.DeadlineExceeded)
		}
		mu.Unlock()
		if o.onWait != nil {
			o.onWait(w)
		}
	}
	v, err = c.Wait(wctx, claim, onWait)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(context.Cause(wctx), context.DeadlineExceeded) {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		cause := ctx.Err()
		if cause == nil {
			cause = context.DeadlineExceeded
		}
		return nil, &WaitError{Claim: claim, Holders: holders, Waited: time.Since(started), Err: cause}
	}
	if !v.Granted {
		return nil, &DoesNotFitError{Claim: claim, Verdict: v}
	}
	return c.releaser(v.ID), nil
}

// releaser returns id's claim at most once, on a fresh bounded context: the caller's own
// is often cancelled by the time it lets go.
func (c *Client) releaser(id string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
			defer cancel()
			c.Release(ctx, id)
		})
	}
}
