package types

import (
	"fmt"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
)

// MachineClaim is one step's request on the machine-wide admission budget.
//
// It crosses the broker socket AND lands in `magus server status`, so it
// lives here. The engine, the broker and every status renderer read one
// definition. Two shapes projected onto each other drifted
// their field names within a week of being written.
//
// Numeric fields use omitzero because the jsonv2 codec's omitempty does not omit 0.
type MachineClaim struct {
	Project    string `json:"project" yaml:"project"`
	Target     string `json:"target" yaml:"target"`
	DeclaredBy string `json:"declared_by,omitempty" yaml:"declared_by,omitempty"` // absent when that is Target itself
	MemoryMB   int    `json:"memory_mb,omitzero" yaml:"memory_mb,omitempty"`
	// Profile is the curve MemoryMB peaks over, which the broker packs claims by. A zero
	// profile (Runs 0) is a claim held flat at MemoryMB.
	Profile MemoryProfile `json:"profile,omitzero" yaml:"profile,omitempty"`
	// Sizing is where MemoryMB came from, so a refusal can say whether it rests on a
	// declaration or on measured runs.
	Sizing MemorySizing `json:"sizing,omitzero" yaml:"sizing,omitempty"`
	Slots  int          `json:"slots,omitzero" yaml:"slots,omitempty"`
	PID    int          `json:"pid" yaml:"pid"`
	// Dir is where the claiming run was started. One broker serves every worktree, so a
	// pid alone does not say which tree to go and look at.
	Dir string `json:"dir,omitempty" yaml:"dir,omitempty"`
	// Command is the holder's argv, space-joined; the broker fills it from the
	// connection when the claim leaves it out.
	Command string `json:"command,omitempty" yaml:"command,omitempty"`
	// Invocation is this run's own reference, recorded so a DESCENDANT magus can tell a
	// claim it runs underneath from one competing with it.
	Invocation string `json:"invocation,omitempty" yaml:"invocation,omitempty"`
	// Ancestors is the invocations this claim runs underneath, which do not count
	// against it.
	Ancestors []string `json:"ancestors,omitempty" yaml:"ancestors,omitempty"`
	// Weight is the Job.Weight of the job the run is bound to, zero for
	// none. It orders the broker's parked requests and decides no verdict.
	Weight float64 `json:"weight,omitzero" yaml:"weight,omitempty"`
	// Under is "<project>:<target>" of the composer an inline member runs
	// under, empty for a claim that runs on its own.
	Under string `json:"under,omitempty" yaml:"under,omitempty"`
	// SlotsFloor is the fewest slots the claim runs with; 0 means Slots.
	SlotsFloor int `json:"slots_floor,omitzero" yaml:"slots_floor,omitempty"`
	// WaitBoundMS is the client's budget_wait or sooner deadline.
	WaitBoundMS int64 `json:"wait_bound_ms,omitzero" yaml:"wait_bound_ms,omitempty"`
	// WaitedMS is set on the ask made at the bound.
	WaitedMS int64 `json:"waited_ms,omitzero" yaml:"waited_ms,omitempty"`
}

// Run is the outermost invocation the claim runs underneath, or its own when it has no
// ancestors; "" for an unstamped claim. Claims sharing a run belong to one invocation
// tree, the siblings of one fan-out.
func (c MachineClaim) Run() string {
	if len(c.Ancestors) > 0 {
		return c.Ancestors[0]
	}
	return c.Invocation
}

// MachineClaimant is one step's hold on the machine budget. Since is when the claim was
// granted.
type MachineClaimant struct {
	Project  string `json:"project" yaml:"project"`
	Target   string `json:"target" yaml:"target"`
	PID      int    `json:"pid,omitzero" yaml:"pid,omitempty"`
	MemoryMB int    `json:"memory_mb,omitzero" yaml:"memory_mb,omitempty"`
	// Profile is the holder's claimed curve, so a refusal can name when the sum breaches.
	Profile MemoryProfile `json:"profile,omitzero" yaml:"profile,omitempty"`
	Sizing  MemorySizing  `json:"sizing,omitzero" yaml:"sizing,omitempty"`
	Slots   int           `json:"slots,omitzero" yaml:"slots,omitempty"`
	Dir     string        `json:"dir,omitempty" yaml:"dir,omitempty"`
	Command string        `json:"command,omitempty" yaml:"command,omitempty"`
	Since   time.Time     `json:"since,omitempty" yaml:"since,omitempty"`
	// Owner is who answers for the holder: its job lease, else `spawned by <job>`, else
	// "unleased". Empty when no connection to the broker names the holder's pid.
	Owner string `json:"owner,omitempty" yaml:"owner,omitempty"`
	// Under is the composer the holder runs under, as on MachineClaim.
	Under string `json:"under,omitempty" yaml:"under,omitempty"`
}

