package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Validator is the read-only step of a queue run. It executes the changes' code (the
// gate and the regeneration run on candidates) and so must never hold a credential that
// can write. It calls no [Provider] at all.
type Validator struct {
	// Only, when set, validates that one change: the changes beneath it in its
	// partition are merged under it but not gated, since their own runs gate them.
	// This is how a CI system spreads one plan's candidates over separate jobs.
	Only string
	// Parallel caps the candidates being built or gated at once across every partition,
	// so many disjoint partitions do not each start Depth builds. Zero means
	// runtime.NumCPU().
	Parallel int
	// Regenerate rewrites the generated files a change touches or conflicts in on each
	// candidate, and proves that a merge of the base into a change reproduces its
	// generated files. Nil leaves them as merged, which is only right when nothing
	// generates them.
	Regenerate RegenerateFunc
	// Scratch is the directory candidates are checked out under. Required, and outside
	// the repository, where a checkout would be discovered as a second copy of it.
	Scratch string
	Events  *Events

	vcs  VCS
	gate Gate
	sink VerdictSink
}

// NewValidator builds candidates with v, gates them with gate, and hands each verdict
// to sink the moment it is decided, so an Applier can start on it while later
// candidates still run.
func NewValidator(v VCS, gate Gate, sink VerdictSink) *Validator {
	return &Validator{vcs: v, gate: gate, sink: sink}
}

// Run validates plan's admitted changes, or only v.Only. Partitions run side by side,
// and an error in one stops that partition alone: the verdicts the others reach are
// still sound, and what no verdict reached waits for the next run.
func (v *Validator) Run(ctx context.Context, plan Plan) error {
	switch {
	case v.vcs == nil || v.gate == nil || v.sink == nil:
		return errors.New("a Validator needs a VCS, a Gate and a VerdictSink; build it with NewValidator")
	case v.Scratch == "":
		return errors.New("a Validator needs a Scratch directory to check candidates out in")
	case v.Parallel < 0:
		return fmt.Errorf("parallel %d must not be negative", v.Parallel)
	}
	if err := plan.check(); err != nil {
		return err
	}
	n := v.Parallel
	if n == 0 {
		n = runtime.NumCPU()
	}
	r := &validation{Validator: v, plan: plan, slots: make(chan struct{}, n)}
	if v.Only != "" {
		return r.only(ctx)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(plan.Partitions))
	for gi, g := range plan.Partitions {
		wg.Go(func() { errs[gi] = r.pipeline(ctx, gi, g) })
	}
	wg.Wait()
	return errors.Join(errs...)
}

type validation struct {
	*Validator
	plan  Plan
	slots chan struct{} // one per candidate being built or gated
}

type flight struct {
	change   Change
	cand     Candidate // zero when building it was refused
	onto     string
	after    string // change beneath it, "" at the bottom
	reviewed string // what a review of its head covers, when proving that took regeneration
	depth    int
	cancel   context.CancelFunc
	done     chan outcome
}

type outcome struct {
	green   bool
	summary string
	refused *RefusedError // building the candidate refused the change rather than a red gate
	err     error
	took    time.Duration
}

