package types

import "slices"

// DelegationState is where one delegated unit stands. The three terminal values are
// the point of the set: a row that never reaches one is a row nobody closed.
//
// NoReturn is deliberately distinct from Fail. A worker that died, stalled, or was
// killed produced no verdict at all, and folding that into "failed" claims a judgment
// nobody made - the root agent still has to go look. Silence is not a pass, and it is
// not a failure either.
type DelegationState string

const (
	// StateDeclared is a row written before its worker was spawned.
	StateDeclared DelegationState = "declared"
	// StateRunning is a worker in flight.
	StateRunning DelegationState = "running"
	// StatePass is a unit whose acceptance criteria and assigned validation both
	// passed, as judged by the agent that owns it.
	StatePass DelegationState = "pass"
	// StateFail is a unit that returned and did not meet its criteria.
	StateFail DelegationState = "fail"
	// StateNoReturn is a unit that never reported: dead, stalled, or cancelled.
	StateNoReturn DelegationState = "no_return"
)

// DelegationUnit is one row of an orchestrating agent's delegation ledger: what that
// agent DECLARED about a piece of work it handed out, recorded so a human can see the
// plan the agents are running.
//
// FACTS ONLY, NEVER ENFORCEMENT. magus records these rows and does nothing else with
// them. OwnedPaths and ForbiddenPaths are what an orchestrator said it intended, not a
// boundary anything checks - no write is blocked, no run is gated, no verdict is
// derived. The skill that defines this vocabulary says the same thing about the prompt
// text these rows mirror: ownership is checked by comparing the ledger against the
// ACTUAL diff since each unit's Checkpoint, which is an agent's job and not magus's. A
// store that quietly started enforcing would make the ledger something agents route
// around instead of something they keep honestly.
//
// The field set mirrors the ledger table in the magus-delegate-multi-agent skill
// one-for-one, so a row an agent writes down and a row it records here cannot describe
// the same unit differently.
//
// Deliberately NOT registered in cmd/magus-utils/boundary_types.go: no host method
// returns one, so there is no Buzz surface to mirror and registering it would generate
// a type declaration nothing can produce. VCSCheckpoint - the value Checkpoint holds -
// deferred the same registration for the same reason. Register both together on the
// day a magusfile can actually read one.
type DelegationUnit struct {
	// ID is the unit's identity within the plan, and the key Put upserts on. The
	// console joins its drawer rows to agent activity by this value, so an
	// orchestrator should use the same id it puts in the worker's prompt.
	ID string `json:"id" yaml:"id"`
	// Parent is the id of the unit that delegated this one, empty for a unit the root
	// spawned. Depth is read off this chain rather than stored, so a mis-stamped depth
	// cannot disagree with the tree.
	Parent string `json:"parent,omitempty" yaml:"parent,omitempty"`
	// Goal is the unit's goal and its observable acceptance criteria, as one block of
	// text. Not split into two fields: the skill requires criteria to be observable and
	// a separate empty Criteria field would read as "none required" rather than as
	// "the author did not write any".
	Goal string `json:"goal,omitempty" yaml:"goal,omitempty"`
	// Checkpoint is the working state this unit was handed, in the form
	// `magus vcs checkpoint -o name` prints: the revision, plus a dirty-patch digest
	// when the tree was not clean. A string rather than an embedded VCSCheckpoint
	// because that is the form an orchestrator has at spawn time and the form a later
	// reader feeds back to `magus graph diff --rev`.
	Checkpoint string `json:"checkpoint,omitempty" yaml:"checkpoint,omitempty"`
	// OwnedPaths and ForbiddenPaths are the declared write boundary. Empty on a
	// read-only unit BY DESIGN (see ReadOnly), which is why neither is required.
	OwnedPaths     []string `json:"owned_paths,omitempty" yaml:"owned_paths,omitempty"`
	ForbiddenPaths []string `json:"forbidden_paths,omitempty" yaml:"forbidden_paths,omitempty"`
	// DependsOn are the ids of units that must land before this one, so a reader can
	// see the ordering the orchestrator committed to.
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// Tier is the effort tier the work was matched to (principal, standard, economy in
	// the skill's table). A free string: hosts name their tiers differently and a
	// closed set here would force a lie for the ones that do not fit.
	Tier string `json:"tier,omitempty" yaml:"tier,omitempty"`
	// Validation is the magus target or named check this unit was assigned, e.g.
	// "magus run test internal/ledger".
	Validation string `json:"validation,omitempty" yaml:"validation,omitempty"`
	// State is the row's lifecycle position. See DelegationState for why no_return is
	// its own value.
	State DelegationState `json:"state,omitempty" yaml:"state,omitempty"`
	// ReadOnly marks the abbreviated row the skill describes: a unit that gathers
	// evidence and writes nothing has no write set, so empty OwnedPaths and
	// ForbiddenPaths are correct rather than missing. Without this flag a reader
	// cannot tell an abbreviated row from one whose author forgot the boundary.
	ReadOnly bool `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	// Created and Updated are unix seconds, stamped by the store on write and
	// output-only to callers - a client-supplied timestamp is a fact about the client's
	// clock, not about when the row was recorded.
	Created int64 `json:"created" yaml:"created"`
	Updated int64 `json:"updated" yaml:"updated"`
}

// Clone returns a deep copy: the slice fields are the only shared state, so copying
// them is what makes a value handed out of a store safe to keep. slices.Clone
// preserves nil, so a row that stored null does not come back as [].
func (u DelegationUnit) Clone() DelegationUnit {
	c := u
	c.OwnedPaths = slices.Clone(u.OwnedPaths)
	c.ForbiddenPaths = slices.Clone(u.ForbiddenPaths)
	c.DependsOn = slices.Clone(u.DependsOn)
	return c
}
