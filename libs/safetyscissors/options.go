package safetyscissors

import (
	"path/filepath"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

// Option configures [New]. Options apply in order: a later option replaces what an
// earlier one of the same name set, except [WithJobOptions], which adds to it.
type Option func(*config) error

type config struct {
	invocation string
	start      session.InvocationStart
	invoked    bool
	actor      func() job.Actor
	notices    func(job.Notice)
	clock      func() time.Time
	jobOpts    []job.StoreOption
	dirs       Dirs

	slots     int
	slotOpts  []budget.LimiterOption
	withSlots bool

	machineMB, machineSlots int
	machineOpts             []budget.MachineOption
	withMachine             bool

	tiers     tier.Declaration
	withTiers bool

	root        string
	binds       []sandbox.Bind
	sandboxOpts []sandbox.Option
	withSandbox bool
}

// Dirs is where each store lives. [New] places them under its dir as
// jobs/, sessions/ and activity/; a field set here replaces that one place.
type Dirs struct {
	// Jobs holds jobs.jsonl and the logs beside it (see [job.Open]).
	Jobs string
	// Sessions holds one JSON Lines file per invocation (see [session.Open]).
	Sessions string
	// Activity holds the activity log and its payloads (see [journal.Calls]).
	Activity string
}

func layout(dir string, over Dirs) Dirs {
	d := Dirs{Jobs: filepath.Join(dir, "jobs"), Sessions: filepath.Join(dir, "sessions"), Activity: filepath.Join(dir, "activity")}
	if over.Jobs != "" {
		d.Jobs = over.Jobs
	}
	if over.Sessions != "" {
		d.Sessions = over.Sessions
	}
	if over.Activity != "" {
		d.Activity = over.Activity
	}
	return d
}

// WithInvocation names the invocation this process records sessions as, and the start
// record its first record follows. Required: the module mints no invocation id. An id
// [session.ValidFileID] refuses is New's error.
func WithInvocation(id string, start session.InvocationStart) Option {
	return func(c *config) error {
		c.invocation, c.start, c.invoked = id, start, true
		return nil
	}
}

// WithActor says who the job store writes as, asked on every write. Required: a store
// without one refuses every write.
func WithActor(actor func() job.Actor) Option {
	return func(c *config) error {
		if actor == nil {
			return &OptionError{Option: "WithActor", Problem: "the function is nil"}
		}
		c.actor = actor
		return nil
	}
}

// WithNotices hears what the job store did that no call asked about, such as a rewrite
// dropping a line no writer finished. Required: a store with nowhere to report refuses
// every write, and the module never prints.
func WithNotices(notices func(job.Notice)) Option {
	return func(c *config) error {
		if notices == nil {
			return &OptionError{Option: "WithNotices", Problem: "the function is nil"}
		}
		c.notices = notices
		return nil
	}
}

// WithClock replaces the real clock for the job store, the session writer and the
// machine budget.
func WithClock(clock func() time.Time) Option {
	return func(c *config) error {
		if clock == nil {
			return &OptionError{Option: "WithClock", Problem: "the function is nil"}
		}
		c.clock = clock
		return nil
	}
}

// WithJobOptions passes the job store's other seams through (see [job.StoreOption]).
// They apply before the store options New derives from [WithActor], [WithNotices] and
// [WithClock], which win over the same seam passed here.
func WithJobOptions(opts ...job.StoreOption) Option {
	return func(c *config) error {
		c.jobOpts = append(c.jobOpts, opts...)
		return nil
	}
}

// WithDirs replaces the places [New] lays the stores out at under its dir. Jobs and
// Sessions may not name one directory, since the session reader would read the job
// files as invocations.
func WithDirs(d Dirs) Option {
	return func(c *config) error {
		c.dirs = d
		return nil
	}
}

// WithSlots configures a slot limiter of n slots (see [budget.NewLimiter]). n below 1
// is refused: the limiter reads it as unlimited, which is not a slot count.
func WithSlots(n int, opts ...budget.LimiterOption) Option {
	return func(c *config) error {
		if n < 1 {
			return &OptionError{Option: "WithSlots", Problem: "the slot count is below 1"}
		}
		c.slots, c.slotOpts, c.withSlots = n, opts, true
		return nil
	}
}

// WithMachine configures a machine budget of megabytes and slots (see
// [budget.NewMachine]). A figure below 1 is refused: the budget reads it as unlimited,
// and a caller that means that builds the budget with budget.NewMachine itself.
func WithMachine(megabytes, slots int, opts ...budget.MachineOption) Option {
	return func(c *config) error {
		if megabytes < 1 || slots < 1 {
			return &OptionError{Option: "WithMachine", Problem: "the memory budget and the slot count must both be at least 1"}
		}
		c.machineMB, c.machineSlots, c.machineOpts, c.withMachine = megabytes, slots, opts, true
		return nil
	}
}

// WithTiers configures the model tier set decl declares (see [tier.NewSet]), which
// [SafetyScissors.SpawnCeiling] judges a spawn against. A declaration NewSet refuses is
// New's error.
func WithTiers(decl tier.Declaration) Option {
	return func(c *config) error {
		c.tiers, c.withTiers = decl, true
		return nil
	}
}

// WithSandbox confines every child [SafetyScissors.Command] starts to binds, narrowed
// to a lease's write paths over the checkout at root (see [sandbox.New] and
// [sandbox.LeaseBoundary]). The policy keeps sandbox's Required mode unless opts set
// another, so New refuses a host that cannot confine a child.
func WithSandbox(root string, binds []sandbox.Bind, opts ...sandbox.Option) Option {
	return func(c *config) error {
		if root == "" {
			return &OptionError{Option: "WithSandbox", Problem: "it names no checkout root"}
		}
		c.root, c.binds, c.sandboxOpts, c.withSandbox = root, binds, opts, true
		return nil
	}
}