// pipeline runs one partition's speculative candidates. Up to Depth are in flight, each
// built onto the one below; their gates run concurrently and are consumed in order.
// When the lowest is green its change is decided and the window slides up; when it is
// red its change is the culprit (everything beneath it validated), so it is kicked back
// and every candidate above, all built onto it, is rebuilt onto what did validate. A
// refused candidate is red the same way, and is attributed only once it is lowest. A
// change stacked on one that is not green this run waits, and is never built onto what
// lacks the change beneath it.
func (r *validation) pipeline(ctx context.Context, group int, pending []Change) error {
	onto, ontoID := r.plan.BaseCommit, ""
	held := map[string]stackHold{} // changes not green this run: what their stacks wait with
	var inflight []*flight
	defer func() {
		for _, f := range inflight {
			r.ground(ctx, f)
		}
	}()
	for {
		for len(inflight) < r.plan.Depth && len(pending) > 0 {
			c := pending[0]
			pending = pending[1:]
			if h, ok := held[c.Below]; ok && c.Below != "" {
				held[c.ID] = h.above(c.ID)
				if err := r.decide(ctx, Verdict{Change: c, Decision: DecisionWait, Code: h.code, Reason: h.reason}); err != nil {
					return err
				}
				continue
			}
			f := &flight{change: c, onto: onto, after: ontoID, depth: len(inflight) + 1, done: make(chan outcome, 1)}
			for _, below := range slices.Backward(inflight) {
				if below.cand.Commit != "" { // a refused candidate is nothing to build onto
					f.onto, f.after = below.cand.Commit, below.change.ID
					break
				}
			}
			queued, err := r.launch(ctx, group, f)
			if err != nil {
				return err
			}
			if queued {
				inflight = append(inflight, f)
			} else {
				held[c.ID] = waitingBelow(c.ID)
			}
		}
		if len(inflight) == 0 {
			return nil
		}
		head := inflight[0]
		inflight = inflight[1:]
		out := <-head.done
		head.cancel()
		r.discard(ctx, head.cand)
		if out.err != nil {
			return fmt.Errorf("gate %s: %w", head.change.Label(), out.err)
		}
		green, err := r.verdict(ctx, head, out, true)
		if err != nil {
			return err
		}
		if !green {
			held[head.change.ID] = stackHold{code: CodeParentKicked, reason: kickedBelow(head.change.ID)}
		}
		if !green && head.cand.Commit == "" {
			continue // nothing above was built onto a refused candidate
		}
		if !green {
			requeue := make([]Change, 0, len(inflight)+len(pending))
			for _, f := range inflight {
				r.ground(ctx, f)
				requeue = append(requeue, f.change)
			}
			inflight = nil
			pending = append(requeue, pending...)
			continue
		}
		onto, ontoID = head.cand.Commit, head.change.ID
	}
}

// stackHold is the wait of a change stacked on one that is not green this run.
type stackHold struct {
	code   Code
	reason string
}

func waitingBelow(id string) stackHold {
	return stackHold{code: CodeParent, reason: "stacked on #" + id + ", which did not validate this run"}
}

// above is the wait of what is stacked on id, which waits with h: a kick-back further
// down stays the reason, anything else names id.
func (h stackHold) above(id string) stackHold {
	if h.code == CodeParentKicked {
		return h
	}
	return waitingBelow(id)
}

// launch builds f's candidate and starts its gate, reporting whether f joined the
// flights. A conflict or a wait is decided on the spot and leaves the window; a refused
// candidate joins with its red outcome already in hand.
func (r *validation) launch(ctx context.Context, group int, f *flight) (bool, error) {
	if err := r.acquire(ctx); err != nil {
		return false, err
	}
	cand, reviewed, err := r.candidate(ctx, f.onto, f.change)
	if err == nil {
		f.cand, f.reviewed = cand, reviewed
		r.start(ctx, group, f)
		return true, nil
	}
	r.release()
	var refused *RefusedError
	if errors.As(err, &refused) {
		f.cancel = func() {}
		f.done <- outcome{refused: refused, summary: refused.Reason}
		return true, nil
	}
	return false, r.hold(ctx, f, err)
}

// hold decides a wait for a build error that says nothing against the change, and
// returns the error as it stands when it is the machine's.
func (r *validation) hold(ctx context.Context, f *flight, err error) error {
	var wait *WaitError
	switch conf, ok := asConflict(err); {
	case ok:
		// Planning proved it merges onto the base alone, so it conflicts with a change
		// ahead of it. The next run sees that change merged.
		return r.decide(ctx, Verdict{Change: f.change, Decision: DecisionWait, Code: CodeConflictAhead,
			Reason: conflictAhead(f.after, conf), Paths: conf.Paths})
	case errors.As(err, &wait):
		return r.decide(ctx, Verdict{Change: f.change, Decision: DecisionWait, Code: wait.Code, Reason: wait.Reason})
	}
	return fmt.Errorf("build the candidate of %s onto %s: %w", f.change.Label(), short(f.onto), err)
}

