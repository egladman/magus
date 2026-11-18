package safetyscissors

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/safetyscissors/trail"
)

// SafetyScissors is the harness opened on one directory: the job store, this
// invocation's session writer, and whichever of the slot limiter, the machine budget,
// the tier set and the sandbox policy [New] was given. It is safe for concurrent use as
// far as each part is.
type SafetyScissors struct {
	dirs    Dirs
	jobs    *job.Store
	session *session.Writer
	slots   *budget.Limiter
	machine *budget.Machine
	tiers   *tier.Set
	root    string
	policy  *sandbox.Policy
}

// New opens the harness on dir. It requires [WithInvocation], [WithActor],
// [WithNotices] and [WithCheckSpec]; every other part is absent until its option is passed, and its
// accessor then returns an [*UnconfiguredError] naming the option.
//
// It refuses an empty dir and each option problem with an [*OptionError], joined, and
// returns the error of the part that would not open: [session.Open]'s, [tier.NewSet]'s,
// [sandbox.New]'s, and under [WithSandbox] the [*sandbox.UnconfinedError] of a host that
// cannot confine a child in the Required mode. It writes no record; [session.Open]
// creates the sessions directory and may prune it.
func New(ctx context.Context, dir string, opts ...Option) (*SafetyScissors, error) {
	var c config
	var errs []error
	if strings.TrimSpace(dir) == "" {
		errs = append(errs, errors.New("safetyscissors: New needs the directory the stores live in, and none was named"))
	}
	for _, opt := range opts {
		if err := opt(&c); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, c.missing()...)
	dirs := layout(dir, c.dirs)
	if dirs.Jobs == dirs.Sessions {
		errs = append(errs, &OptionError{Option: "WithDirs", Problem: "Jobs and Sessions name one directory, " + dirs.Jobs})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	s := &SafetyScissors{dirs: dirs, root: c.root}
	var err error
	if s.session, err = session.Open(ctx, dirs.Sessions, c.invocation, c.start, c.sessionOptions()...); err != nil {
		return nil, err
	}
	if s.jobs, err = job.Open(dirs.Jobs, c.jobOptions()...); err != nil {
		return nil, err
	}
	if c.withSlots {
		s.slots = budget.NewLimiter(c.slots, c.slotOpts...)
	}
	if c.withMachine {
		s.machine = budget.NewMachine(c.machineMB, c.machineSlots, c.machineOptions()...)
	}
	if c.withTiers {
		if s.tiers, err = tier.NewSet(c.tiers); err != nil {
			return nil, err
		}
	}
	if c.withSandbox {
		if s.policy, err = confining(c.binds, c.sandboxOpts); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (c *config) missing() []error {
	var errs []error
	if !c.invoked {
		errs = append(errs, &OptionError{Option: "WithInvocation", Problem: "New requires it: the module mints no invocation id"})
	}
	if c.actor == nil {
		errs = append(errs, &OptionError{Option: "WithActor", Problem: "New requires it: every job write says who writes"})
	}
	if c.notices == nil {
		errs = append(errs, &OptionError{Option: "WithNotices", Problem: "New requires it: the job store reports what it did there, and never prints"})
	}
	if c.checks == nil {
		errs = append(errs, &OptionError{Option: "WithCheckSpec", Problem: "New requires it: the job store keeps a check as opaque JSON and reads it only through the caller's spec"})
	}
	return errs
}

func (c *config) sessionOptions() []session.Option {
	if c.clock == nil {
		return nil
	}
	return []session.Option{session.WithClock(c.clock)}
}

func (c *config) jobOptions() []job.StoreOption {
	opts := slices.Concat(c.jobOpts, []job.StoreOption{job.WithActor(c.actor), job.WithNotices(c.notices)}, c.checks)
	if c.clock != nil {
		opts = append(opts, job.WithClock(c.clock))
	}
	return opts
}

func (c *config) machineOptions() []budget.MachineOption {
	if c.clock == nil {
		return c.machineOpts
	}
	return append(slices.Clone(c.machineOpts), budget.WithClock(c.clock))
}

// confining builds the policy and asks the host once, so a host that cannot confine
// fails here rather than at the first child. A BestEffort policy's refusal is its
// WithUnconfined's to hear, per child.
func confining(binds []sandbox.Bind, opts []sandbox.Option) (*sandbox.Policy, error) {
	p, err := sandbox.New(binds, opts...)
	if err != nil {
		return nil, err
	}
	if _, err := p.Confines(); err != nil {
		var unconfined *sandbox.UnconfinedError
		if !errors.As(err, &unconfined) || unconfined.Mode != sandbox.BestEffort {
			return nil, err
		}
	}
	return p, nil
}

// Dirs is where each store lives, for the subpackage calls that take a directory, such
// as [session.Prune] and [session.FoldKind].
func (s *SafetyScissors) Dirs() Dirs { return s.dirs }

// Jobs is the job store. Its verbs are [job.Store]'s.
func (s *SafetyScissors) Jobs() *job.Store { return s.jobs }

// Session is this invocation's session writer.
func (s *SafetyScissors) Session() *session.Writer { return s.session }

// Sessions folds every invocation's records in the sessions directory (see
// [session.ReadAll]).
func (s *SafetyScissors) Sessions(ctx context.Context) (session.Fold, error) {
	return session.ReadAll(ctx, s.dirs.Sessions)
}

// Trail returns up to limit trail events, newest first (see [trail.ReadRecent]).
func (s *SafetyScissors) Trail(ctx context.Context, limit int) ([]trail.Event, error) {
	return trail.ReadRecent(ctx, s.dirs.Trail, limit)
}

// Slots is the slot limiter [WithSlots] configured.
func (s *SafetyScissors) Slots() (*budget.Limiter, error) {
	if s.slots == nil {
		return nil, &UnconfiguredError{Part: "slot limiter", Option: "WithSlots"}
	}
	return s.slots, nil
}

// Machine is the machine budget [WithMachine] configured.
func (s *SafetyScissors) Machine() (*budget.Machine, error) {
	if s.machine == nil {
		return nil, &UnconfiguredError{Part: "machine budget", Option: "WithMachine"}
	}
	return s.machine, nil
}

// Tiers is the model tier set [WithTiers] configured.
func (s *SafetyScissors) Tiers() (*tier.Set, error) {
	if s.tiers == nil {
		return nil, &UnconfiguredError{Part: "tier set", Option: "WithTiers"}
	}
	return s.tiers, nil
}

// Sandbox is the policy [WithSandbox] configured, before any lease narrows it.
func (s *SafetyScissors) Sandbox() (*sandbox.Policy, error) {
	if s.policy == nil {
		return nil, &UnconfiguredError{Part: "sandbox", Option: "WithSandbox"}
	}
	return s.policy, nil
}

// Command returns an unstarted child running name, confined to the sandbox's binds and,
// when lease is not empty, narrowed to that job's write paths over the checkout root
// (see [sandbox.Command] and [sandbox.LeaseBoundary]). It starts nothing.
//
// Without [WithSandbox] it is an [*UnconfiguredError]: it never starts a child
// unconfined. A lease the store does not hold wraps [job.ErrUnknownJob], and one whose
// row is not live is a [*LeaseNotLiveError]. A live row that bounds nothing narrower
// than the checkout, such as a lease with no parent, leaves the binds as they are.
func (s *SafetyScissors) Command(ctx context.Context, lease, name string, args ...string) (*exec.Cmd, error) {
	p, err := s.Sandbox()
	if err != nil {
		return nil, err
	}
	if lease != "" {
		row, err := s.row(ctx, lease)
		if err != nil {
			return nil, err
		}
		if !row.State.Live() {
			return nil, &LeaseNotLiveError{Lease: lease, State: row.State}
		}
		if b, ok := sandbox.LeaseBoundary(row, job.LeaseSourceFlag, s.root); ok {
			p = b.Apply(p)
		}
	}
	return sandbox.Command(ctx, p, name, args...)
}

// SpawnCeiling judges child, written or not, against its parent's row by
// [tier.Ceiling]: an [*tier.AboveParentError] for a child ranked above its parent. A
// child with no parent, or a side with no recorded tier, is bounded by nothing and
// passes.
//
// Without [WithTiers] it is an [*UnconfiguredError]. A parent the store does not hold
// wraps [job.ErrUnknownJob], and a recorded tier the set does not declare at its rank is
// an [*AssignmentError].
func (s *SafetyScissors) SpawnCeiling(ctx context.Context, child job.Job) error {
	set, err := s.Tiers()
	if err != nil {
		return err
	}
	if child.Parent == "" {
		return nil
	}
	parentRow, err := s.row(ctx, child.Parent)
	if err != nil {
		return err
	}
	parent, err := declared(set, parentRow)
	if err != nil {
		return err
	}
	kid, err := declared(set, child)
	if err != nil {
		return err
	}
	return tier.Ceiling(parent, kid)
}

// declared is row's recorded tier in set, the zero Tier when it records none.
func declared(set *tier.Set, row job.Job) (tier.Tier, error) {
	if row.ModelTier == nil {
		return tier.Tier{}, nil
	}
	t, err := set.Parse(row.ModelTier.Name)
	if err != nil {
		return tier.Tier{}, &AssignmentError{Job: row.ID, Assignment: *row.ModelTier, Err: err}
	}
	if t.Rank() != row.ModelTier.Rank {
		return tier.Tier{}, &AssignmentError{Job: row.ID, Assignment: *row.ModelTier}
	}
	return t, nil
}

func (s *SafetyScissors) row(ctx context.Context, id string) (job.Job, error) {
	rows, err := s.jobs.List(ctx)
	if err != nil {
		return job.Job{}, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id })
	if i < 0 {
		return job.Job{}, fmt.Errorf("%w: %q", job.ErrUnknownJob, id)
	}
	return rows[i], nil
}
