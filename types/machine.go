package types

import "time"

// MachineClaim is one step's request on the machine-wide admission budget.
//
// It crosses the proc socket AND lands in `magus status`, which is why it lives here
// rather than in the package that arbitrates it: the engine, the daemon, and every
// status renderer read one definition. Two shapes projected onto each other drifted
// their field names within a week of being written.
//
// Numeric fields use omitzero because the jsonv2 codec's omitempty does not omit 0.
type MachineClaim struct {
	Project    string `json:"project" yaml:"project"`
	Target     string `json:"target" yaml:"target"`
	DeclaredBy string `json:"declared_by,omitempty" yaml:"declared_by,omitempty"` // absent when that is Target itself
	MemoryMB   int    `json:"memory_mb,omitzero" yaml:"memory_mb,omitempty"`
	Slots      int    `json:"slots,omitzero" yaml:"slots,omitempty"`
	PID        int    `json:"pid" yaml:"pid"`
	// Dir is where the claiming run was started. One daemon serves every worktree, so a
	// pid alone does not say which tree to go and look at.
	Dir string `json:"dir,omitempty" yaml:"dir,omitempty"`
	// Invocation is this run's own reference, recorded so a DESCENDANT magus can tell a
	// claim it runs underneath from one competing with it.
	Invocation string `json:"invocation,omitempty" yaml:"invocation,omitempty"`
	// Ancestors is the invocations this claim runs underneath, which do not count
	// against it.
	Ancestors []string `json:"ancestors,omitempty" yaml:"ancestors,omitempty"`
}

// MachineClaimant is one step's hold on, or place in the queue for, the machine budget.
//
// One type for both because a reader asks the same question of each - who is this, and
// where - and the only difference is which list it appears in. Since is when the claim
// was granted, or when the waiter first asked.
type MachineClaimant struct {
	Project  string    `json:"project" yaml:"project"`
	Target   string    `json:"target" yaml:"target"`
	PID      int       `json:"pid,omitzero" yaml:"pid,omitempty"`
	MemoryMB int       `json:"memory_mb,omitzero" yaml:"memory_mb,omitempty"`
	Slots    int       `json:"slots,omitzero" yaml:"slots,omitempty"`
	Dir      string    `json:"dir,omitempty" yaml:"dir,omitempty"`
	Since    time.Time `json:"since,omitempty" yaml:"since,omitempty"`
}

// MachineVerdict is the budget's answer to one admission request.
type MachineVerdict struct {
	// Granted means the claim is recorded and ID releases it.
	Granted bool   `json:"granted" yaml:"granted"`
	ID      string `json:"id,omitempty" yaml:"id,omitempty"`
	// Fits is false when no state of the machine admits this claim, so waiting is a
	// hang rather than a queue.
	Fits        bool              `json:"fits" yaml:"fits"`
	Holders     []MachineClaimant `json:"holders,omitempty" yaml:"holders,omitempty"`
	Ahead       int               `json:"ahead,omitzero" yaml:"ahead,omitempty"` // waiters queued in front of this one
	BudgetMB    int               `json:"budget_mb,omitzero" yaml:"budget_mb,omitempty"`
	HeldMB      int               `json:"held_mb,omitzero" yaml:"held_mb,omitempty"`
	BudgetSlots int               `json:"budget_slots,omitzero" yaml:"budget_slots,omitempty"`
	HeldSlots   int               `json:"held_slots,omitzero" yaml:"held_slots,omitempty"`
}

// MachineSnapshot is the whole machine budget: what it is, what is spent, and the
// claims spending it. It is what `magus status` reports and what the daemon serves.
type MachineSnapshot struct {
	BudgetMB    int `json:"budget_mb,omitzero" yaml:"budget_mb,omitempty"`
	HeldMB      int `json:"held_mb,omitzero" yaml:"held_mb,omitempty"`
	BudgetSlots int `json:"budget_slots,omitzero" yaml:"budget_slots,omitempty"`
	HeldSlots   int `json:"held_slots,omitzero" yaml:"held_slots,omitempty"`
	// Holders are the steps running against the budget; Waiters are the ones queued for
	// it, oldest first.
	Holders []MachineClaimant `json:"holders,omitempty" yaml:"holders,omitempty"`
	Waiters []MachineClaimant `json:"waiters,omitempty" yaml:"waiters,omitempty"`
}