// only builds v.Only's chain one candidate at a time and gates just the top. What is
// red on top of changes this run did not gate cannot be pinned on the top change, so it
// waits for a run where they are validated rather than kicking its author back.
func (r *validation) only(ctx context.Context) error {
	gi, pos, ok := r.plan.Find(r.Only)
	if !ok {
		return fmt.Errorf("%s is not an admitted change of the plan", r.Only)
	}
	onto, after := r.plan.BaseCommit, ""
	var built []Candidate
	defer func() {
		for _, cand := range built {
			r.discard(ctx, cand)
		}
	}()
	skipped := map[string]bool{}
	for i, c := range r.plan.Partitions[gi][:pos+1] {
		f := &flight{change: c, onto: onto, after: after, depth: len(built) + 1, done: make(chan outcome, 1)}
		if skipped[c.Below] {
			if i < pos {
				skipped[c.ID] = true
				continue
			}
			h := waitingBelow(c.Below)
			return r.decide(ctx, Verdict{Change: c, Decision: DecisionWait, Code: h.code, Reason: h.reason})
		}
		cand, reviewed, err := r.candidate(ctx, onto, c)
		if err != nil {
			if i < pos {
				skipped[c.ID] = true
				continue // its own run decides it; the chain skips it, as the pipeline does
			}
			var refused *RefusedError
			if errors.As(err, &refused) {
				_, err := r.verdict(ctx, f, outcome{refused: refused, summary: refused.Reason}, len(built) == 0)
				return err
			}
			return r.hold(ctx, f, err)
		}
		built = append(built, cand)
		if i < pos {
			onto, after = cand.Commit, c.ID
			continue
		}
		f.cand, f.reviewed = cand, reviewed
		if err := r.acquire(ctx); err != nil {
			return err
		}
		r.start(ctx, gi, f)
		out := <-f.done
		f.cancel()
		if out.err != nil {
			return fmt.Errorf("gate %s: %w", c.Label(), out.err)
		}
		_, err = r.verdict(ctx, f, out, f.depth == 1)
		return err
	}
	return nil
}

