package safetyscissors

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

// Config is what [New] opens the harness from. Every exported field is required; each
// optional part is set by an [Option].
type Config struct {
	// Dir is where the stores live, laid out as [Dirs] says.
	Dir string
	// Invocation is the id this process records sessions as: the module mints none. An
	// id [session.ValidFileID] refuses is a [*session.InvalidIDError].
	Invocation string
	// Start is the start record the invocation's first record follows.
	Start session.InvocationStart
	// Actor says who the job store writes as, asked on every write.
	Actor func() job.Actor
	// Notices hears what the job store did that no call asked about, such as a rewrite
	// dropping a line no writer finished. The module never prints.
	Notices func(job.Notice)
	// CheckSpec is how the job store reads the checks a row and its postconditions carry
	// (see [job.WithCheckSpec]).
	CheckSpec job.CheckSpec
	// Retention and PruneErrors configure the prune opening the session writer runs (see
	// [session.Config]): how long an invocation file outlives its newest fact, and who
	// hears what went wrong.
	Retention   time.Duration
	PruneErrors func(context.Context, error)

	clock   func() time.Time
	jobOpts []job.Option
	dirs    Dirs

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

	errs []error
}

// Option adjusts a [Config] before [New] reads it. Options apply in order: a later option
// replaces what an earlier one of the same name set, except [WithJobOptions], which adds
// to it. An option given a nil or invalid value records an *[OptionError] on the Config
// rather than being ignored, so New refuses the harness.
type Option func(*Config)

func (c *Config) fail(option, problem string) {
	c.errs = append(c.errs, &OptionError{Option: option, Problem: problem})
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

// layout is the stores' places, each cleaned so two spellings of one directory compare
// equal.
func layout(dir string, over Dirs) Dirs {
	d := Dirs{Jobs: filepath.Join(dir, "jobs"), Sessions: filepath.Join(dir, "sessions"), Activity: filepath.Join(dir, "activity")}
	if over.Jobs != "" {
		d.Jobs = filepath.Clean(over.Jobs)
	}
	if over.Sessions != "" {
		d.Sessions = filepath.Clean(over.Sessions)
	}
	if over.Activity != "" {
		d.Activity = filepath.Clean(over.Activity)
	}
	return d
}

// shared refuses each pair of stores laid out in one directory: each reader would read
// the other's files as its own.
func (d Dirs) shared() []error {
	places := []struct{ name, dir string }{{"Jobs", d.Jobs}, {"Sessions", d.Sessions}, {"Activity", d.Activity}}
	var errs []error
	for i, a := range places {
		for _, b := range places[i+1:] {
			if a.dir == b.dir {
				errs = append(errs, &OptionError{Option: "WithDirs", Problem: fmt.Sprintf("%s and %s name one directory, %s", a.name, b.name, a.dir)})
			}
		}
	}
	return errs
}

// WithClock replaces the real clock for the job store, the session writer and the
// machine budget. A nil clock is an *[OptionError].
func WithClock(clock func() time.Time) Option {
	return func(c *Config) {
		if clock == nil {
			c.fail("WithClock", "the function is nil")
			return
		}
		c.clock = clock
	}
}

// WithJobOptions passes the job store's other seams through (see [job.Option]). They
// apply before the store options New derives from [Config.Actor], [Config.Notices],
// [Config.CheckSpec] and [WithClock], which win over the same seam passed here. No
// options, or a nil one, is an *[OptionError].
func WithJobOptions(opts ...job.Option) Option {
	return func(c *Config) {
		if len(opts) == 0 || slices.ContainsFunc(opts, func(o job.Option) bool { return o == nil }) {
			c.fail("WithJobOptions", "no options, or a nil one")
			return
		}
		c.jobOpts = append(c.jobOpts, opts...)
	}
}

// WithDirs replaces the places [New] lays the stores out at under its dir. No two of
// Jobs, Sessions and Activity may name one directory, compared cleaned, since each
// reader would read the other's files as its own. A Dirs that sets no field is an
// *[OptionError].
func WithDirs(d Dirs) Option {
	return func(c *Config) {
		if d == (Dirs{}) {
			c.fail("WithDirs", "it sets no directory")
			return
		}
		c.dirs = d
	}
}

// WithSlots configures a slot limiter of n slots (see [budget.NewLimiter]). n below 1
// is refused, because it is not a slot count.
func WithSlots(n int, opts ...budget.LimiterOption) Option {
	return func(c *Config) {
		if n < 1 {
			c.fail("WithSlots", "the slot count is below 1")
			return
		}
		c.slots, c.slotOpts, c.withSlots = n, opts, true
	}
}

// WithMachine configures a machine budget of megabytes and slots (see
// [budget.NewMachine]). A figure below 1 is refused: the budget reads it as unlimited,
// and a caller that means that builds the budget with budget.NewMachine itself.
func WithMachine(megabytes, slots int, opts ...budget.MachineOption) Option {
	return func(c *Config) {
		if megabytes < 1 || slots < 1 {
			c.fail("WithMachine", "the memory budget and the slot count must both be at least 1")
			return
		}
		c.machineMB, c.machineSlots, c.machineOpts, c.withMachine = megabytes, slots, opts, true
	}
}

// WithTiers configures the model tier set decl declares (see [tier.NewSet]), which
// [SafetyScissors.SpawnCeiling] judges a spawn against. A declaration NewSet refuses is
// New's error.
func WithTiers(decl tier.Declaration) Option {
	return func(c *Config) { c.tiers, c.withTiers = decl, true }
}

// WithSandbox confines every child [SafetyScissors.Command] starts to binds, narrowed
// to a lease's write paths over the checkout at root (see [sandbox.New] and
// [sandbox.LeaseBoundary]). The policy keeps sandbox's Required mode unless opts set
// another, so New refuses a host that cannot confine a child. An empty root is an
// *[OptionError].
func WithSandbox(root string, binds []sandbox.Bind, opts ...sandbox.Option) Option {
	return func(c *Config) {
		if root == "" {
			c.fail("WithSandbox", "it names no checkout root")
			return
		}
		c.root, c.binds, c.sandboxOpts, c.withSandbox = root, binds, opts, true
	}
}
