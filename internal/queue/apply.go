package queue

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

// DefaultStatusContext is the commit status an [Applier] posts when none is configured.
const DefaultStatusContext = "merge-queue"

// Applier is the write step of a queue run. It trusts nothing validation produced but
// its decision and bytes it can check: it rebuilds each validated candidate itself, from
// the change's head, the base and the base's own regeneration or validation's
// regenerated outputs, and merges only what matches what validation gated. It re-checks,
// without running any change's code, everything else it can:
// approval at the head, merge intent, the change's base and merge method, that the head
// has not moved, that the provider's merge carries exactly the rebuilt tree, and
// afterwards that the base branch does and has the shape the merge method gives.
//
// The status it posts reads pending while a change waits, and success only on the commit
// it is about to see merged, once it has read the base again and found it still at the
// tip that commit's merge was predicted onto. The provider may then merge on its own, so
// nothing needs to bypass the status. A success applying cannot follow through is set
// back to pending, and each run first sets back any success an earlier run left. What
// remains is a push to the base between that read and the merge, which the check after
// the merge detects, stopping applying.
//
// Nothing of the plan is taken on its word where the applier can read it itself: the
// plan must name the applier's own Base and RemoteURL, its base commit must be on the
// base, and a stacked change's stack base must be the reviewed head of the change
// beneath it (see [magustypes.QueuePlanUnverified]). Each squash message is the
// applier's own, from the change's commits.
type Applier struct {
	// Base is the branch the applier merges into and RemoteURL the URL of the remote the
	// provider names its repository by; both are the applier's own, and a plan naming
	// others is refused. Base is required.
	Base      string
	RemoteURL string
	// StatusContext names the commit status an Applier posts; empty means
	// [DefaultStatusContext].
	StatusContext string
	// App is the app the provider's write credential belongs to (github: a GitHub App,
	// which it requires); the zero App names none. Run refuses to start when the base
	// requires StatusContext from an integration other than the credential's.
	App types.App
	// Interval is how long to wait between polls while verdicts are outstanding.
	Interval time.Duration
	// DryRun reports what would merge and calls nothing on the provider.
	DryRun bool
	// Committer authors and commits every update commit, overriding the committer the
	// provider names. The head's author is never copied: it is a claim the change's
	// author typed, and an update commit holds the base and its regeneration, not their
	// work.
	Committer magustypes.Person
	// Regenerate is the base's own regeneration. An Applier runs it where validation's
	// candidate holds regenerated files, and only once the build tool proved it runs
	// none of the change's code; where it cannot, the Applier takes validation's
	// regenerated candidate from the verdict source's bundle once it checks it (see
	// takeValidated), and kicks the change back only without one. It runs in the job
	// holding the write credential over a checkout carrying the change's files, so it
	// must be confined to that checkout and fail rather than run unconfined:
	// [CommandRegenerate] runs it in the base's sandbox, which `magus queue apply
	// --sandbox=required` holds to the kernel's confinement.
	Regenerate types.RegenerateFunc
	// Source names the validation run the verdicts come from, as the provider names it,
	// for each kick-back to point at; empty when they come from a directory.
	Source string
	// Reproduce is the hook lines validation runs, shown on each kick-back validation
	// decided so its author can run it again; an Applier runs neither. It is never read
	// from a verdict, which a job running the change's code wrote. A zero Reproduction
	// shows none.
	Reproduce types.Reproduction
	Events    *Events

	vcs      types.PushVCS
	clone    Clone
	provider types.Provider
	src      types.VerdictSource
	facts    types.BuildFacts
	scratch  string
}

// NewApplier merges the verdicts src supplies through p, in cl with v, rebuilding each
// candidate under scratch. scratch must be absolute and outside cl.Root.
func NewApplier(v types.PushVCS, cl Clone, p types.Provider, src types.VerdictSource, f types.BuildFacts, scratch string) (*Applier, error) {
	switch {
	case v == nil || p == nil || src == nil || f == nil:
		return nil, errors.New("applier needs a VCS, a provider, a verdict source and build facts")
	case cl.check() != nil:
		return nil, cl.check()
	case !filepath.IsAbs(scratch):
		return nil, fmt.Errorf("scratch directory %q is not absolute", scratch)
	}
	return &Applier{vcs: v, clone: cl, provider: p, src: src, facts: f, scratch: scratch}, nil
}

