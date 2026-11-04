package types

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
)

// Unit is the smallest piece of work a CI shard plan places. It is one target
// a project's anchor (ci) asks for through ctx.needs, under the plan's charms
// and args. It carries no args: ctx.needs hands a step none.
type Unit struct {
	Project string `json:"project"`
	Target  string `json:"target"`
}

// String is the unit as project:target, the key ties break on.
func (u Unit) String() string { return u.Project + ":" + u.Target }

// DirectCall is a target an anchor's body calls as a function instead of
// through ctx.needs. The recorder cannot see it, so no unit runs it.
type DirectCall struct {
	Project string `json:"project"`
	Anchor  string `json:"anchor"`
	Target  string `json:"target"`
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

// Duties is every duty a plan places, in placement order.
var Duties = []Duty{DutyCacheExport, DutyNotesVerify}

// Shard is one runner's worth of work in a CI shard plan.
type Shard struct {
	ID string // zero-padded shard index (e.g. "00", "01")
	// Label is UnitsDigest(Units). The same units get the same label on any
	// plan, so a provider can key a per-shard cache by it.
	Label string
	// ProjectPaths are the distinct projects of Units, sorted.
	ProjectPaths []string
	// Units are what the shard runs, in the order the planner placed them.
	Units []Unit
	// Duties are the workspace-wide steps placed on this shard.
	Duties []Duty
}

// UnitsDigest is a short stable digest of units, independent of their order.
func UnitsDigest(units []Unit) string {
	keys := make([]string, len(units))
	for i, u := range units {
		keys[i] = u.String()
	}
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

// RunnerClass is the machine every shard runs on. The plan packs against it
// rather than against the machine that planned, so one change plans the same on
// a laptop and on a runner.
type RunnerClass struct {
	Name     string `json:"name"`
	MemoryMB int    `json:"memory_mb"`
	Cores    int    `json:"cores"`
}

// ShardPlan is a provider-neutral CI shard plan produced by Magus.Plan.
type ShardPlan struct {
	Shards      []Shard
	Source      string // VCS source label (e.g. "git diff vs origin/main")
	MaxParallel int    // recommended concurrency cap; 0 means unlimited
	// Charms and Args are what the anchor was evaluated under. A shard that
	// runs the plan under others runs something else.
	Charms []string
	Args   []string
	// Runner is the class the shards were packed for. Nil when none is
	// configured, and Fallbacks then says no memory budget was checked.
	Runner *RunnerClass
	// Fallbacks says each default the plan fell back to, one sentence each.
	// Empty when every input was given.
	Fallbacks []string
	// Unplanned are the targets an anchor calls directly. No shard runs them.
	Unplanned []DirectCall
	// Sufficient is the smallest shard count that finishes as fast as this plan, so a
	// reader can see what the extra runners bought. 0 when nothing was predictable.
	Sufficient int
	// OverBudget names the shards predicted to exceed one runner's memory, by ID. A
	// runner that runs out of memory disappears and reports "cancelled" with no
	// diagnostics, so this is the only warning there is.
	OverBudget []string
	// Unmeasured are the units and prerequisites with no recorded
	// duration, sorted. The plan weighed each with the fallback.
	Unmeasured []Unit
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

// StepRecorder collects the units a composed target's body asks for, in place
// of running them. A planner evaluates the anchor (ci) under a tracing context
// carrying one. The units are then the steps that body takes under the shard's
// charms and args. Safe for concurrent use.
type StepRecorder struct {
	mu    sync.Mutex
	units []Unit
	seen  map[Unit]bool
}

// NewStepRecorder returns an empty recorder.
func NewStepRecorder() *StepRecorder {
	return &StepRecorder{seen: map[Unit]bool{}}
}

// Record adds u once; a repeat is dropped.
func (r *StepRecorder) Record(u Unit) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.seen[u] {
		r.seen[u] = true
		r.units = append(r.units, u)
	}
}

// Units are the recorded units in the order the body asked for them.
func (r *StepRecorder) Units() []Unit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.units)
}

type stepRecorderKey struct{}

// WithStepRecorder returns ctx carrying r. ctx.needs under it records and runs
// nothing.
func WithStepRecorder(ctx context.Context, r *StepRecorder) context.Context {
	return context.WithValue(ctx, stepRecorderKey{}, r)
}

// StepRecorderFromContext returns the recorder WithStepRecorder stored, or nil.
func StepRecorderFromContext(ctx context.Context) *StepRecorder {
	r, _ := ctx.Value(stepRecorderKey{}).(*StepRecorder)
	return r
}
