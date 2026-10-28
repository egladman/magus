package sessions

import (
	"fmt"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
)

// KindGateResult records how a whole ci gate came out: pass or fail, at which
// commit, under which input fingerprint. One record per completed gate run.
//
// It lives in THIS store rather than the forecast history or the run journal,
// because the redundancy check needs exactly this store's scope: the history
// file is user-global (a sibling repo's branch of the same name would read as
// this one's), and the run journal dies with its worktree. The session store is
// per-repository, shared by every worktree, and survives both worktree deletion
// and server restarts.
const KindGateResult = "gate_result"

// OutcomeDeferred marks a gate invocation that was REFUSED as redundant under
// load (MGS3010, exit 75) rather than run. It is a record of the deferral
// decision, kept so the decision is interrogable after the fact; it is never a
// verdict on the inputs, which is why [LatestGate] skips it.
const OutcomeDeferred = "deferred"

// GateScope names what a gate's verdict covers.
//
// The zero value is an entry written before gate records named their scope. It covers
// what a project scope covers, and a reader that skips it for that reason says so through
// [GateScope.Unscoped] rather than reading it as either scope silently.
type GateScope string

const (
	// GateScopeAffected is a gate over every project the branch affects against its
	// default base: `magus affected ci`, or `magus run ci` over a superset of that set.
	// Only this scope speaks for the branch.
	GateScopeAffected GateScope = "affected"
	// GateScopeProject is a gate over named projects that leaves an affected project out,
	// such as `magus run ci libs/cmdline`. Its pass proves those projects alone.
	GateScopeProject GateScope = "project"
)

// CoversBranch reports whether a verdict of this scope speaks for the whole branch.
func (s GateScope) CoversBranch() bool { return s == GateScopeAffected }

// Unscoped reports whether the entry predates gate scopes.
func (s GateScope) Unscoped() bool { return s == "" }

// MarshalJSON refuses the zero value: a record written now that names no scope would
// read back as a pre-scope entry.
func (s GateScope) MarshalJSON() ([]byte, error) {
	switch s {
	case GateScopeAffected, GateScopeProject:
		return json.Marshal(string(s))
	default:
		return nil, fmt.Errorf("gate result scope %q: want %q or %q", string(s), GateScopeAffected, GateScopeProject)
	}
}

// UnmarshalJSON accepts only a known scope. An entry with no scope member never reaches
// it and keeps the zero value.
func (s *GateScope) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch GateScope(v) {
	case GateScopeAffected, GateScopeProject:
		*s = GateScope(v)
		return nil
	default:
		return fmt.Errorf("gate result scope %q: want %q or %q", v, GateScopeAffected, GateScopeProject)
	}
}

// GateResult is the payload of a [KindGateResult] record.
type GateResult struct {
	// Target is the gate target the run anchored on (normally "ci").
	Target string `json:"target"`
	// Scope is what the verdict covers; see [GateScope].
	Scope GateScope `json:"scope"`
	// Ref is the movable branch name the gate ran on (types.VCSMeta.Ref).
	Ref string `json:"ref"`
	// Commit is the revision the gate ran at. The working tree may have been
	// dirty on top of it; the Fingerprint is what captures that exactly.
	Commit string `json:"commit"`
	// Outcome is OutcomePass, OutcomeFail, or OutcomeDeferred.
	Outcome string `json:"outcome"`
	// Fingerprint condenses the per-step cache keys the gate ran under
	// (ci.GateFingerprint), so a later run can tell "identical inputs"
	// apart from "same commit".
	Fingerprint string `json:"fingerprint,omitempty"`
	// Projects are the project paths the gate covered. A later gate defers to
	// this record only when its own selection is a subset.
	Projects []string `json:"projects,omitempty"`
	// Charms is the sorted charm set the gate ran under. A gate under
	// different charms verifies different things, so equivalence requires an
	// equal set.
	Charms []string `json:"charms,omitempty"`
	// UndeclaredSeeds are the projects this gate covered that a changed file no
	// project declares put in the set on its own (MGS1028): work the gate paid for
	// whose answer could not have moved. Recorded per gate so the debt is countable
	// across a branch rather than only visible in the run that printed it.
	UndeclaredSeeds []string `json:"undeclared_seeds,omitempty"`
	// Tier is the risk tier a sized gate ran at (types.RiskTier); empty is the full
	// gate. A sized pass is a verdict on the change, since the tier proved the rest of
	// the gate could not observe it.
	Tier string `json:"tier,omitempty"`
	// Inv is the invocation id of the run that produced this verdict, joining
	// it to the execution journal (`magus query <inv>`). Empty on a deferral:
	// nothing ran, so there is no journal to join.
	Inv string `json:"inv,omitempty"`
	// DeferredTo is set on an OutcomeDeferred record: the commit of the green
	// gate the refusal pointed at.
	DeferredTo string `json:"deferred_to,omitempty"`
}