// Run merges plan's changes as their verdicts arrive, each as its own commit, as soon as
// every change beneath it in its partition has merged; a run of stacked changes merges
// in one call where the provider merges stacks atomically. Partitions merge
// independently. It returns once every admitted change is settled or the source is
// exhausted; what it did not reach stays queued for the next run. An error means
// applying stopped.
func (a *Applier) Run(ctx context.Context, plan types.Plan) error {
	if err := plan.Check(); err != nil {
		return err
	}
	if a.Base == "" {
		return errors.New("applier needs the base branch it merges into")
	}
	switch {
	case plan.Base != a.Base:
		return magustypes.DiagnosticErrorf(magustypes.QueuePlanUnverified, "the plan merges into %q, and this applier merges into %q", plan.Base, a.Base)
	case plan.RemoteURL != a.RemoteURL:
		return magustypes.DiagnosticErrorf(magustypes.QueuePlanUnverified, "the plan names remote %q, and this applier's remote is %q", plan.RemoteURL, a.RemoteURL)
	}
	if a.Committer != (magustypes.Person{}) && (a.Committer.Name == "" || a.Committer.Email == "") {
		return fmt.Errorf("committer %q <%s> needs a name and an email", a.Committer.Name, a.Committer.Email)
	}
	r := &applyRun{Applier: a, plan: plan, committer: a.Committer, merged: map[string]bool{}, got: map[string]types.Verdict{},
		rebuilt: map[string]string{}, confirmed: map[string]string{}, onBase: map[string]bool{plan.BaseCommit: true},
		bundles: map[string]string{}, taken: map[string]bool{}}
	if !a.DryRun {
		caps, err := a.provider.Describe(ctx, types.ListQuery{Base: a.Base, RemoteURL: a.RemoteURL, StatusContext: a.statusContext(), App: a.App})
		if err != nil {
			return fmt.Errorf("describe the provider: %w", err)
		}
		if err := caps.Check(); err != nil {
			return err
		}
		if err := checkCredential(caps.Setup, a.Base); err != nil {
			return err
		}
		r.caps = caps
		if r.committer == (magustypes.Person{}) {
			r.committer = caps.Committer
		}
		if err := r.revokeStale(ctx); err != nil {
			return err
		}
		r.markStart(ctx)
	}
	defer func() {
		if err := removeCheckoutsUnder(context.WithoutCancel(ctx), a.vcs, a.clone.Root, a.scratch); err != nil {
			a.Events.Emit(Event{Kind: EventNotice, Reason: "remove checkouts under " + a.scratch + ": " + err.Error()})
		}
	}()
	for _, v := range plan.Verdicts {
		if err := r.settle(ctx, v); err != nil {
			return err
		}
	}
	queues := slices.Clone(plan.Partitions)
	for {
		batch, err := a.src.Poll(ctx)
		if err != nil {
			return err
		}
		for _, v := range batch.Verdicts {
			r.accept(v)
		}
		maps.Copy(r.bundles, batch.Bundles)
		for _, rej := range batch.Rejected {
			r.reject(rej)
		}
		progress := false
		remaining := 0
		for gi := range queues {
			for len(queues[gi]) > 0 {
				n, err := r.settleFront(ctx, queues[gi], batch.Done)
				if err != nil {
					return err
				}
				if n == 0 {
					break
				}
				queues[gi] = queues[gi][n:]
				progress = true
			}
			remaining += len(queues[gi])
		}
		if remaining == 0 {
			return nil
		}
		if batch.Done && !progress {
			for _, q := range queues {
				for _, c := range q {
					if err := r.wait(ctx, c, c.Head, types.CodeWaitBehind, "not validated in this run"); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if progress {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(max(a.Interval, 10*time.Millisecond)):
		}
	}
}

// checkCredential refuses a base that requires the queue's status from an integration
// other than the one the credential posts as: the provider would count none of the
// statuses the queue posts, so nothing would merge. A provider that reports no setup
// proves nothing either way.
func checkCredential(s *types.Setup, base string) error {
	if s == nil {
		return nil
	}
	for _, rc := range s.RequiredChecks {
		if rc.Context != s.StatusContext || rc.Integration == "" || rc.Integration == s.Credential.ID {
			continue
		}
		return magustypes.DiagnosticErrorf(magustypes.QueueCredentialMismatch,
			"%s requires status %q from integration %s, and the queue's credential posts it as %s; "+
				"pin %q to %s or give the queue integration %s's credential (magus queue describe prints the pin)",
			base, rc.Context, rc.Integration, s.Credential, rc.Context, s.Credential.ID, rc.Integration)
	}
	return nil
}

type applyRun struct {
	*Applier
	plan      types.Plan
	caps      types.Capabilities
	committer magustypes.Person
	merged    map[string]bool
	got       map[string]types.Verdict
	rebuilt   map[string]string // change -> the candidate this run rebuilt and checked it against
	confirmed map[string]string // change -> the head the provider reported for it this run
	onBase    map[string]bool   // base tips found to carry the plan's base commit
	listing   *types.Changes    // the provider's own listing, read once a stack needs it
	bundles   map[string]string // change -> the bundle of validation's regenerated candidate
	taken     map[string]bool   // change -> its candidate is validation's, checked by takeValidated
}

// accept files a verdict under the plan's own record of its change. A verdict names
// its change, but only the plan says which head was admitted and what lies beneath
// it, so a verdict that disagrees with the plan merges nothing, and one for a change the
// plan did not admit is dropped.
func (r *applyRun) accept(v types.Verdict) {
	gi, pos, ok := find(r.plan, v.Change.ID)
	if !ok {
		r.Events.Emit(Event{Kind: EventNotice, Change: v.Change.ID, Reason: "dropped a verdict on #" + v.Change.ID + ", which the plan did not admit"})
		return
	}
	g := r.plan.Partitions[gi]
	planned := g[pos]
	wait := func(reason string) types.Verdict {
		return types.Verdict{Change: planned, Decision: types.DecisionWait, Code: types.CodeWaitRevalidate, Reason: reason}
	}
	beneath := func(id string) int { return slices.IndexFunc(g[:pos], func(c types.Change) bool { return c.ID == id }) }
	switch {
	case v.BaseCommit != r.plan.BaseCommit:
		v = wait("validated on " + short(v.BaseCommit) + ", not this plan's base " + short(r.plan.BaseCommit))
	case v.Change.Head != planned.Head:
		v = wait("validated at " + short(v.Change.Head) + ", not the planned head " + short(planned.Head))
	case v.Decision == types.DecisionMerge && v.After != "" && beneath(v.After) < 0:
		v = wait("validated on top of #" + v.After + ", which is not beneath it in its partition")
	case v.Decision == types.DecisionMerge && v.After == "" && v.Onto != r.plan.BaseCommit:
		v = wait("validated at the bottom of its partition, but onto " + short(v.Onto) + ", not the plan's base")
	case v.Decision == types.DecisionMerge && planned.Below != "" && (v.After == "" || beneath(planned.Below) > beneath(v.After)):
		v = wait("validated without #" + planned.Below + ", which it is stacked on")
	default:
		v.Change = planned
	}
	r.got[planned.ID] = v
}

// reject holds a change whose verdict could not be read; nothing else is affected.
func (r *applyRun) reject(rej types.RejectedVerdict) {
	gi, pos, ok := find(r.plan, rej.Change)
	if !ok {
		r.Events.Emit(Event{Kind: EventNotice, Change: rej.Change, Reason: "dropped an unreadable verdict on #" + rej.Change + ": " + rej.Reason})
		return
	}
	planned := r.plan.Partitions[gi][pos]
	r.got[planned.ID] = types.Verdict{Change: planned, Decision: types.DecisionWait, Code: types.CodeWaitRevalidate, Reason: "its verdict could not be read: " + rej.Reason}
}

// settleFront settles what it can from the front of one partition's queue and returns
// how many changes that was: none while the front's verdict, or a stack run's, is still
// to come.
func (r *applyRun) settleFront(ctx context.Context, q []types.Change, done bool) (int, error) {
	v, ok := r.got[q[0].ID]
	if !ok {
		return 0, nil
	}
	run := stackRun(q, r.caps.StackMerge == types.StackMergeAtomic && !r.DryRun)
	if len(run) == 1 {
		return 1, r.settle(ctx, v)
	}
	missing := slices.ContainsFunc(run, func(c types.Change) bool { _, ok := r.got[c.ID]; return !ok })
	if missing && !done {
		return 0, nil
	}
	return r.mergeRun(ctx, run)
}

// stackRun is the run of stacked changes at q's front that merge in one provider call:
// each stacked on the one before as the provider declared, when the provider merges
// stacks atomically, else q's front alone.
func stackRun(q []types.Change, atomic bool) []types.Change {
	if !atomic {
		return q[:1]
	}
	n := 1
	for n < len(q) && q[n].Below == q[n-1].ID && q[n].Parent == q[n-1].ID {
		n++
	}
	return q[:n]
}

// greenRun is how many of run, from its bottom, can merge in one call: each green and
// validated on top of the candidate of the one beneath it.
func greenRun(run []types.Change, got map[string]types.Verdict) int {
	green, below := 0, ""
	for _, c := range run {
		v, ok := got[c.ID]
		if !ok || v.Decision != types.DecisionMerge || (below != "" && (v.After != below || v.Onto != got[below].CandidateCommit)) {
			break
		}
		green, below = green+1, c.ID
	}
	return green
}

// pins is what an atomic merge through run's top pins: every member beneath the top, at
// its head, lowest first.
func pins(run []types.Change) []types.PinnedChange {
	through := make([]types.PinnedChange, 0, max(0, len(run)-1))
	for _, c := range run[:max(0, len(run)-1)] {
		through = append(through, types.PinnedChange{ID: c.ID, Commit: c.Head})
	}
	return through
}

// runBases are the merge bases a provider merging run atomically takes, member for
// member: the natural one at the bottom, even when the change beneath it merged as a
// squash, and each member's stack base above it.
func runBases(run []types.Change) []string {
	bases := make([]string, len(run))
	for i, c := range run {
		if i > 0 {
			bases[i] = c.StackBase
		}
	}
	return bases
}

// retargetable reports whether c, which targets base, may be pointed at the plan's base:
// it targets the branch of below, the change it is stacked on, which merged this run.
func retargetable(c, below types.Change, belowMerged bool, base string) bool {
	return c.Below != "" && c.Below == below.ID && belowMerged && below.Branch != "" && below.Branch == base
}

func (r *applyRun) settle(ctx context.Context, v types.Verdict) error {
	c := v.Change
	switch v.Decision {
	case types.DecisionMerged:
		r.mergedEvent(ctx, c, Event{Kind: EventMerged, Change: c.ID, Commit: c.Head, Reason: v.Reason})
		return nil
	case types.DecisionKick:
		k := kickOf(r.plan.Base, v)
		// An admitted change's kick is validation's, so its hooks reproduce it.
		if _, _, admitted := find(r.plan, c.ID); admitted && r.Reproduce.Gate != "" {
			k.Reproduce = &types.Reproduction{Gate: r.Reproduce.Gate, Regenerate: r.Reproduce.Regenerate}
		}
		return r.kick(ctx, c, k)
	case types.DecisionWait:
		return r.wait(ctx, c, c.Head, v.Code, v.Reason)
	case types.DecisionMerge:
		if code, reason, held := r.blocked(v); held {
			return r.wait(ctx, c, c.Head, code, reason)
		}
		ok, err := r.mergeOne(ctx, v)
		r.merged[c.ID] = ok
		return err
	}
	return fmt.Errorf("verdict decides %q for %s", v.Decision, c.Label())
}

// blocked says why a green verdict cannot merge yet: what it was validated on, or what
// it is stacked on, did not merge this run, or was not what it was built onto.
func (r *applyRun) blocked(v types.Verdict) (types.Code, string, bool) {
	c := v.Change
	switch {
	case v.After != "" && !r.merged[v.After]:
		code := types.CodeWaitBehind
		if v.After == c.Below {
			code = types.CodeWaitBelow
		}
		return code, "validated on top of #" + v.After + ", which did not merge", true
	case c.Below != "" && !r.merged[c.Below]:
		return types.CodeWaitBelow, "stacked on #" + c.Below + ", which did not merge", true
	case c.Below != "" && !r.chainHas(v, c.Below):
		return types.CodeWaitRevalidate, "validated without #" + c.Below + ", which it is stacked on", true
	case v.After != "" && r.got[v.After].CandidateCommit != v.Onto:
		return types.CodeWaitRevalidate, "validated onto " + short(v.Onto) + ", not the candidate #" + v.After + " merged from", true
	}
	return "", "", false
}

// chainHas reports whether id is in the chain of changes v was validated on top of.
func (r *applyRun) chainHas(v types.Verdict, id string) bool {
	for seen := 0; v.After != "" && seen <= len(r.got); seen++ {
		if v.After == id {
			return true
		}
		v = r.got[v.After]
	}
	return false
}

// ready is what merging one change needs once its checks passed.
type ready struct {
	v        types.Verdict
	approval approvalResult
	tip      string
	onto     string // what cand was rebuilt onto
	cand     string // the candidate rebuilt here
	tree     string // the tree the base carries once it merges
	// stale names the required checks that failed on an older base: the change gets an
	// update commit carrying tip, which runs them again, instead of a merge.
	stale []string
}

// onto is the commit this run rebuilds v's candidate onto: the plan's base at the
// bottom of a partition, else the candidate it rebuilt for the change beneath.
func (r *applyRun) onto(v types.Verdict) (string, bool) {
	if v.After == "" {
		return r.plan.BaseCommit, true
	}
	cand, ok := r.rebuilt[v.After]
	return cand, ok
}

// tip reads the base's tip to merge onto, and stops applying unless it carries the
// plan's base commit: every candidate was built on that commit, so a base without it is
// not the one the plan was made for.
func (r *applyRun) tip(ctx context.Context) (string, error) {
	tip, err := fetchBase(ctx, r.vcs, r.clone, r.plan.Base)
	if err != nil || r.onBase[tip] {
		return tip, err
	}
	on, err := r.vcs.IsAncestor(ctx, r.clone.Root, r.plan.BaseCommit, tip)
	if err != nil {
		return "", fmt.Errorf("is the plan's base commit on %s: %w", r.plan.Base, err)
	}
	if !on {
		return "", magustypes.DiagnosticErrorf(magustypes.QueuePlanUnverified,
			"the plan's base commit %s is not on %s at %s", short(r.plan.BaseCommit), r.plan.Base, short(tip))
	}
	r.onBase[tip] = true
	return tip, nil
}

// unverified is the error that stops applying when the plan's record of c disagrees
// with what applying reads itself.
func unverified(c types.Change, why string) error {
	return magustypes.DiagnosticErrorf(magustypes.QueuePlanUnverified, "the plan's stack of %s: %s", c.Label(), why)
}

// verifyStack checks c's stack base and the change beneath it against the version
// control and the provider before anything uses them. A stack base is where the delta
// c's reviewers approved starts; a merge measured from any other commit merges a delta
// nobody reviewed, so a stack base the version control or a confirmed head disagrees
// with stops applying. A stack the provider now declares differently makes c wait (a
// *waitError), since the plan may have been right when it was made. c's head must be
// fetched.
func (r *applyRun) verifyStack(ctx context.Context, c types.Change) error {
	if c.StackBase == "" {
		if c.Below != "" {
			return unverified(c, "it is stacked on #"+c.Below+" with no stack base")
		}
		return nil
	}
	root := r.clone.Root
	carried, err := r.vcs.IsAncestor(ctx, root, c.StackBase, c.Head)
	if err != nil {
		return err
	}
	if !carried || c.StackBase == c.Head {
		return unverified(c, "stack base "+short(c.StackBase)+" is not beneath its head "+short(c.Head))
	}
	onBase, err := r.vcs.IsAncestor(ctx, root, c.StackBase, r.plan.BaseCommit)
	if err != nil {
		return err
	}
	if onBase {
		return unverified(c, "stack base "+short(c.StackBase)+" is already on "+r.plan.Base)
	}
	listed, err := r.listed(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(listed.Changes, func(l types.Change) bool { return l.ID == c.ID })
	if i < 0 {
		return &waitError{code: types.CodeWaitWithdrawn, reason: "the provider no longer lists it as queued"}
	}
	beneath := c.Below
	if beneath != "" {
		gi, pos, _ := find(r.plan, c.Below)
		below := r.plan.Partitions[gi][pos]
		if r.confirmed[below.ID] != below.Head {
			return unverified(c, "#"+below.ID+" beneath it was not confirmed at "+short(below.Head)+" this run")
		}
		top, err := ownTop(ctx, r.vcs, root, r.plan.BaseCommit, below.Head)
		if err != nil {
			return err
		}
		if top != c.StackBase {
			return unverified(c, "stack base "+short(c.StackBase)+" is not "+short(top)+", the head #"+below.ID+" was reviewed at")
		}
	} else if beneath, err = r.mergedBeneath(ctx, listed.Merged, c); err != nil {
		return err
	}
	if p := listed.Changes[i].Parent; p != "" && p != beneath {
		return &waitError{code: types.CodeWaitRestack, reason: "the provider now says it is stacked on #" + p + ", not #" + beneath}
	}
	return nil
}

// mergedBeneath returns the merged change c's stack base belongs to, among the merged
// changes the provider lists: the newest of that change's own commits c carries, as
// planning chose it, from a change whose commit is on the plan's base commit.
func (r *applyRun) mergedBeneath(ctx context.Context, merged []types.MergedChange, c types.Change) (string, error) {
	root := r.clone.Root
	for _, m := range merged {
		if err := r.vcs.FetchCommit(ctx, root, r.clone.Remote, m.Head); err != nil {
			return "", fmt.Errorf("fetch the head #%s merged at: %w", m.ID, err)
		}
		own, err := r.vcs.RangeCommits(ctx, root, r.plan.BaseCommit, m.Head, nil)
		if err != nil {
			return "", fmt.Errorf("commits of #%s: %w", m.ID, err)
		}
		k := slices.IndexFunc(own, func(cm magustypes.Commit) bool { return cm.ID == c.StackBase })
		if k < 0 {
			continue
		}
		for _, newer := range own[:k] {
			in, err := r.vcs.IsAncestor(ctx, root, newer.ID, c.Head)
			if err != nil {
				return "", err
			}
			if in {
				return "", unverified(c, "stack base "+short(c.StackBase)+" is not the newest commit of #"+m.ID+" it carries, "+short(newer.ID))
			}
		}
		if err := r.vcs.FetchCommit(ctx, root, r.clone.Remote, m.Commit); err != nil {
			return "", fmt.Errorf("fetch the commit #%s merged as: %w", m.ID, err)
		}
		on, err := r.vcs.IsAncestor(ctx, root, m.Commit, r.plan.BaseCommit)
		if err != nil {
			return "", err
		}
		if !on {
			return "", unverified(c, "#"+m.ID+" beneath it merged as "+short(m.Commit)+", which "+r.plan.Base+" does not carry")
		}
		return m.ID, nil
	}
	return "", unverified(c, "stack base "+short(c.StackBase)+" is neither the head of a change beneath it nor a commit of one the provider lists as merged")
}

// listed is the provider's own listing of the base, read once per run.
func (r *applyRun) listed(ctx context.Context) (types.Changes, error) {
	if r.listing == nil {
		l, err := r.provider.ListChanges(ctx, types.ListQuery{Base: r.Base, RemoteURL: r.RemoteURL})
		if err != nil {
			return types.Changes{}, fmt.Errorf("list the changes on %s: %w", r.Base, err)
		}
		r.listing = &l
	}
	return *r.listing, nil
}

func (r *applyRun) mergeOne(ctx context.Context, v types.Verdict) (bool, error) {
	c := v.Change
	if r.DryRun {
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Commit: v.CandidateCommit, Reason: "dry run: would merge candidate " + short(v.CandidateCommit)})
		return true, nil
	}
	onto, ok := r.onto(v)
	if !ok {
		return false, r.wait(ctx, c, c.Head, types.CodeWaitRevalidate, "validated on top of #"+v.After+", whose candidate this run did not rebuild")
	}
	tip, err := r.tip(ctx)
	if err != nil {
		return false, err
	}
	rd, err := r.check(ctx, v, tip, onto, onto)
	if rd == nil || err != nil {
		return false, err
	}
	return r.hand(ctx, rd)
}

// check re-checks v's change against the provider and the base at tip without writing
// anything, rebuilds its candidate onto buildOnto, and returns the tree it merges as:
// the rebuilt candidate's changes since predictFrom, merged onto tip. A nil ready with a
// nil error means it waits or was kicked back.
func (r *applyRun) check(ctx context.Context, v types.Verdict, tip, buildOnto, predictFrom string) (*ready, error) {
	c := v.Change
	if err := fetchHead(ctx, r.vcs, r.clone, c); err != nil {
		return nil, fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	if err := r.verifyStack(ctx, c); err != nil {
		var held *waitError
		if errors.As(err, &held) {
			return nil, r.wait(ctx, c, c.Head, held.code, held.reason)
		}
		return nil, err
	}
	stackBase := ""
	if c.Below == "" || r.merged[c.Below] {
		stackBase = c.StackBase
	}
	a, err := approval(ctx, r.provider, r.vcs, r.facts, r.clone, tip, c, stackBase, stackRefs(r.plan))
	if err != nil {
		return nil, err
	}
	if a.carried != "" {
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: carriedNotice(a)})
	}
	if a.Head == c.Head {
		r.confirmed[c.ID] = a.Head
	}
	switch {
	case a.Head != c.Head:
		moved := c
		moved.Head = a.Head
		return nil, r.wait(ctx, moved, a.Head, types.CodeWaitHeadMoved, "head moved to "+short(a.Head)+" after validation; retried next run")
	case !a.Queued:
		return nil, r.wait(ctx, c, c.Head, types.CodeWaitWithdrawn, "its merge intent was withdrawn after validation")
	case !a.Approved:
		return nil, r.wait(ctx, c, c.Head, types.CodeWaitNotApproved, "approval at "+short(a.reviewed)+" was withdrawn"+reasonSuffix(a.Reason))
	}
	if a.Base != r.plan.Base {
		retargeted, err := r.retarget(ctx, c, a, tip, stackBase)
		if err != nil || retargeted == nil {
			return nil, err
		}
		a = *retargeted
	}
	switch {
	case a.Method != v.Method:
		return nil, r.wait(ctx, c, c.Head, types.CodeWaitMethodChanged, "validated as "+string(v.Method)+", now "+string(a.Method)+"; validated again next run")
	case !r.caps.Allows(a.Method):
		return nil, r.kick(ctx, c, refusal(&types.RefusedError{Reason: "the repository does not allow the " + string(a.Method) + " merge method",
			Remedy: "Pick a merge method it allows."}))
	}
	stale, settled, err := r.requiredChecks(ctx, c, tip)
	if settled || err != nil {
		return nil, err
	}
	cand, err := r.rebuild(ctx, v, buildOnto)
	if err == nil {
		err = r.proveOwed(ctx, c, a.owed)
	}
	var refusedErr *types.RefusedError
	var held *waitError
	switch {
	case errors.As(err, &refusedErr):
		return nil, r.kick(ctx, c, refusal(refusedErr))
	case errors.As(err, &held):
		return nil, r.wait(ctx, c, c.Head, held.code, held.reason)
	case err != nil:
		return nil, fmt.Errorf("rebuild the candidate of %s: %w", c.Label(), err)
	}
	r.rebuilt[c.ID] = cand
	tree, err := predict(ctx, r.vcs, r.clone.Root, tip, predictFrom, cand, c)
	if conf, ok := asConflict(err); ok {
		return nil, r.wait(ctx, c, c.Head, types.CodeWaitRevalidate, joinPaths(conf.paths)+" changed both here and in what merged since validation; validated again next run")
	}
	if err != nil {
		return nil, fmt.Errorf("predict %s after %s: %w", r.plan.Base, c.Label(), err)
	}
	return &ready{v: v, approval: a, tip: tip, onto: buildOnto, cand: cand, tree: tree, stale: stale}, nil
}

// requiredChecks settles what the base's required checks, other than the queue's own
// status, say of c's head before the queue asks for a merge the provider would refuse.
// Running checks wait. Red ones kick c back when its head already carries tip, since
// they ran on the base it would merge onto; otherwise they ran on an older base, and
// their names come back as stale, for an update commit that runs them again on tip. A
// head is updated at most once per base commit: once it carries tip, red is its own.
// settled says c waits or was kicked back.
func (r *applyRun) requiredChecks(ctx context.Context, c types.Change, tip string) (stale []string, settled bool, err error) {
	checks, err := r.provider.RequiredChecks(ctx, c, c.Head)
	if err != nil {
		return nil, false, fmt.Errorf("required checks of %s: %w", c.Label(), err)
	}
	var failing, running []string
	for _, ch := range checks {
		switch {
		case ch.Name == r.statusContext():
		case ch.State == types.StateFailure:
			failing = append(failing, ch.Name)
		case ch.State == types.StatePending:
			running = append(running, ch.Name)
		}
	}
	switch {
	case len(failing) > 0:
		carried, err := r.vcs.IsAncestor(ctx, r.clone.Root, tip, c.Head)
		if err != nil {
			return nil, false, err
		}
		if carried {
			return nil, true, r.kick(ctx, c, types.Kick{Code: types.CodeKickRed, Report: redChecksReport(r.plan.Base, c.Head, tip, failing)})
		}
		return failing, false, nil
	case len(running) > 0:
		return nil, true, r.wait(ctx, c, c.Head, types.CodeWaitChecks, "its required checks "+joinPaths(running)+" are running")
	}
	return nil, false, nil
}

// redChecksReport says which required checks failed on a head that carries the base's
// tip, so the failure is the change's own.
func redChecksReport(base, head, tip string, failing []string) string {
	return "The merge queue cannot merge this change at `" + short(head) + "`: its required checks " + joinPaths(failing) +
		" failed there, and it already carries `" + base + "` at `" + short(tip) + "`, so they failed on what it would merge onto.\n\n" +
		"Fix them and push.\n"
}

// rebuild builds v's candidate again onto onto and returns it once it is the commit
// validation gated. Where validation's regeneration rewrote files, or the merge settled
// a conflicted generated file, the rebuild runs the base's own regeneration, after the
// build tool proved it runs none of the change's code; where it cannot prove that, it
// takes validation's regenerated candidate once takeValidated checks it. Either way no
// change's code runs in the one job that holds the write credential. What does not match
// is kicked back, never merged. A source file auto-resolution settled is settled again
// here, by this job's own computation from the three versions, so the commit matches
// only if validation settled it the same way.
func (r *applyRun) rebuild(ctx context.Context, v types.Verdict, onto string) (string, error) {
	c := v.Change
	s := candidateSpec{clone: r.clone, facts: r.facts, onto: onto, change: c, scratch: r.scratch, date: r.plan.CommitDate}
	b, err := buildMerge(ctx, r.vcs, s)
	if conf, ok := asConflict(err); ok {
		return "", &waitError{code: types.CodeWaitRevalidate, reason: "rebuilding its candidate conflicted in " + joinPaths(conf.paths) + ", which validation did not; validated again next run"}
	}
	if err != nil {
		return "", err
	}
	defer r.discard(ctx, b.Candidate)
	if len(b.resolved) > 0 {
		r.Events.Emit(Event{Kind: EventResolved, Change: c.ID, Commit: b.Commit, Reason: resolvedNote(b.resolved)})
	}
	if b.Commit == v.CandidateCommit && len(b.settled) == 0 {
		return b.Commit, nil
	}
	regen, err := outputs(ctx, r.facts, b.touched)
	if err != nil {
		return "", err
	}
	if len(regen) == 0 {
		return "", &waitError{code: types.CodeWaitRevalidate, reason: "rebuilding its candidate gave " + short(b.Commit) + ", not the validated " + short(v.CandidateCommit) + "; validated again next run"}
	}
	g, err := r.generation(ctx, c, regen, onto, b.touched)
	var unproven *types.RefusedError
	if bundle, ok := r.bundles[c.ID]; ok && errors.As(err, &unproven) {
		return r.takeValidated(ctx, v, b, bundle)
	}
	if err != nil {
		return "", err
	}
	commit, err := regenerateIn(ctx, r.vcs, s, b, r.Regenerate, g.Units)
	if err != nil {
		return "", err
	}
	if commit != v.CandidateCommit {
		written, _ := r.vcs.DiffTrees(ctx, r.clone.Root, commit, v.CandidateCommit)
		return "", &types.RefusedError{Paths: written, Reason: "the base's regeneration of " + joinPaths(regen) + " differs from what validation produced in " +
			joinPaths(written), Remedy: r.mergeBaseIn()}
	}
	return commit, nil
}

// takeValidated loads the bundle validation left for v's change and returns v's
// candidate from it once four things hold, each checked by this job alone:
//   - the candidate is the commit validation gated: v's candidate commit, looked up by
//     that id, so its tree is the one the gate ran on;
//   - its only parent is b's commit, this job's own merge of the change's pinned head
//     onto what it rebuilt it onto, so nothing beneath the regeneration is validation's;
//   - it differs from that merge only in files the base's workspace declares as outputs;
//   - each of those it holds is a regular file, executable exactly where the merge's is
//     (see unexpectedMode).
//
// Auto-resolved source files are already in the merge, settled here. Any other bundle is
// refused: its bytes came from a job that ran the change's code.
func (r *applyRun) takeValidated(ctx context.Context, v types.Verdict, b built, bundle string) (string, error) {
	root, want, merged := r.clone.Root, v.CandidateCommit, b.Commit
	refuse := func(paths []string, why string) (string, error) {
		return "", &types.RefusedError{Paths: paths, Reason: "validation's regenerated candidate " + why, Remedy: r.mergeBaseIn()}
	}
	if err := r.vcs.Unbundle(ctx, root, bundle); err != nil {
		return refuse(nil, "does not load onto this job's own merge of it: "+types.CodeSpan(err.Error()))
	}
	cm, err := r.vcs.FindCommit(ctx, root, want)
	if err != nil {
		return refuse(nil, "does not hold "+short(want)+", the commit its gate ran on")
	}
	if len(cm.Parents) != 1 || cm.Parents[0] != merged {
		return refuse(nil, "is not a commit on "+short(merged)+", this job's own merge of it onto what it was built onto")
	}
	changed, err := r.vcs.DiffTrees(ctx, root, merged, want)
	if err != nil {
		return "", err
	}
	if src, err := sources(ctx, r.facts, changed); err != nil || len(src) > 0 {
		if err != nil {
			return "", err
		}
		return refuse(src, "changes "+joinPaths(src)+", which the base does not declare as any target's output")
	}
	// No capability reports a tree entry's mode, so a checkout of the candidate shows
	// them, beside b's checkout of the merge.
	cand, err := checkout(ctx, r.vcs, root, r.scratch, "bundle-"+v.Change.ID, want)
	if err != nil {
		return "", err
	}
	defer r.discard(ctx, cand)
	path, why, err := unexpectedMode(cand.Dir, b.Dir, changed)
	if err != nil {
		return "", err
	}
	if path != "" {
		return refuse([]string{path}, "holds "+types.CodeSpan(path)+" as "+why+", which regenerating a declared output does not write")
	}
	r.taken[v.Change.ID] = true
	r.Events.Emit(Event{Kind: EventNotice, Change: v.Change.ID, Commit: want, Reason: "took validation's regeneration of " + joinPaths(changed) + " after checking it"})
	return want, nil
}

// generation proves regenerating outputs, in a tree built on at with added changed on top,
// runs none of c's code, and says what the regeneration runs.
//
// The build facts are the declarations at the plan's base, so everything between the base
// and at is proven too: a change merged beneath c in this run can make one of c's files
// a generator's input. A proof failing only on that span is not c's to fix, so c waits
// for a run whose base holds it.
func (r *applyRun) generation(ctx context.Context, c types.Change, outputs []string, at string, added []string) (types.Generation, error) {
	if r.Regenerate == nil {
		return types.Generation{}, &types.RefusedError{Paths: outputs, Reason: "merging it needs " + joinPaths(outputs) +
			" regenerated, and this queue regenerates nothing", Remedy: r.mergeBaseIn()}
	}
	own, err := r.vcs.RangeFiles(ctx, r.clone.Root, r.plan.BaseCommit, c.Head, nil)
	if err != nil {
		return types.Generation{}, err
	}
	var beneath []string
	if at != r.plan.BaseCommit {
		if beneath, err = r.vcs.DiffTrees(ctx, r.clone.Root, r.plan.BaseCommit, at); err != nil {
			return types.Generation{}, err
		}
	}
	// The outputs themselves are overwritten, not run; own still names any c changed.
	rewritten := func(p string) bool { return slices.Contains(outputs, p) }
	added = slices.DeleteFunc(slices.Clone(added), rewritten)
	changed := slices.Concat(own, added, slices.DeleteFunc(slices.Clone(beneath), rewritten))
	changed = slices.Compact(slices.Sorted(slices.Values(changed)))
	g, err := r.facts.Generation(ctx, outputs, changed)
	if err != nil {
		return types.Generation{}, fmt.Errorf("generation of %s: %w", joinPaths(outputs), err)
	}
	if regenerationProven(g) {
		return g, nil
	}
	itsOwn := func(p string) bool { return slices.Contains(own, p) || slices.Contains(added, p) }
	if len(g.Code) > 0 && !slices.ContainsFunc(g.Code, itsOwn) {
		return types.Generation{}, &waitError{code: types.CodeWaitRevalidate, reason: "regenerating " + joinPaths(outputs) + " on " + short(at) +
			" would run " + joinPaths(g.Code) + ", which it does not change but what it builds on does; validated again next run"}
	}
	why, paths := unprovenWhy(g, outputs)
	return types.Generation{}, &types.RefusedError{Paths: paths, Reason: "merging it needs " + joinPaths(outputs) + " regenerated, and " + why + " (" + joinPaths(paths) +
		"), and validation left no regenerated candidate this queue can check, so only its author can regenerate them",
		Remedy: r.mergeBaseIn() + " Then queue it again: the merge leaves them as they are.", Code: types.CodeKickRegeneration}
}

// proveOwed proves each merge of the base into c that a review covers only through
// regeneration: the base's own regeneration, run on the plain merge, must give exactly
// the merge commit's tree.
func (r *applyRun) proveOwed(ctx context.Context, c types.Change, owed []regenerationProof) error {
	for _, ob := range owed {
		unreviewed := func(why string) error {
			return &waitError{code: types.CodeWaitNotApproved, reason: "not approved at " + short(ob.Commit) + ": " + why}
		}
		plain, err := r.vcs.MergeTrees(ctx, r.clone.Root, magustypes.TreeMerge{Ours: ob.Onto, Theirs: ob.First})
		if err != nil {
			return err
		}
		// Conflict markers in a generated file are what regeneration overwrites; any it
		// leaves make the trees differ below.
		if src, err := sources(ctx, r.facts, conflictPaths(plain.Conflicts)); err != nil || len(src) > 0 {
			if err != nil {
				return err
			}
			return unreviewed("its plain merge conflicts in " + joinPaths(src))
		}
		// Dated as the merge it stands in for, so a regeneration that reads HEAD's date
		// makes the same tree of both.
		date, err := newestDate(ctx, r.vcs, r.clone.Root, time.Time{}, ob.Commit)
		if err != nil {
			return err
		}
		from, err := r.vcs.CommitTree(ctx, r.clone.Root, magustypes.TreeCommit{CommitMeta: queueMeta("merge queue: plain merge of "+short(ob.Commit), date),
			Tree: plain.Tree, Parents: []string{ob.First, ob.Onto}})
		if err != nil {
			return err
		}
		added, err := r.vcs.DiffTrees(ctx, r.clone.Root, ob.Onto, from)
		if err != nil {
			return err
		}
		g, err := r.generation(ctx, c, ob.Paths, ob.Onto, added)
		var refusedErr *types.RefusedError
		var held *waitError
		if errors.As(err, &refusedErr) || errors.As(err, &held) {
			return unreviewed(joinPaths(ob.Paths) + " differ from its plain merge, and the base's regeneration cannot vouch for them")
		}
		if err != nil {
			return err
		}
		got, err := r.regenerateAt(ctx, c, from, ob.Onto, date, ob.Paths, added, g.Units)
		if err != nil {
			return err
		}
		want, err := r.vcs.TreeID(ctx, r.clone.Root, ob.Commit)
		if err != nil {
			return err
		}
		if got != want {
			return unreviewed(joinPaths(ob.Paths) + " are not what the base's regeneration makes of its plain merge, so no review covers them")
		}
	}
	return nil
}

// regenerateAt regenerates paths in a checkout of commit, which holds changed
// differently from onto, and returns the tree that leaves.
func (r *applyRun) regenerateAt(ctx context.Context, c types.Change, commit, onto string, date time.Time, paths, changed, units []string) (string, error) {
	cand, err := checkout(ctx, r.vcs, r.clone.Root, r.scratch, "proof-"+c.ID, commit)
	if err != nil {
		return "", err
	}
	defer r.discard(ctx, cand)
	b := built{Candidate: cand, touched: paths, changed: changed, date: date}
	s := candidateSpec{clone: r.clone, facts: r.facts, onto: onto, change: c, scratch: r.scratch}
	after, err := regenerateIn(ctx, r.vcs, s, b, r.Regenerate, units)
	if err != nil {
		return "", err
	}
	return r.vcs.TreeID(ctx, r.clone.Root, after)
}

// retarget points c at the plan's base when it targets the branch of the change it is
// stacked on and that change merged this run: the provider merges a change into its own
// base. Any other base means c was retargeted after planning, and it is skipped.
func (r *applyRun) retarget(ctx context.Context, c types.Change, a approvalResult, tip, stackBase string) (*approvalResult, error) {
	skip := func() (*approvalResult, error) {
		return nil, r.wait(ctx, c, c.Head, types.CodeWaitRetarget, "targets "+a.Base+", not "+r.plan.Base+"; skipped")
	}
	gi, pos, ok := find(r.plan, c.Below)
	if !ok || !retargetable(c, r.plan.Partitions[gi][pos], r.merged[c.Below], a.Base) {
		return skip()
	}
	if err := r.provider.Retarget(ctx, c, r.plan.Base); err != nil {
		return nil, fmt.Errorf("retarget %s at %s: %w", c.Label(), r.plan.Base, err)
	}
	again, err := approval(ctx, r.provider, r.vcs, r.facts, r.clone, tip, c, stackBase, stackRefs(r.plan))
	if err != nil {
		return nil, err
	}
	if again.Base != r.plan.Base || again.Head != c.Head || !again.Approved || !again.Queued {
		return nil, r.wait(ctx, c, c.Head, types.CodeWaitRetarget, "targets "+again.Base+", not "+r.plan.Base+"; asked the provider to retarget it")
	}
	return &again, nil
}

// hand hands rd's change to the provider and checks what the base carries afterwards.
func (r *applyRun) hand(ctx context.Context, rd *ready) (bool, error) {
	v, c, tip := rd.v, rd.v.Change, rd.tip
	commit, err := r.handOver(ctx, rd)
	var refusedErr *types.RefusedError
	var held *waitError
	switch {
	case errors.As(err, &refusedErr):
		return false, r.kick(ctx, c, refusal(refusedErr))
	case errors.As(err, &held):
		return false, r.wait(ctx, c, c.Head, held.code, held.reason)
	case errors.Is(err, types.ErrNoCommitter):
		if werr := r.wait(ctx, c, c.Head, types.CodeWaitNoCommitter, "needs an update commit, and no committer is configured"); werr != nil {
			return false, werr
		}
		return false, err
	case err != nil:
		return false, fmt.Errorf("push the update commit of %s: %w", c.Label(), err)
	case len(rd.stale) > 0:
		return false, r.wait(ctx, c, commit, types.CodeWaitChecks, "its required checks "+joinPaths(rd.stale)+" failed on an older "+r.plan.Base+
			"; pushed "+short(commit)+", which carries "+short(tip)+", to run them again")
	}
	if ok, err := r.stillQueued(ctx, c, commit); !ok || err != nil {
		return false, err
	}
	now, err := fetchBase(ctx, r.vcs, r.clone, r.plan.Base)
	if err != nil {
		return false, err
	}
	if now != tip {
		return false, r.wait(ctx, c, commit, types.CodeWaitRevalidate, baseMoved(r.plan.Base, now))
	}
	msg, err := squashMessage(ctx, r.vcs, r.clone.Root, r.plan.BaseCommit, c)
	if err != nil {
		return false, fmt.Errorf("squash message of %s: %w", c.Label(), err)
	}
	// Green before the merge: a provider that merges on its own once the status passes
	// (GitHub's auto-merge) merges now, and one that does not takes the queue's call.
	if err := r.post(ctx, c, commit, types.StateSuccess, "validated as "+short(rd.cand)+"; merging"); err != nil {
		r.revokeOne(ctx, c, commit, err)
		return false, err
	}
	res, err := r.provider.MergeChange(ctx, c, types.MergeOptions{Commit: commit, Message: msg, App: r.App})
	if err != nil {
		return false, r.wait(context.WithoutCancel(ctx), c, commit, types.CodeWaitProviderRefused, "the provider refused the merge: "+err.Error())
	}
	after, err := fetchBase(ctx, r.vcs, r.clone, r.plan.Base)
	if err != nil {
		return true, err
	}
	r.mergedEvent(ctx, c, Event{Kind: EventMerged, Change: c.ID, Commit: commit, ByProvider: res.ByProvider})
	return true, r.mergedAs(ctx, c, tip, after, commit, rd.tree, v.Method)
}

// squashMessage is the conventional squash body for c's own commits: one "* subject"
// paragraph each, oldest first, merges left out. A stacked change's own commits start
// at its stack base. Validation never supplies it: its jobs run the change's code.
func squashMessage(ctx context.Context, v types.ReadVCS, root, baseCommit string, c types.Change) (string, error) {
	from := baseCommit
	if c.StackBase != "" {
		from = c.StackBase
	}
	commits, err := v.RangeCommits(ctx, root, from, c.Head, nil)
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

// stillQueued reads c's merge intent, base and head again right before its merge, and
// waits it when any of them changed.
func (r *applyRun) stillQueued(ctx context.Context, c types.Change, commit string) (bool, error) {
	a, err := r.provider.ApprovalAt(ctx, c, commit)
	if err != nil {
		return false, fmt.Errorf("approval of %s: %w", c.Label(), err)
	}
	switch {
	case a.Head != commit:
		moved := c
		moved.Head = a.Head
		return false, r.wait(ctx, moved, a.Head, types.CodeWaitHeadMoved, "head moved to "+short(a.Head)+" before its merge; retried next run")
	case !a.Queued:
		return false, r.wait(ctx, c, commit, types.CodeWaitWithdrawn, "its merge intent was withdrawn before its merge")
	case a.Base != r.plan.Base:
		return false, r.wait(ctx, c, commit, types.CodeWaitRetarget, "targets "+a.Base+", not "+r.plan.Base+"; skipped")
	}
	return true, nil
}

// mergedAs stops applying unless after, the base once c merged onto tip, carries the
// validated tree in the shape c's merge method gives: something wrote to the branch
// besides the queue, or the provider merged differently than predicted, and nothing more
// may merge on a base nobody validated.
func (r *applyRun) mergedAs(ctx context.Context, c types.Change, tip, after, commit, tree string, m types.MergeMethod) error {
	got, err := r.vcs.TreeID(ctx, r.clone.Root, after)
	if err != nil {
		return err
	}
	if got != tree {
		return fmt.Errorf("%s merged, but %s at %s carries tree %s, not the validated %s", c.Label(), r.plan.Base, short(after), short(got), short(tree))
	}
	cm, err := r.vcs.FindCommit(ctx, r.clone.Root, after)
	if err != nil {
		return err
	}
	shaped := false
	switch m {
	case types.MethodSquash:
		shaped = slices.Equal(cm.Parents, []string{tip})
	case types.MethodMerge:
		shaped = slices.Equal(cm.Parents, []string{tip, commit})
	case types.MethodRebase:
		if shaped, err = r.linearOn(ctx, tip, after); err != nil {
			return err
		}
	}
	if !shaped {
		return fmt.Errorf("%s merged, but %s at %s is not what a %s merge onto %s makes", c.Label(), r.plan.Base, short(after), m, short(tip))
	}
	return nil
}

// linearOn reports whether after is a line of single-parent commits on tip.
func (r *applyRun) linearOn(ctx context.Context, tip, after string) (bool, error) {
	commits, err := r.vcs.RangeCommits(ctx, r.clone.Root, tip, after, nil)
	if err != nil || len(commits) == 0 {
		return false, err
	}
	for i, cm := range commits {
		want := tip
		if i+1 < len(commits) {
			want = commits[i+1].ID
		}
		if !slices.Equal(cm.Parents, []string{want}) {
			return false, nil
		}
	}
	return true, nil
}

// handOver returns the commit to hand the provider so that its merge gives exactly
// rd.tree: the head when the provider's own merge already does and no required check is
// stale, else an update commit pushed to the change's branch under a lease on the head.
//
// The update commit may differ from the provider's own merge only where no reviewer's
// view changes: in declared outputs, which only the base's own regeneration may have
// written, and for a change stacked on one that merged as a squash, in exactly what that
// change merged. The second is proven, not assumed: merging the head onto tip from its
// stack base must give rd.tree, so the update commit is tip plus the change's own
// delta, the diff its reviewers approved. It may also differ in a conflicted source file
// the workspace opts into auto-resolution, where this job's own resolution, from the
// base's and the change's versions, is exactly what the rebuild holds; the provider's
// merge would stop on that conflict.
func (r *applyRun) handOver(ctx context.Context, rd *ready) (string, error) {
	c, root := rd.v.Change, r.clone.Root
	plain, err := r.vcs.MergeTrees(ctx, root, magustypes.TreeMerge{Ours: rd.tip, Theirs: c.Head})
	if err != nil {
		return "", err
	}
	// Only stale checks ask for an update commit where the provider's merge already
	// gives rd.tree: its content is then exactly that merge.
	onlyStale := len(plain.Conflicts) == 0 && plain.Tree == rd.tree
	if onlyStale && len(rd.stale) == 0 {
		return c.Head, nil
	}
	reviewed := plain.Tree
	src, err := r.unreviewed(ctx, reviewed, rd.tree)
	if err != nil {
		return "", err
	}
	resolved, src, err := r.settledAsValidated(ctx, rd, plain, src)
	if err != nil {
		return "", err
	}
	stacked := false
	if len(src) > 0 && c.StackBase != "" && (c.Below == "" || r.merged[c.Below]) {
		resolved = nil
		own, err := r.vcs.MergeTrees(ctx, root, magustypes.TreeMerge{Base: c.StackBase, Ours: rd.tip, Theirs: c.Head})
		if err != nil {
			return "", err
		}
		if len(own.Conflicts) == 0 {
			if src, err = r.unreviewed(ctx, own.Tree, rd.tree); err != nil {
				return "", err
			}
			stacked, reviewed = len(src) == 0, own.Tree
		}
	}
	if len(src) > 0 {
		return "", &types.RefusedError{Paths: src, Reason: "validated at `" + short(c.Head) + "`, but merging it the way the provider would differs from what was validated in " +
			joinPaths(src), Remedy: "Rebase it onto `" + r.plan.Base + "`."}
	}
	outputs, err := r.vcs.DiffTrees(ctx, root, reviewed, rd.tree)
	if err != nil {
		return "", err
	}
	outputs = slices.DeleteFunc(outputs, func(p string) bool {
		return slices.ContainsFunc(resolved, func(s settledSource) bool { return s.path == p })
	})
	if len(resolved) > 0 && c.Method == types.MethodRebase {
		return "", &types.RefusedError{Paths: resolvedPaths(resolved), Reason: "its merge onto `" + r.plan.Base + "` " + resolvedNote(resolved) +
			", and a rebase replays its commits onto `" + r.plan.Base + "`, where they still conflict", Remedy: "Pick another merge method, or rebase it onto `" + r.plan.Base + "`."}
	}
	// A candidate takeValidated took was checked to differ from this job's merge in
	// declared outputs alone, which its gate's drift check held to their generators.
	if len(outputs) > 0 && !r.taken[c.ID] {
		if _, err := r.generation(ctx, c, outputs, rd.onto, nil); err != nil {
			return "", err
		}
	}
	needs, remedy := "its merge onto `"+r.plan.Base+"` needs an update commit", r.mergeBaseIn()
	if onlyStale {
		needs = "its required checks " + joinPaths(rd.stale) + " failed on an older `" + r.plan.Base + "`, and running them again needs an update commit carrying it"
		remedy = "Merge `" + r.plan.Base + "` in and push, which runs them again."
	}
	switch {
	case c.Branch == "":
		return "", &types.RefusedError{Reason: needs + ", and the queue cannot push to its branch", Remedy: remedy}
	case len(rd.approval.BranchSharedWith) > 0:
		return "", &types.RefusedError{Reason: needs + ", and its branch is also the head of " +
			joinIDs(rd.approval.BranchSharedWith) + ", which would gain it too", Remedy: remedy}
	}
	if r.committer == (magustypes.Person{}) {
		return "", types.ErrNoCommitter
	}
	parents, msg := []string{c.Head, rd.tip}, "merge "+r.plan.Base+" into #"+c.ID+" and regenerate generated files"
	if onlyStale {
		msg = "merge " + r.plan.Base + " into #" + c.ID + " to run its required checks again"
	}
	if len(resolved) > 0 {
		msg += "\n\nThe merge queue " + resolvedNote(resolved) + "."
	}
	switch {
	case c.Method == types.MethodRebase && onlyStale:
		// A commit on the head holding the base would be replayed as the change's own.
		return "", &types.RefusedError{Reason: needs + ", and a change merged by rebase takes the base only by rebasing its commits, which the queue leaves to its author",
			Remedy: "Rebase it onto `" + r.plan.Base + "` and push, which runs them again."}
	case c.Method == types.MethodRebase:
		// The provider replays the head's commits and then this one, whose tree is the
		// validated one.
		parents, msg = []string{c.Head}, "regenerate generated files on "+r.plan.Base
	case stacked && r.caps.LinearStacks:
		// The branch has to stay linear on the base, so this replaces its commits with one
		// holding their delta. It is the queue's commit, not the author's: the author's
		// commits stay reachable by id, and the squash that merges it names the change's
		// own commits. A change stacked on the replaced head would be left carrying
		// commits no queued change holds any more, so then only the author restacks.
		if above, err := r.carrying(ctx, c); err != nil || above != "" {
			if err != nil {
				return "", err
			}
			return "", &types.RefusedError{Reason: "its merge onto `" + r.plan.Base + "` needs its branch restacked onto it, which would replace the commits #" + above +
				" is stacked on", Remedy: "Restack the stack onto `" + r.plan.Base + "`."}
		}
		parents = []string{rd.tip}
		msg = "restack #" + c.ID + " onto " + r.plan.Base + "\n\nReplaces " + c.Head + " and the commits beneath it that " +
			r.plan.Base + " lacks with one commit holding their delta onto " + short(rd.tip) + "."
	}
	update, err := r.vcs.CommitTree(ctx, root, magustypes.TreeCommit{
		// The queue's own identity authors it too: its content is the base and the base's
		// regeneration, not the author's work, and the head's author field is whatever
		// the change's author typed, which the queue would otherwise countersign.
		CommitMeta: magustypes.CommitMeta{Message: msg, Author: r.committer, Committer: r.committer},
		Tree:       rd.tree, Parents: parents,
	})
	if err != nil {
		return "", err
	}
	err = r.vcs.Push(ctx, root, magustypes.PushLease{Remote: r.clone.Remote, Ref: branchRef(c.Branch), To: update, Expected: c.Head})
	var rejected *magustypes.PushRejectedError
	switch {
	case errors.Is(err, magustypes.ErrStaleLease):
		return "", &waitError{code: types.CodeWaitBranchMoved, reason: "its branch moved or was deleted since validation"}
	case errors.As(err, &rejected):
		return "", &types.RefusedError{Reason: "its merge onto `" + r.plan.Base + "` needs an update commit, and its branch refused it: " + types.CodeSpan(rejected.Reason),
			Remedy: r.mergeBaseIn()}
	}
	return update, err
}

// carrying names an open change of the plan whose head carries c's head, or "".
func (r *applyRun) carrying(ctx context.Context, c types.Change) (string, error) {
	for _, ref := range stackRefs(r.plan) {
		if ref.id == c.ID || r.merged[ref.id] || slices.ContainsFunc(r.plan.Merged, func(m types.MergedChange) bool { return m.ID == ref.id }) {
			continue
		}
		if err := r.vcs.FetchCommit(ctx, r.clone.Root, r.clone.Remote, ref.head); err != nil {
			return "", err
		}
		in, err := r.vcs.IsAncestor(ctx, r.clone.Root, c.Head, ref.head)
		if err != nil {
			return "", err
		}
		if in {
			return ref.id, nil
		}
	}
	return "", nil
}

func joinIDs(ids []string) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "#" + id
	}
	return strings.Join(out, ", ")
}