// MachineWaiter is a claim the broker holds until the budget can seat it.
type MachineWaiter struct {
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Target  string `json:"target,omitempty" yaml:"target,omitempty"`
	// PID is the waiting process.
	PID int `json:"pid,omitzero" yaml:"pid,omitempty"`
	// MemoryMB and Slots are the claim it waits to seat.
	MemoryMB int    `json:"memory_mb,omitzero" yaml:"memory_mb,omitempty"`
	Slots    int    `json:"slots,omitzero" yaml:"slots,omitempty"`
	Dir      string `json:"dir,omitempty" yaml:"dir,omitempty"`
	Command  string `json:"command,omitempty" yaml:"command,omitempty"`
	Owner    string `json:"owner,omitempty" yaml:"owner,omitempty"`
	// Tree is the job tree whose validation seat it waits for, "" when it waits on the
	// budget.
	Tree  string    `json:"tree,omitempty" yaml:"tree,omitempty"`
	Since time.Time `json:"since" yaml:"since"`
	// WaitedMS is how long it had waited when the status was read.
	WaitedMS int64 `json:"waited_ms" yaml:"waited_ms"`
}

// AdmissionOutcome is how the broker answered a claim it recorded.
type AdmissionOutcome string

const (
	// AdmissionRefused is a claim turned away with MGS3009.
	AdmissionRefused AdmissionOutcome = "refused"
	// AdmissionSeated is a claim granted after it waited.
	AdmissionSeated AdmissionOutcome = "seated"
)

// MachineAdmission is one admission the broker recorded: the claim it
// answered, and the budget and holders at that moment. It is a line of the
// admissions store, so its encoding lives here.
type MachineAdmission struct {
	At       time.Time        `json:"at" yaml:"at"`
	Outcome  AdmissionOutcome `json:"outcome,omitempty" yaml:"outcome,omitempty"`
	PID      int              `json:"pid" yaml:"pid"`
	Project  string           `json:"project" yaml:"project"`
	Target   string           `json:"target" yaml:"target"`
	Under    string           `json:"under,omitempty" yaml:"under,omitempty"`
	MemoryMB int              `json:"memory_mb,omitzero" yaml:"memory_mb,omitempty"`
	Slots    int              `json:"slots,omitzero" yaml:"slots,omitempty"`
	Dir      string           `json:"dir,omitempty" yaml:"dir,omitempty"`
	Command  string           `json:"command,omitempty" yaml:"command,omitempty"`
	Owner    string           `json:"owner,omitempty" yaml:"owner,omitempty"`
	// Fits is false when no state of the machine admits the claim.
	Fits bool `json:"fits" yaml:"fits"`
	// WaitedMS and WaitBoundMS are how long the claim waited, and the bound.
	WaitedMS    int64             `json:"waited_ms,omitzero" yaml:"waited_ms,omitempty"`
	WaitBoundMS int64             `json:"wait_bound_ms,omitzero" yaml:"wait_bound_ms,omitempty"`
	Sizing      MemorySizing      `json:"sizing,omitzero" yaml:"sizing,omitempty"`
	BudgetMB    int               `json:"budget_mb,omitzero" yaml:"budget_mb,omitempty"`
	HeldMB      int               `json:"held_mb,omitzero" yaml:"held_mb,omitempty"`
	BudgetSlots int               `json:"budget_slots,omitzero" yaml:"budget_slots,omitempty"`
	HeldSlots   int               `json:"held_slots,omitzero" yaml:"held_slots,omitempty"`
	Holders     []MachineClaimant `json:"holders,omitempty" yaml:"holders,omitempty"`
}

// machineAdmissionWire has MachineAdmission's fields and tags but none of its
// methods, so encoding it cannot call MarshalJSON again.
type machineAdmissionWire MachineAdmission

// MarshalJSON encodes the admission through its wire shape.
func (a MachineAdmission) MarshalJSON() ([]byte, error) {
	return json.Marshal(machineAdmissionWire(a))
}

