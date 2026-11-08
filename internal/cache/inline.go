package cache

import (
	"context"

	"github.com/egladman/magus/types"
)

// RunInline runs fn, a ctx.needs member running inline in its composer,
// admitted as a step is: s's machine claim, its limiter slots and the
// inflight record. fn's processes cost s alone, never its composer.
// Nothing is keyed, replayed or journaled. The claim goes back last.
// A ctx still holding a claim rides it (see LendMachineClaim).
func (c *Cache) RunInline(ctx context.Context, s Step, fn func(context.Context) error, opts ...RunOption) error {
	rc := &runCtx{}
	for _, o := range opts {
		o(rc)
	}
	lim := rc.limiter
	if lim == nil {
		lim = NewLimiter(DefaultConcurrency())
	}
	ctx, releaseMachine, err := c.claimMachine(ctx, s, stepSlots(s, lim))
	if err != nil {
		if ctx.Err() == nil {
			emitRefusal(ctx, &s, err)
		}
		return err
	}
	defer releaseMachine()
	ctx, slots, release, err := c.admit(ctx, s, lim)
	if err != nil {
		return err
	}
	defer release()
	ctx = types.WithExecCost(ctx)
	watchOverrun(ctx)
	return runSeated(ctx, lim, slots, fn)
}