// discard removes a checkout this run made; one it cannot remove is reported, and the
// next run's cleanup removes it.
func (r *applyRun) discard(ctx context.Context, cand types.Candidate) {
	if err := discard(ctx, r.vcs, r.clone.Root, cand); err != nil {
		r.Events.Emit(Event{Kind: EventNotice, Reason: "remove checkout " + cand.Dir + ": " + err.Error()})
	}
}

// settledAsValidated takes out of src the files that conflict in plain and that
// auto-resolution, computed here from the base at rd.tip and the change's head, settles to
// exactly what rd.tree holds. It returns those files and what is left of src. Nothing
// validation wrote is read: rd.tree is this job's own rebuild.
func (r *applyRun) settledAsValidated(ctx context.Context, rd *ready, plain magustypes.TreeMergeResult, src []string) ([]settledSource, []string, error) {
	if len(src) == 0 || len(plain.Conflicts) == 0 {
		return nil, src, nil
	}
	settled, _, _, err := resolveSources(ctx, r.vcs, r.facts, r.clone.Root, magustypes.TreeMerge{Ours: rd.tip, Theirs: rd.v.Change.Head}, plain.Conflicts)
	if err != nil {
		return nil, nil, err
	}
	var same []settledSource
	for _, s := range settled {
		if !slices.Contains(src, s.path) {
			continue
		}
		got, err := r.vcs.ReadFileAt(ctx, r.clone.Root, rd.tree, s.path)
		if err != nil {
			return nil, nil, err
		}
		if got == string(s.res.Content) {
			same = append(same, s)
		}
	}
	left := slices.DeleteFunc(slices.Clone(src), func(p string) bool { return slices.Contains(resolvedPaths(same), p) })
	return same, left, nil
}

