package types

import (
	"path"
	"slices"
	"strings"
)

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

// terminal reports whether the unit is done, however it ended. Nothing derives a
// verdict from it - it is what keeps a finished unit out of the overlap report below.
// Unexported because that is its only reader: a client decides what "done" means from
// the state string itself, which is the value the wire carries.
func (s DelegationState) terminal() bool {
	return s == StatePass || s == StateFail || s == StateNoReturn
}

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
// Registered in cmd/magus-utils/boundary_types.go as a RuntimeObject: magus\ledger.put
// and magus\ledger.list (bound in internal/interp/bindings/ledger_ns.go, backed by
// std.MagusPutLedger/MagusListLedger) return one. VCSCheckpoint - the value Checkpoint
// holds - stays unregistered: Checkpoint is a plain string here, the form an
// orchestrator has at spawn time, so there is no struct to mirror yet.
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
	// Releases are the paths this unit gave up, each with the content digest the path
	// carried at that moment. Store-computed and output-only, like the timestamps: a
	// worker announces a release by shrinking OwnedPaths, and the digest is what the
	// next agent needs to tell whether it inherited the file the releaser left.
	Releases []DelegationRelease `json:"releases,omitempty" yaml:"releases,omitempty"`
	// Created and Updated are unix seconds, stamped by the store on write and
	// output-only to callers - a client-supplied timestamp is a fact about the client's
	// clock, not about when the row was recorded.
	//
	// Updated is the row's heartbeat. A unit that re-puts its row on every state change
	// keeps it moving; a row nobody touches goes stale, and a reader may then judge the
	// unit possibly dead. That judgment is the READER'S - nothing here transitions a row
	// on its own, and silence has no verdict in it.
	Created int64 `json:"created" yaml:"created"`
	Updated int64 `json:"updated" yaml:"updated"`
}

// Digests that are not a content hash. A digest is `sha256:<hex>` of the file's bytes
// when the path held one; these say why it could not be, so a reader is never handed a
// hash-shaped value that is not a hash. Named for the FIELD they land in
// (DelegationRelease.Digest) rather than for releases, which they do not classify.
const (
	// DigestAbsent is a path with nothing on disk when it was released: a file the
	// unit deleted, or a declared glob, which is a pattern rather than a path.
	DigestAbsent = "absent"
	// DigestDir is a directory. A tree has no single content digest, and hashing one
	// on every put would walk it, so the next agent is told to go look instead.
	DigestDir = "dir"
	// DigestUnreadable is a path that IS there and could not be hashed: unreadable,
	// not a regular file, or too large to hash under the store's lock. Distinct from
	// DigestAbsent because "the releaser deleted it" and "something is there nobody
	// could read" send the next agent to different places.
	DigestUnreadable = "unreadable"
)

// DelegationRelease is one path a unit stopped owning, and the version of it the
// next agent inherits.
//
// The skill has workers release a contested path as soon as they finish EDITING it
// rather than at exit, so a waiter can start against it during validation. Digest is
// what makes that safe to act on: it identifies the file the releaser left behind, and
// a mismatch at verification time means the waiter built on a tree the releaser never
// saw.
type DelegationRelease struct {
	Path   string `json:"path"   yaml:"path"`
	Digest string `json:"digest" yaml:"digest"`
	// ReleasedAt is unix seconds, stamped by the store on the put that dropped the path.
	ReleasedAt int64 `json:"released_at" yaml:"released_at"`
}

// DelegationOverlap is two units whose declared OwnedPaths intersect. A FACT the
// reader is handed, never a verdict: two units may share a path because their author
// meant them to run in sequence, or because nobody noticed. Nothing here blocks,
// gates, or reorders anything.
type DelegationOverlap struct {
	// UnitA and UnitB are the unit ids, in ledger order - UnitA was recorded first.
	UnitA string `json:"unit_a" yaml:"unit_a"`
	UnitB string `json:"unit_b" yaml:"unit_b"`
	// PathsA and PathsB are the intersecting declarations from each side, deduped and
	// kept apart. They are rarely the same string - "internal/ledger" and
	// "internal/ledger/store.go" intersect - so one merged list left a reader unable to
	// tell which unit claimed which, which is the only thing they can act on.
	PathsA []string `json:"paths_a" yaml:"paths_a"`
	PathsB []string `json:"paths_b" yaml:"paths_b"`
}