// BranchVerdict reports whether this result is a verdict on the branch: a pass or fail
// over every affected project, or a fail of any scope. A project's failure is a red
// project wherever it ran; a project's pass proves nothing about the projects it left out.
func (g GateResult) BranchVerdict() bool {
	switch g.Outcome {
	case OutcomeFail:
		return true
	case OutcomePass:
		return g.Scope.CoversBranch()
	default:
		return false
	}
}

// GateRecord is one gate result with the envelope's timestamp resolved.
type GateRecord struct {
	GateResult
	At time.Time
}

// LatestGate returns the newest gate VERDICT on the branch for (ref, target); see
// [GateResult.BranchVerdict]. The newest verdict wins on purpose: a fail recorded after a
// pass means the branch is red, and a redundancy check must see that rather than the stale
// green behind it. A project-scoped pass is skipped, so `magus run ci <project>` never
// reads as the branch gate. Deferral records are skipped: a deferral is not a verdict on
// the inputs, and letting one shadow the green gate it points at would make the check
// inert after its own first refusal.
func LatestGate(fold Fold, ref, target string) (GateRecord, bool) {
	for i := len(fold.Records) - 1; i >= 0; i-- {
		rec := fold.Records[i]
		if rec.Kind != KindGateResult {
			continue
		}
		var g GateResult
		if json.Unmarshal(rec.Payload, &g) != nil {
			continue
		}
		if g.Ref != ref || g.Target != target || !g.BranchVerdict() {
			continue
		}
		return GateRecord{GateResult: g, At: time.UnixMilli(rec.Ts)}, true
	}
	return GateRecord{}, false
}

// GateAt returns the newest gate verdict on the branch for target recorded at commit, on
// any ref; see [GateResult.BranchVerdict]. When the commit holds only project-scoped
// passes, it returns the newest of those, so a caller can name what ran instead of
// reporting nothing; such a record answers false to BranchVerdict. Commits compare by
// prefix in both directions, because callers hold abbreviations of different lengths.
// Deferrals are skipped for the reason LatestGate gives.
func GateAt(fold Fold, commit, target string) (GateRecord, bool) {
	if commit == "" {
		return GateRecord{}, false
	}
	var narrow GateRecord
	found := false
	for i := len(fold.Records) - 1; i >= 0; i-- {
		rec := fold.Records[i]
		if rec.Kind != KindGateResult {
			continue
		}
		var g GateResult
		if json.Unmarshal(rec.Payload, &g) != nil {
			continue
		}
		if g.Target != target || g.Outcome == OutcomeDeferred || g.Commit == "" {
			continue
		}
		if !strings.HasPrefix(g.Commit, commit) && !strings.HasPrefix(commit, g.Commit) {
			continue
		}
		at := GateRecord{GateResult: g, At: time.UnixMilli(rec.Ts)}
		if g.BranchVerdict() {
			return at, true
		}
		if !found {
			narrow, found = at, true
		}
	}
	return narrow, found
}

// RecordGate appends one gate result to the store under dir. A result that names no
// [GateScope] is an error, and nothing is written. Like the
// attention producers it mints its own invocation id: the gate verdict is one
// short-lived fact, and joining it to the run's execution journal is not what
// it is read for. Callers on the run path must not fail the build over an
// error here.
func RecordGate(dir string, g GateResult, start InvocationStart) error {
	// Checked before Open: a payload that fails to marshal would leave the start record.
	if _, err := g.Scope.MarshalJSON(); err != nil {
		return err
	}
	w, err := Open(dir, NewID(), start)
	if err != nil {
		return err
	}
	return w.Append(KindGateResult, g)
}