func resolvedPaths(settled []settledSource) []string {
	out := make([]string, len(settled))
	for i, s := range settled {
		out[i] = s.path
	}
	return out
}

// unreviewed lists the paths a and b differ in that no target declares as its output.
func (r *applyRun) unreviewed(ctx context.Context, a, b string) ([]string, error) {
	diff, err := r.vcs.DiffTrees(ctx, r.clone.Root, a, b)
	if err != nil {
		return nil, err
	}
	return sources(ctx, r.facts, diff)
}

// partialMergeError is a provider merging part of an atomic run: applying stops, since
// the base holds members nobody merged one at a time.
type partialMergeError struct {
	top           string
	merged, total int
	cause         error
}

func (e *partialMergeError) Error() string {
	return fmt.Sprintf("stack through #%s merged %d of %d changes: %v", e.top, e.merged, e.total, e.cause)
}

// mergeRun merges a run of stacked changes in one provider call, through its highest
// green member; the members above it are settled one by one. It returns how many
// changes it settled.
func (r *applyRun) mergeRun(ctx context.Context, run []types.Change) (int, error) {
	green := greenRun(run, r.got)
	bottom := r.got[run[0].ID]
	if _, _, held := r.blocked(bottom); green < 2 || held {
		return 1, r.settle(ctx, bottom)
	}
	run = run[:green]
	predictFrom, ok := r.onto(bottom)
	if !ok {
		return 1, r.settle(ctx, bottom)
	}
	tip, err := r.tip(ctx)
	if err != nil {
		return 0, err
	}
	var steps []*ready
	buildOnto := predictFrom
	for i, c := range run {
		rd, err := r.check(ctx, r.got[c.ID], tip, buildOnto, predictFrom)
		if err != nil {
			return 0, err
		}
		if rd == nil {
			// It waits or was kicked back; what is stacked on it cannot merge without it.
			for _, above := range run[i+1:] {
				if err := r.wait(ctx, above, above.Head, types.CodeWaitBelow, "stacked on #"+c.ID+", which did not merge"); err != nil {
					return 0, err
				}
			}
			break
		}
		steps = append(steps, rd)
		buildOnto = rd.cand
	}
	for i, st := range steps {
		ok, err := r.stillQueued(ctx, st.v.Change, st.v.Change.Head)
		if err != nil {
			return 0, err
		}
		if !ok {
			for _, above := range steps[i+1:] {
				if err := r.wait(ctx, above.v.Change, above.v.Change.Head, types.CodeWaitBelow, "stacked on #"+st.v.Change.ID+", which did not merge"); err != nil {
					return 0, err
				}
			}
			steps = steps[:i]
			break
		}
	}
	// A member whose checks run again is updated rather than merged, alone.
	atomic := len(steps) > 1 && !slices.ContainsFunc(steps, func(st *ready) bool { return len(st.stale) > 0 })
	if atomic {
		if atomic, err = r.ownDeltas(ctx, tip, steps); err != nil {
			return 0, err
		}
	}
	if !atomic {
		// One at a time, each checked again onto what the one beneath it left.
		for _, st := range steps {
			if err := r.settle(ctx, st.v); err != nil {
				return 0, err
			}
		}
		return len(run), nil
	}
	top := steps[len(steps)-1]
	members := make([]types.Change, len(steps))
	for i, st := range steps {
		members[i] = st.v.Change
	}
	now, err := fetchBase(ctx, r.vcs, r.clone, r.plan.Base)
	if err != nil {
		return 0, err
	}
	if now != tip {
		for _, st := range steps {
			if err := r.wait(ctx, st.v.Change, st.v.Change.Head, types.CodeWaitRevalidate, baseMoved(r.plan.Base, now)); err != nil {
				return 0, err
			}
		}
		return len(run), nil
	}
	msg, err := squashMessage(ctx, r.vcs, r.clone.Root, r.plan.BaseCommit, top.v.Change)
	if err != nil {
		return 0, fmt.Errorf("squash message of %s: %w", top.v.Change.Label(), err)
	}
	for i, st := range steps {
		if err := r.post(ctx, st.v.Change, st.v.Change.Head, types.StateSuccess, "validated as "+short(st.cand)+"; merging in a stack"); err != nil {
			r.revoke(ctx, steps[:i+1], err)
			return 0, err
		}
	}
	res, mergeErr := r.provider.MergeChange(ctx, top.v.Change, types.MergeOptions{Commit: top.v.Change.Head, Message: msg, App: r.App, Through: pins(members)})
	after, err := fetchBase(ctx, r.vcs, r.clone, r.plan.Base)
	if err != nil {
		r.revoke(ctx, steps, err)
		return 0, err
	}
	if mergeErr != nil && after == tip {
		for _, st := range steps {
			if err := r.wait(context.WithoutCancel(ctx), st.v.Change, st.v.Change.Head, types.CodeWaitProviderRefused, "the provider refused the stack merge: "+mergeErr.Error()); err != nil {
				return 0, err
			}
		}
		return len(run), nil
	}
	merged, err := r.stackMerged(ctx, tip, after, steps)
	if err != nil {
		r.revoke(ctx, steps, err)
		return 0, err
	}
	for _, st := range steps[:merged] {
		r.merged[st.v.Change.ID] = true
		r.mergedEvent(ctx, st.v.Change, Event{Kind: EventMerged, Change: st.v.Change.ID, Commit: st.v.Change.Head, ByProvider: res.ByProvider})
	}
	if merged < len(steps) {
		err := &partialMergeError{top: top.v.Change.ID, merged: merged, total: len(steps), cause: mergeErr}
		r.revoke(ctx, steps[merged:], err)
		return 0, err
	}
	return len(run), nil
}