// DelegationReport is what a reader of the ledger is served: the recorded rows, plus
// the overlaps derived from them. A constructor rather than a literal at each read
// door, because the MCP tool and the console's route must not be able to disagree
// about whether an overlap exists - the same reason types.NewFileReport exists.
type DelegationReport struct {
	Units    []DelegationUnit    `json:"units"              yaml:"units"`
	Overlaps []DelegationOverlap `json:"overlaps,omitempty" yaml:"overlaps,omitempty"`
}

// NewDelegationReport wraps the rows and derives the overlaps. Derived on READ and
// never stored: an overlap is a relation between two rows, so storing it on either
// one would mean a row that stopped being true when its neighbor changed.
//
// The single door onto a report, which is why the empty case is normalized HERE: an
// unwritten ledger serves "units":[] rather than null, and the MCP tool and the HTTP
// route would otherwise each have to decide that for themselves.
func NewDelegationReport(units []DelegationUnit) DelegationReport {
	if units == nil {
		units = []DelegationUnit{}
	}
	return DelegationReport{Units: units, Overlaps: delegationOverlaps(units)}
}

// delegationOverlaps reports every pair of units whose declared owned paths
// intersect, in ledger order.
//
// A unit in a terminal state is not in any pair. A released or finished unit is not
// competing for a path - that is the whole shape of the skill's early-release rule,
// where a worker shrinks its owned paths so a waiter can start - and reporting one
// would make the surface noisiest exactly when the plan is winding down.
func delegationOverlaps(units []DelegationUnit) []DelegationOverlap {
	var out []DelegationOverlap
	for i, a := range units {
		if a.State.terminal() || len(a.OwnedPaths) == 0 {
			continue
		}
		for _, b := range units[i+1:] {
			if b.State.terminal() || len(b.OwnedPaths) == 0 {
				continue
			}
			if pa, pb := intersectingPaths(a.OwnedPaths, b.OwnedPaths); len(pa) > 0 {
				out = append(out, DelegationOverlap{UnitA: a.ID, UnitB: b.ID, PathsA: pa, PathsB: pb})
			}
		}
	}
	return out
}

// intersectingPaths collects the declared paths that cover common ground, each side
// kept in its own list and deduped.
func intersectingPaths(a, b []string) (pathsA, pathsB []string) {
	for _, pa := range a {
		for _, pb := range b {
			if !pathsIntersect(pa, pb) {
				continue
			}
			if !slices.Contains(pathsA, pa) {
				pathsA = append(pathsA, pa)
			}
			if !slices.Contains(pathsB, pb) {
				pathsB = append(pathsB, pb)
			}
		}
	}
	return pathsA, pathsB
}

// pathsIntersect decides whether two DECLARED paths cover common ground. Nothing else
// in magus compares owned paths yet, so this is the definition rather than a copy of
// one: containment on the cleaned paths, with a glob truncated to the literal prefix
// it can be judged by.
//
// Truncating is deliberate over-reporting. "console/src/**/*.ts" and
// "console/src/**/*.css" cover no common file, and this reports them anyway because
// both reduce to "console/src"; deciding whether two arbitrary globs can ever match
// one path is a solver, and a missed collision costs a reader far more than a pair
// they look at and dismiss.
func pathsIntersect(a, b string) bool {
	// An entry that names nothing claims nothing. It cleans to ".", which the whole-tree
	// rule below would then read as a claim on everything, pairing a row that holds one
	// stray blank with every other unit in the plan.
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	a, b = literalPrefix(a), literalPrefix(b)
	if a == "" || b == "" {
		// A pattern with no literal prefix ("**/*.go") claims the whole tree.
		return true
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// literalPrefix is the part of a declared path that names actual directories: the
// cleaned path itself when it holds no glob metacharacter, and everything above the
// first segment that does when it holds one.
func literalPrefix(p string) string {
	p = path.Clean(strings.TrimSpace(p))
	if p == "." || p == "/" {
		return ""
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if strings.ContainsAny(s, "*?[{") {
			return strings.Join(segs[:i], "/")
		}
	}
	return p
}

// Clone returns a deep copy: the slice fields are the only shared state, so copying
// them is what makes a value handed out of a store safe to keep. slices.Clone
// preserves nil, so a row that stored null does not come back as [].
func (u DelegationUnit) Clone() DelegationUnit {
	c := u
	c.OwnedPaths = slices.Clone(u.OwnedPaths)
	c.ForbiddenPaths = slices.Clone(u.ForbiddenPaths)
	c.DependsOn = slices.Clone(u.DependsOn)
	c.Releases = slices.Clone(u.Releases)
	return c
}