// UnmarshalJSON decodes an admission from its wire shape.
func (a *MachineAdmission) UnmarshalJSON(b []byte) error {
	var w machineAdmissionWire
	if err := json.Unmarshal(b, &w); err != nil {
		return fmt.Errorf("decode machine admission: %w", err)
	}
	*a = MachineAdmission(w)
	return nil
}

// MachineVerdict is the budget's answer to one admission request.
type MachineVerdict struct {
	// Granted means the claim is recorded and ID releases it.
	Granted bool   `json:"granted" yaml:"granted"`
	ID      string `json:"id,omitempty" yaml:"id,omitempty"`
	// Fits is false when no state of the machine admits this claim, so waiting is a
	// hang rather than a queue.
	Fits bool `json:"fits" yaml:"fits"`
	// OwnRun is set on a claim that is not granted but would fit beside every claim
	// outside its own process and run. What keeps it out will be released by the
	// requester's own run, so the requester waits rather than refusing.
	OwnRun bool `json:"own_run,omitzero" yaml:"own_run,omitempty"`
	// Packed is true when the verdict judged memory by curve, and false when every
	// claim it weighed was a peak held flat, or the broker predates curves.
	Packed      bool              `json:"packed,omitempty" yaml:"packed,omitempty"`
	Holders     []MachineClaimant `json:"holders,omitempty" yaml:"holders,omitempty"`
	BudgetMB    int               `json:"budget_mb,omitzero" yaml:"budget_mb,omitempty"`
	HeldMB      int               `json:"held_mb,omitzero" yaml:"held_mb,omitempty"`
	BudgetSlots int               `json:"budget_slots,omitzero" yaml:"budget_slots,omitempty"`
	HeldSlots   int               `json:"held_slots,omitzero" yaml:"held_slots,omitempty"`
	// Slots is what a grant seated, floor to Slots; 0 from a broker without
	// grants.
	Slots int `json:"slots,omitzero" yaml:"slots,omitempty"`
	// Ahead is the parked claims ahead of this one, on a parked verdict.
	Ahead int `json:"ahead,omitzero" yaml:"ahead,omitempty"`
}

// MachineSnapshot is the whole machine budget: what it is, what is spent, and the
// claims spending it. Status reports it and the broker serves it.
type MachineSnapshot struct {
	BudgetMB    int `json:"budget_mb,omitzero" yaml:"budget_mb,omitempty"`
	HeldMB      int `json:"held_mb,omitzero" yaml:"held_mb,omitempty"`
	BudgetSlots int `json:"budget_slots,omitzero" yaml:"budget_slots,omitempty"`
	HeldSlots   int `json:"held_slots,omitzero" yaml:"held_slots,omitempty"`
	// Holders are the steps running against the budget, oldest first.
	Holders []MachineClaimant `json:"holders,omitempty" yaml:"holders,omitempty"`
	// Waiting is every caller blocked on a seat now, longest waiting first.
	Waiting []MachineWaiter `json:"waiting,omitempty" yaml:"waiting,omitempty"`
	// Refused is the last admissions the broker recorded, newest first.
	Refused []MachineAdmission `json:"refused,omitempty" yaml:"refused,omitempty"`
	// Reserve is what BudgetMB leaves the OS and desktop, and why.
	Reserve MemoryReserve `json:"reserve,omitzero" yaml:"reserve,omitempty"`
}

// MemoryReserve is the memory kept back from build work: TotalMB less
// ReservedMB is the budget. Rule says which profile set it.
type MemoryReserve struct {
	TotalMB    int    `json:"total_mb,omitzero" yaml:"total_mb,omitempty"`
	ReservedMB int    `json:"reserved_mb,omitzero" yaml:"reserved_mb,omitempty"`
	Rule       string `json:"rule,omitempty" yaml:"rule,omitempty"`
}

// HolderList names each holder as its pid, its command (or its target and project when it
// carries none) and how long it has held its seat at now, comma-joined.
func (s MachineSnapshot) HolderList(now time.Time) string {
	holders := make([]string, len(s.Holders))
	for i, h := range s.Holders {
		what := fmt.Sprintf("%s %s", h.Target, h.Project)
		if h.Command != "" {
			what = fmt.Sprintf("`%s`", h.Command)
		}
		holders[i] = fmt.Sprintf("pid %d (%s) for %s", h.PID, what, now.Sub(h.Since).Round(time.Second))
	}
	return strings.Join(holders, ", ")
}