// revoke sets the success applying posted on each of steps back to pending once applying
// stopped without seeing it merged: a success left behind would let the change merge
// later onto a base nobody validated. It reports what it cannot post and returns nothing,
// since the caller is already returning cause.
func (r *applyRun) revoke(ctx context.Context, steps []*ready, cause error) {
	for _, st := range steps {
		r.revokeOne(ctx, st.v.Change, st.v.Change.Head, cause)
	}
}

func (r *applyRun) revokeOne(ctx context.Context, c types.Change, commit string, cause error) {
	if err := r.post(context.WithoutCancel(ctx), c, commit, types.StatePending, "waiting: applying stopped before its merge: "+cause.Error()); err != nil {
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: err.Error()})
	}
}

// baseMoved is why a change waits when base is no longer at the tip its merge was
// predicted onto, right before it would go green.
func baseMoved(base, now string) string {
	return base + " moved to " + short(now) + " before its merge; validated again next run"
}

// ownDeltas reports whether what a provider merging a stack atomically produces is each
// step's validated tree: the bottom merged from its natural merge base, even when the
// change it is stacked on merged as a squash, and each member above as its own delta
// from the member below. When it does not (a candidate regenerated a file, or the bottom
// would bring back what the change beneath it deleted), the run merges one change at a
// time with update commits instead.
func (r *applyRun) ownDeltas(ctx context.Context, tip string, steps []*ready) (bool, error) {
	members := make([]types.Change, len(steps))
	for i, st := range steps {
		members[i] = st.v.Change
	}
	bases := runBases(members)
	below := tip
	for i, st := range steps {
		if i > 0 {
			var err error
			if below, err = r.vcs.CommitTree(ctx, r.clone.Root, magustypes.TreeCommit{CommitMeta: queueMeta("expected", r.plan.CommitDate), Tree: steps[i-1].tree, Parents: []string{below}}); err != nil {
				return false, err
			}
		}
		m, err := r.vcs.MergeTrees(ctx, r.clone.Root, magustypes.TreeMerge{Base: bases[i], Ours: below, Theirs: st.v.Change.Head})
		if err != nil {
			return false, err
		}
		if len(m.Conflicts) > 0 || m.Tree != st.tree {
			return false, nil
		}
	}
	return true, nil
}