// candidate proves what a review of c's head covers, then builds c's candidate onto
// onto. It returns the review target when proving it took regeneration.
func (r *validation) candidate(ctx context.Context, onto string, c Change) (Candidate, string, error) {
	if err := fetchHead(ctx, r.vcs, c); err != nil {
		return Candidate{}, "", fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	reviewed, err := r.proveReview(ctx, c)
	if err != nil {
		return Candidate{}, "", err
	}
	cand, err := buildCandidate(ctx, r.vcs, candidateSpec{baseCommit: r.plan.BaseCommit, onto: onto, change: c,
		scratch: r.Scratch, regenerate: r.Regenerate})
	return cand, reviewed, err
}

// proveReview regenerates, in a checkout of each merge of the base into c that differs
// from the plain merge only in generated files, the files it differs in. A merge whose
// regeneration rewrites nothing adds nothing a reviewer did not see; any other leaves c
// waiting for an approval at its head.
func (r *validation) proveReview(ctx context.Context, c Change) (string, error) {
	target, owed, err := reviewTarget(ctx, r.vcs, r.plan.BaseCommit, r.plan.BaseCommit, c.Head, c.StackBase)
	if err != nil || len(owed) == 0 {
		return "", err
	}
	if r.Regenerate == nil {
		return "", &WaitError{Code: CodeNotApproved, Reason: "not approved at " + short(owed[0].Commit) + ": it differs from its merge in generated files, " +
			"and proving regeneration reproduces them needs a regenerate hook"}
	}
	for _, ob := range owed {
		written, err := r.regenerateAt(ctx, c, ob)
		if err != nil {
			return "", err
		}
		if len(written) > 0 {
			return "", &WaitError{Code: CodeNotApproved, Reason: "not approved at " + short(ob.Commit) + ": " + joinPaths(written) +
				" are marked generated, but regeneration does not reproduce them, so no review covers them"}
		}
	}
	return target, nil
}

func (r *validation) regenerateAt(ctx context.Context, c Change, ob regenerationProof) ([]string, error) {
	dir := filepath.Join(r.Scratch, fmt.Sprintf("review-%d-%d-%s", os.Getpid(), checkoutSeq.Add(1), c.ID))
	if err := r.vcs.CreateCheckout(ctx, dir, ob.Commit); err != nil {
		return nil, err
	}
	defer r.discard(ctx, Candidate{Dir: dir})
	if err := r.Regenerate(ctx, dir, ob.Onto, c, ob.Paths); err != nil {
		var refused *RefusedError
		if errors.As(err, &refused) {
			return nil, &WaitError{Code: CodeNotApproved, Reason: "not approved at " + short(ob.Commit) + ": regenerating its generated files failed: " + refused.Reason}
		}
		return nil, err
	}
	return r.vcs.DirtyFiles(ctx, dir)
}

func (r *validation) acquire(ctx context.Context) error {
	select {
	case r.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *validation) release() { <-r.slots }

// start runs f's gate in the background; the slot acquired for f is released when the
// gate returns.
func (r *validation) start(ctx context.Context, group int, f *flight) {
	fctx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	r.Events.Emit(Event{Kind: EventGate, Change: f.change.ID, Partition: partitionOf(group), Commit: f.cand.Commit, Depth: f.depth})
	go func() {
		defer r.release()
		start := time.Now()
		res, err := r.gate.Validate(fctx, f.cand, f.onto, f.change)
		f.done <- outcome{green: res.Green, summary: res.Summary, err: err, took: time.Since(start)}
	}()
}

// verdict decides a gated or refused change and reports whether it was green.
// attributable says everything beneath the candidate is validated, so a red is the
// change's own.
func (r *validation) verdict(ctx context.Context, f *flight, out outcome, attributable bool) (bool, error) {
	v := Verdict{Change: f.change, After: f.after, Onto: f.onto, Candidate: f.cand.Commit, Method: f.change.Method,
		Depth: f.depth, DurationMS: out.took.Milliseconds()}
	if !out.green {
		what := "the gate failed"
		v.Code = CodeRed
		if out.refused != nil {
			what, v.Code, v.Paths = "building its candidate failed", CodeRefused, out.refused.Paths
		}
		if !attributable {
			v.Decision, v.Code, v.Paths = DecisionWait, CodeBehind, nil
			v.Reason = what + " on top of #" + f.after + ", which this run did not validate; retried once it is"
			return false, r.decide(ctx, v)
		}
		v.Decision = DecisionKick
		v.Reason = what + ": " + out.summary
		v.Report = failureReport(r.plan.Base, f.change.Head, what, out.summary)
		return false, r.decide(ctx, v)
	}
	msg, err := squashMessage(ctx, r.vcs, r.plan.BaseCommit, f.change)
	if err != nil {
		return false, fmt.Errorf("squash message of %s: %w", f.change.Label(), err)
	}
	v.Decision, v.Message, v.Reviewed = DecisionMerge, msg, f.reviewed
	return true, r.decide(ctx, v)
}

// squashMessage is GitHub's default squash body for c's own commits: one "* subject"
// paragraph each, oldest first, merges left out. A stacked change's own commits start
// at its stack base.
func squashMessage(ctx context.Context, v VCS, baseCommit string, c Change) (string, error) {
	from := baseCommit
	if c.StackBase != "" {
		from = c.StackBase
	}
	commits, err := v.RangeCommits(ctx, from, c.Head, nil)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, cm := range slices.Backward(commits) {
		if len(cm.Parents) <= 1 {
			parts = append(parts, "* "+cm.Subject)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// ground stops a flight's gate, waits for it, and removes its checkout.
func (r *validation) ground(ctx context.Context, f *flight) {
	f.cancel()
	<-f.done
	r.discard(ctx, f.cand)
}

func (r *validation) discard(ctx context.Context, cand Candidate) {
	if cand.Dir == "" {
		return
	}
	if err := r.vcs.RemoveCheckout(context.WithoutCancel(ctx), cand.Dir); err != nil {
		r.Events.Emit(Event{Kind: EventNotice, Reason: "remove checkout " + cand.Dir + ": " + err.Error()})
	}
}

func (r *validation) decide(ctx context.Context, v Verdict) error {
	v.BaseCommit = r.plan.BaseCommit
	r.Events.Emit(Event{Kind: EventDecided, Change: v.Change.ID, Decision: v.Decision, Code: v.Code, Reason: v.Reason,
		Commit: v.Candidate, Depth: v.Depth, DurationMS: v.DurationMS})
	if err := r.sink.Record(ctx, v); err != nil {
		return fmt.Errorf("record the verdict on %s: %w", v.Change.Label(), err)
	}
	return nil
}

func conflictAhead(after string, conf Conflict) string {
	with := "the commit it was merged onto"
	if after != "" {
		with = "#" + after + " ahead of it"
	}
	return "conflicts with " + with + " in " + joinPaths(conf.Paths) + "; retried once it merges"
}

func failureReport(base, head, what, summary string) string {
	return fmt.Sprintf("The merge queue validated this change at `%s` on `%s`, and %s.\n\n%s\n\nPush a fix and queue the change again.\n",
		short(head), base, what, summary)
}
