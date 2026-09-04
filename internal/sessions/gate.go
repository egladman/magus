package sessions

import (
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
// and daemon restarts.
const KindGateResult = "gate_result"

// OutcomeDeferred marks a gate invocation that was REFUSED as redundant under
// load (MGS3010, exit 75) rather than run. It is a record of the deferral
// decision, kept so the decision is interrogable after the fact; it is never a
// verdict on the inputs, which is why [LatestGate] skips it.
const OutcomeDeferred = "deferred"

// GateResult is the payload of a [KindGateResult] record.
type GateResult struct {
	// Target is the gate target the run anchored on (normally "ci").
	Target string `json:"target"`
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
	// Inv is the invocation id of the run that produced this verdict, joining
	// it to the execution journal (`magus query <inv>`). Empty on a deferral:
	// nothing ran, so there is no journal to join.
	Inv string `json:"inv,omitempty"`
	// DeferredTo is set on an OutcomeDeferred record: the commit of the green
	// gate the refusal pointed at.
	DeferredTo string `json:"deferred_to,omitempty"`
}

// GateRecord is one gate result with the envelope's timestamp resolved.
type GateRecord struct {
	GateResult
	At time.Time
}

// LatestGate returns the newest gate VERDICT for (ref, target): a pass or a
// fail. The newest verdict wins on purpose - a fail recorded after a pass
// means the branch is red, and a redundancy check must see that rather than
// the stale green behind it. Deferral records are skipped: a deferral is not a
// verdict on the inputs, and letting one shadow the green gate it points at
// would make the check inert after its own first refusal.
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
		if g.Ref != ref || g.Target != target || g.Outcome == OutcomeDeferred {
			continue
		}
		return GateRecord{GateResult: g, At: time.UnixMilli(rec.Ts)}, true
	}
	return GateRecord{}, false
}

// RecordGate appends one gate result to the store under dir. Like the
// attention producers it mints its own session id: the gate verdict is one
// short-lived fact, and joining it to the run's execution journal is not what
// it is read for. Callers on the run path must not fail the build over an
// error here.
func RecordGate(dir string, g GateResult, start SessionStart) error {
	w, err := Open(dir, NewID(), start)
	if err != nil {
		return err
	}
	return w.Append(KindGateResult, g)
}