// stackMerged counts the steps merged between tip and after, each checked against its
// validated tree and its merge method's shape, and errors when the base holds anything
// else.
func (r *applyRun) stackMerged(ctx context.Context, tip, after string, steps []*ready) (int, error) {
	commits, err := r.vcs.RangeCommits(ctx, r.clone.Root, tip, after, nil)
	if err != nil {
		return 0, err
	}
	top := steps[len(steps)-1].v.Change
	if steps[0].v.Method == types.MethodMerge {
		// One merge commit for the whole run, so only the top's tree is checked and the
		// members' own commits are its second parent's history.
		if err := r.mergedAs(ctx, top, tip, after, top.Head, steps[len(steps)-1].tree, types.MethodMerge); err != nil {
			return 0, err
		}
		return len(steps), nil
	}
	if len(commits) > len(steps) {
		return 0, fmt.Errorf("stack through %s left %d commits on %s for %d changes", top.Label(), len(commits), r.plan.Base, len(steps))
	}
	below := tip
	for i := range commits {
		cm := commits[len(commits)-1-i]
		st := steps[i]
		if err := r.mergedAs(ctx, st.v.Change, below, cm.ID, st.v.Change.Head, st.tree, types.MethodSquash); err != nil {
			return 0, err
		}
		below = cm.ID
	}
	return len(commits), nil
}

