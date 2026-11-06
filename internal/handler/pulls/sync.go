package pulls

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/log/attr"
	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

// ListFunc reads the open pulls. bindings.ListPulls is the production one.
type ListFunc func(ctx context.Context, req types.ListPullsRequest) (types.PullList, error)

// PollerOptions wires a Poller. Every field but Log is required.
type PollerOptions struct {
	Config config.Pulls
	Store  Store
	List   ListFunc
	// Remote returns the workspace's remote, empty when it has none.
	Remote func(ctx context.Context) string
	// Base is the branch pulls target.
	Base string
	// BaseNote is added to the cadence reason when Base was not configured.
	BaseNote string
	Now      func() time.Time
	Log      *slog.Logger
}

// Poller syncs the forge on a cadence and keeps the last good list.
type Poller struct {
	opts PollerOptions
	kick chan struct{}

	mu       sync.Mutex
	list     types.PullList
	haveList bool
	viewer   string
	state    pullsv1.SyncState
	attempt  time.Time
	// isHeld is true while the next sync waits for the rate limit's reset.
	isHeld bool
	// failures counts syncs that failed since the last good one.
	failures int
}

// maxFailureBackoff caps the wait after repeated failures.
const maxFailureBackoff = 30 * time.Minute

// NewPoller returns a Poller that has not synced yet.
func NewPoller(opts PollerOptions) *Poller {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Poller{opts: opts, kick: make(chan struct{}, 1)}
}

// Kick asks for one sync now. The poll floor and a rate-limit hold still apply.
func (p *Poller) Kick() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// Run syncs until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-p.kick:
			if wait := p.kickWait(); wait > 0 {
				timer.Reset(wait)
				continue
			}
		}
		p.Sync(ctx)
		timer.Reset(p.nextWait())
	}
}

func (p *Poller) kickWait() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.attempt.IsZero() {
		return 0
	}
	now := p.opts.Now()
	wait := p.attempt.Add(p.opts.Config.Floor).Sub(now)
	if p.isHeld {
		wait = max(wait, p.state.NextSyncAt.AsTime().Sub(now))
	}
	return max(wait, 0)
}

func (p *Poller) nextWait() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(p.state.NextSyncAt.AsTime().Sub(p.opts.Now()), p.opts.Config.Floor)
}

