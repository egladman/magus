package types

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
)

const (
	// RootProject is the path of the workspace root's project.
	RootProject = "."
	// MagusfileName is the file a project declares its targets in.
	MagusfileName = "magusfile.buzz"
)

// Unit is the smallest piece of work a CI shard plan places. It is one target
// a project's anchor (ci) asks for through ctx.needs, under the plan's charms
// and args. It carries no args: ctx.needs hands a step none.
type Unit struct {
	Project string `json:"project"`
	Target  string `json:"target"`
}

// String is the unit as project:target, the key ties break on.
func (u Unit) String() string { return fmt.Sprintf("%s:%s", u.Project, u.Target) }

// UnitNames is each unit as project:target, in order.
func UnitNames(units []Unit) []string {
	names := make([]string, len(units))
	for i, u := range units {
		names[i] = u.String()
	}
	return names
}

// DirectCall is a target an anchor's body calls as a function instead of
// through ctx.needs. The recorder cannot see it, so no unit would run it.
type DirectCall struct {
	Project string `json:"project"`
	Anchor  string `json:"anchor"`
	Target  string `json:"target"`
}

// String is the call as project:anchor calls target.
func (c DirectCall) String() string {
	return fmt.Sprintf("%s:%s calls %s", c.Project, c.Anchor, c.Target)
}

// Duty is a workspace-wide CI step the plan places on exactly one shard, so a
// provider runs it once instead of guessing which shard should.
type Duty string

const (
	// DutyCacheExport saves the spells' cache bundle. It goes to the shard
	// holding the most units of the workspace root.
	DutyCacheExport Duty = "cache-export"
	// DutyNotesVerify checks the shared notes store's anchors. It goes to the
	// shard with the fewest units.
	DutyNotesVerify Duty = "notes-verify"
)

// Shard is one runner's worth of work in a CI shard plan.
type Shard struct {
	ID string // zero-padded shard index (e.g. "00", "01")
	// Digest is UnitsDigest(Units). The same units get the same digest on any
	// plan, so a provider can key a per-shard cache by it.
	Digest string
	// ProjectPaths are the distinct projects of Units, sorted.
	ProjectPaths []string
	// Units are what the shard runs, in the order the planner placed them.
	Units []Unit
	// Duties are the workspace-wide steps placed on this shard.
	Duties []Duty
}

// UnitsDigest is a short stable digest of units, independent of their order.
func UnitsDigest(units []Unit) string {
	keys := UnitNames(units)
	slices.Sort(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(sum[:6])
}

// UnitProjects is the distinct projects of units, sorted.
func UnitProjects(units []Unit) []string {
	paths := make([]string, 0, len(units))
	for _, u := range units {
		paths = append(paths, u.Project)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// Machine is the size of the machine every shard runs on. The plan packs
// against it rather than against the host that planned, so one change plans
// the same on a laptop and on CI.
type Machine struct {
	Name     string `json:"name"`
	MemoryMB int    `json:"memory_mb"`
	Cores    int    `json:"cores"`
}

// Fallback is one default a plan used because an input was missing. Each
// field is one sentence, so a summary can lay them out as table columns.
type Fallback struct {
	// What is the default the plan used, e.g. "At most 8 shards".
	What string `json:"what"`
	// Why names the missing input.
	Why string `json:"why"`
	// Effect is what the default means for the run, and how to stop it.
	Effect string `json:"effect"`
}

// String is the fallback on one line, for a log.
func (f Fallback) String() string {
	return f.What + ": " + f.Why + ". " + f.Effect
}

// ShardPlan is a provider-neutral CI shard plan produced by Magus.Plan.
type ShardPlan struct {
	// StageZero is the stage every shard of Shards waits for. Its shards
	// compute, once, each prerequisite two or more of Shards would
	// otherwise both compute. Shards then replay them from the cache stage
	// zero exports, or compute them again when that cache is missing. Empty
	// when no shard shares such a prerequisite. Its shards carry no duties.
	StageZero []Shard
	// Shards are the last stage, the one every plan has.
	Shards      []Shard
	Source      string // VCS source label (e.g. "git diff vs origin/main")
	MaxParallel int    // recommended concurrency cap; 0 means unlimited
	// Charms and Args are what the anchor was evaluated under. A shard that
	// runs the plan under others runs something else.
	Charms []string
	Args   []string
	// Machine is what the shards were packed for. Nil when none is
	// configured, and Fallbacks then says no memory budget was checked.
	Machine *Machine
	// Fallbacks says each default the plan fell back to. Empty when every
	// input was given.
	Fallbacks []Fallback
	// Sufficient is the fewest shards that pack by duration as fast as
	// this plan's count. Memory splits are not counted. 0 when no unit
	// outlasts setup.
	Sufficient int
	// OverBudget names the shards predicted to exceed one runner's memory, by ID. A
	// runner that runs out of memory disappears and reports "cancelled" with no
	// diagnostics, so this is the only warning there is. It names shards of
	// both stages.
	OverBudget []string
	// Unmeasured are the units and prerequisites with no recorded
	// duration, sorted. The plan weighed each with the fallback.
	Unmeasured []Unit
	// Unreached are the units of affected projects whose closures read no
	// changed path, sorted. Each replays from the cache, so no shard runs it.
	Unreached []Unit
	// Affected is every project the change set reaches, sorted: the closure the shards
	// were cut from, before any sharding or filtering.
	Affected []string
	// UnboundedBy, when set, says why Affected is not a proof: the change set edits a
	// file that can move the graph's edges or every project's build (a declaration, a
	// dependency manifest, a toolchain pin), touches a file no project claims, or the
	// VCS could not diff and the plan fell back to every project. A consumer that relies
	// on two closures being disjoint (a merge queue) must treat it as overlapping
	// everything.
	UnboundedBy string
}

// StepLog collects the units a composed target's body asks for, in place
// of running them. A planner evaluates the anchor (ci) under a tracing context
// carrying one. The units are then the steps that body takes under the shard's
// charms and args. Safe for concurrent use.
type StepLog struct {
	mu      sync.Mutex
	units   []Unit
	seen    map[Unit]bool
	effects []string
}

// NewStepLog returns an empty log.
func NewStepLog() *StepLog {
	return &StepLog{seen: map[Unit]bool{}}
}

// Record adds u once; a repeat is dropped.
func (r *StepLog) Record(u Unit) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.seen[u] {
		r.seen[u] = true
		r.units = append(r.units, u)
	}
}

// Units are the recorded units in the order the body asked for them.
func (r *StepLog) Units() []Unit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.units)
}

// Effects are the host operations the traced body asked for and the trace
// stubbed, each once, in the order asked. A shard runs only the recorded
// units, so none of these would run.
func (r *StepLog) Effects() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.effects)
}

func (r *StepLog) effect(op string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.effects, op) {
		r.effects = append(r.effects, op)
	}
}

type stepLogKey struct{}

// WithStepLog returns ctx carrying r. ctx.needs under it records and runs
// nothing.
func WithStepLog(ctx context.Context, r *StepLog) context.Context {
	return context.WithValue(ctx, stepLogKey{}, r)
}

// StepLogFromContext returns the log WithStepLog stored, or nil.
func StepLogFromContext(ctx context.Context) *StepLog {
	r, _ := ctx.Value(stepLogKey{}).(*StepLog)
	return r
}