func refusal(r *types.RefusedError) types.Kick {
	report := "The merge queue validated this change but cannot merge it: " + r.Reason + ".\n"
	if r.Remedy != "" {
		report += "\n" + r.Remedy + "\n"
	}
	code := r.Code
	if code == "" {
		code = types.CodeKickRefused
	}
	return types.Kick{Code: code, Report: report, Paths: r.Paths}
}

// mergeBaseIn is the remedy for what only the author can regenerate.
func (r *applyRun) mergeBaseIn() string {
	return "Merge `" + r.plan.Base + "` in, regenerate, and push."
}

// kick kicks c back for its head. A head pushed since the kick was decided gets a
// decision of its own, so c waits instead.
func (r *applyRun) kick(ctx context.Context, c types.Change, k types.Kick) error {
	if !r.DryRun {
		a, err := r.provider.ApprovalAt(ctx, c, c.Head)
		if err != nil {
			return fmt.Errorf("approval of %s: %w", c.Label(), err)
		}
		if a.Head != c.Head {
			moved := c
			moved.Head = a.Head
			return r.wait(ctx, moved, a.Head, types.CodeWaitHeadMoved, "head moved to "+short(a.Head)+" since it was decided; decided again next run")
		}
	}
	k.Source = r.Source
	r.Events.Emit(Event{Kind: EventKicked, Change: c.ID, Code: k.Code, Reason: firstLine(k.Report)})
	if r.DryRun {
		return nil
	}
	if err := r.post(ctx, c, c.Head, types.StateFailure, "kicked back; see the comment"); err != nil {
		return err
	}
	if err := r.provider.KickBack(ctx, c, c.Head, k); err != nil {
		return fmt.Errorf("kick back %s: %w", c.Label(), err)
	}
	r.mark(ctx, c, k.Mark())
	return nil
}