// Sync reads the forge once. A failure keeps the last good list and
// records why on the sync state.
func (p *Poller) Sync(ctx context.Context) {
	now := p.opts.Now()
	filter, ferr := p.opts.Store.Filter()
	p.mu.Lock()
	viewer := p.viewer
	p.mu.Unlock()

	var list types.PullList
	err := ferr
	if err == nil {
		list, err = p.read(ctx, filter, viewer)
	}

	if err != nil && ctx.Err() != nil {
		// Shutdown cut the read short; the last good state still stands.
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempt = now
	if err != nil {
		p.failures++
		p.opts.Log.WarnContext(ctx, "pulls sync failed", attr.Error(err), slog.Int("failures", p.failures))
		p.state.SyncError = err.Error()
		p.state.FailedAt = toTimestamp(now)
	} else {
		p.failures = 0
		p.list, p.haveList = list, true
		if list.Viewer != "" {
			p.viewer = list.Viewer
		}
		p.state.SyncedAt = toTimestamp(now)
		p.state.SyncError, p.state.FailedAt = "", nil
		p.state.Requests, p.state.Revalidated = int32(list.Requests), int32(list.Revalidated)
		p.state.RateLimit, p.state.RateRemaining = int32(list.Rate.Limit), int32(list.Rate.Remaining)
		p.state.RateReset = toTimestamp(parseTime(list.Rate.Reset))
	}
	c := chooseCadence(p.opts.Config, p.list, now)
	if wait := failureBackoff(p.opts.Config.Floor, p.failures); wait > c.interval {
		c = cadence{
			interval: wait,
			reason:   fmt.Sprintf("%s failed in a row: retrying in %s", plural(p.failures, "sync", "syncs"), formatDuration(wait)),
		}
	}
	p.isHeld = c.isHold
	if p.opts.BaseNote != "" {
		c.reason += "; " + p.opts.BaseNote
	}
	p.state.Interval = toDuration(c.interval)
	p.state.CadenceReason = c.reason
	p.state.NextSyncAt = toTimestamp(now.Add(c.interval))
}

func (p *Poller) read(ctx context.Context, f authorFilter, viewer string) (types.PullList, error) {
	remote := p.opts.Remote(ctx)
	if remote == "" {
		return types.PullList{}, errors.New("pulls: the workspace has no remote to read pull requests from")
	}
	list, err := p.opts.List(ctx, types.ListPullsRequest{
		Remote:  remote,
		Base:    p.opts.Base,
		Authors: f.Effective(viewer),
	})
	if err != nil {
		return types.PullList{}, fmt.Errorf("pulls: list: %w", err)
	}
	if list.Reason != "" {
		return types.PullList{}, fmt.Errorf("pulls: nothing read: %s", list.Reason)
	}
	return list, nil
}

// Board builds the board from the last good list under the filter as it
// stands now. It never calls the forge.
func (p *Poller) Board() (*pullsv1.Board, error) {
	filter, err := p.opts.Store.Filter()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	board := analyseBoard(boardInput{List: p.list, Now: now, Config: p.opts.Config, Filter: filter})
	if !p.haveList {
		board.Filter = filter.proto(p.viewer)
	}
	s := &pullsv1.SyncState{
		SyncedAt:      p.state.SyncedAt,
		NextSyncAt:    p.state.NextSyncAt,
		FailedAt:      p.state.FailedAt,
		Interval:      p.state.Interval,
		CadenceReason: p.state.CadenceReason,
		SyncError:     p.state.SyncError,
		RateLimit:     p.state.RateLimit,
		RateRemaining: p.state.RateRemaining,
		RateReset:     p.state.RateReset,
		Requests:      p.state.Requests,
		Revalidated:   p.state.Revalidated,
	}
	if !p.haveList && s.CadenceReason == "" {
		s.CadenceReason = "waiting for the first sync"
	}
	board.Sync = s
	return board, nil
}

type cadence struct {
	interval time.Duration
	reason   string
	// isHold is true when the interval waits for the rate limit's reset.
	isHold bool
}

// failureBackoff doubles the floor per failure since the last good sync,
// up to maxFailureBackoff. Zero failures wait nothing extra.
func failureBackoff(floor time.Duration, failures int) time.Duration {
	if failures == 0 {
		return 0
	}
	wait := floor
	for range failures - 1 {
		if wait >= maxFailureBackoff/2 {
			return maxFailureBackoff
		}
		wait *= 2
	}
	return min(wait, maxFailureBackoff)
}

// chooseCadence picks the wait before the next sync. Near the request
// reserve it waits for the budget's reset; otherwise it polls faster
// while checks run. Nothing polls faster than the floor.
func chooseCadence(cfg config.Pulls, list types.PullList, now time.Time) cadence {
	rate := list.Rate
	if reset := parseTime(rate.Reset); rate.Limit > 0 && rate.Remaining < cfg.Reserve && reset.After(now) {
		return cadence{
			interval: max(reset.Sub(now), cfg.Floor),
			reason: fmt.Sprintf("%d requests left of %d: waiting until %s reset",
				rate.Remaining, rate.Limit, reset.Format("15:04")),
			isHold: true,
		}
	}
	interval, reason := cfg.Idle, "nothing running: backed off to %s"
	if anyPending(list, now) {
		interval, reason = cfg.Active, "checks running: every %s"
	}
	if interval < cfg.Floor {
		interval, reason = cfg.Floor, reason+" (the poll floor)"
	}
	return cadence{interval: interval, reason: fmt.Sprintf(reason, formatDuration(interval))}
}

func anyPending(list types.PullList, now time.Time) bool {
	if rollupChecks(list.Base.Checks, now).pending > 0 {
		return true
	}
	for _, p := range list.Pulls {
		if rollupChecks(p.Checks, now).pending > 0 {
			return true
		}
	}
	return false
}