func (r *applyRun) wait(ctx context.Context, c types.Change, commit string, code types.Code, reason string) error {
	r.Events.Emit(Event{Kind: EventWaiting, Change: c.ID, Code: code, Reason: reason})
	if r.DryRun {
		return nil
	}
	return r.post(ctx, c, commit, types.StatePending, "waiting: "+reason)
}

// revokeStale sets back to pending every success on an open change: nothing is about to
// merge when a run starts, so any success is one an earlier run could not follow through.
func (r *applyRun) revokeStale(ctx context.Context) error {
	green, err := r.provider.ListGreen(ctx, types.ListQuery{Base: r.Base, RemoteURL: r.RemoteURL}, r.statusContext())
	if err != nil {
		return fmt.Errorf("list the changes carrying %s at success: %w", r.statusContext(), err)
	}
	for _, g := range green {
		r.Events.Emit(Event{Kind: EventNotice, Change: g.ID, Commit: g.Head, Reason: "set back to pending the success an earlier run left on #" + g.ID})
		if err := r.post(ctx, types.Change{ID: g.ID, Repo: g.Repo, Head: g.Head}, g.Head, types.StatePending, "waiting: an earlier run stopped before merging it"); err != nil {
			return err
		}
	}
	return nil
}

// markStart marks queued every change the plan admitted, and clears the queued mark from
// each unqueued change still showing one: its intent was withdrawn, or a run stopped
// before marking what it did to it. It clears the marks of every closed change still
// showing one, merged or closed where the queue did not see it go.
func (r *applyRun) markStart(ctx context.Context) {
	for _, g := range r.plan.Partitions {
		for _, c := range g {
			r.mark(ctx, c, types.MarkQueued)
		}
	}
	for _, v := range r.plan.Verdicts {
		if v.Decision == types.DecisionWait {
			r.mark(ctx, v.Change, types.MarkQueued)
		}
	}
	for _, u := range r.plan.Unqueued {
		if u.Mark == types.MarkQueued {
			r.mark(ctx, types.Change{ID: u.ID, Repo: u.Repo, Head: u.Head}, types.MarkNone)
		}
	}
	for _, cl := range r.plan.Closed {
		r.mark(ctx, types.Change{ID: cl.ID, Repo: cl.Repo}, types.MarkNone)
	}
}

// mergedEvent reports c merged and clears its mark and the label that queued it.
func (r *applyRun) mergedEvent(ctx context.Context, c types.Change, e Event) {
	r.Events.Emit(e)
	r.mark(ctx, c, types.MarkNone)
}

// mark shows m on c. A failure is a notice and applying goes on: a mark is a courtesy,
// and the status and the kick-back comment are the record.
func (r *applyRun) mark(ctx context.Context, c types.Change, m types.Mark) {
	if r.DryRun {
		return
	}
	if err := r.provider.Mark(ctx, c, m); err != nil {
		what := "mark #" + c.ID + " " + string(m)
		if m == types.MarkNone {
			what = "clear the mark on #" + c.ID
		}
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: "could not " + what + ": " + err.Error()})
	}
}

func (a *Applier) statusContext() string {
	if a.StatusContext == "" {
		return DefaultStatusContext
	}
	return a.StatusContext
}

func (r *applyRun) post(ctx context.Context, c types.Change, commit string, state types.CommitState, desc string) error {
	name := r.statusContext()
	if err := r.provider.PostStatus(ctx, c, commit, types.CommitStatus{Context: name, State: state, Description: desc}); err != nil {
		return fmt.Errorf("post %s on %s: %w", name, short(commit), err)
	}
	return nil
}
