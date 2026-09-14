package job

import (
	_ "embed"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// ResultSchemaVersion is the version of the result shape this magus accepts. A holder
// sends it, the decoder rejects what it does not know by name, and the schema requires
// it. See types.JobSchemaVersion for the job's half of the same rule.
const ResultSchemaVersion = 2

// ResultSchema is the JSON Schema for [types.JobResult], embedded so a harness can give a
// holder a response format without magus having to render one. Generated from the struct
// itself; see [DeclarationSchema].
//
//go:embed gen/result.schema.json
var ResultSchema string

// Status is kept as the internal package spelling for callers that predate
// types.JobStatus. The public type lives in types because it now crosses the typed Buzz
// boundary from magus\job.wait.
type Status = types.JobStatus

// Verify checks a holder's result against the job it was given: every changed path
// inside the declared lanes and outside the declared deny list, a change set that is
// not empty on a job that writes, descendants the store carries, and an evidence ref
// recording a PASSING run of that job's own check.
//
// IT VERIFIES EVIDENCE, NOT ASSERTIONS, which is the difference between this and reading
// the result: every rule turns on something magus already holds. Whether the work is GOOD,
// and whether the criteria in Goal were met, stay the reading of whoever forked the job.
//
// att is what an output store recorded for the result's ref, filed on the job by `job
// exit` or resolved by the caller; resolving it is never done here, so no rule reads a
// file while the store's lock is held. declared is the rest of the plan, which is what the
// result's descendant ids are checked against.
func Verify(row types.Job, rep types.JobResult, att types.JobAttempt, declared []types.Job) Status {
	return VerifyGates(row, rep, att, nil, declared)
}

// VerifyGates checks the primary check plus every explicit completion gate. It
// derives every verdict from output-store snapshots; GateEvidence has no passed bit
// by design. Callers that only carry the historical primary attempt may keep using
// Verify while Exit/Wait use this complete form.
func VerifyGates(row types.Job, rep types.JobResult, att types.JobAttempt, gateAttempts []types.JobGateAttempt, declared []types.Job) Status {
	v := Status{Job: row.ID, Risks: rep.UnresolvedRisks, Command: rep.Validation.Command}

	if rep.Job != "" && rep.Job != row.ID {
		v.Violations = append(v.Violations, fmt.Sprintf("the result is filed under job %q and this one is %q", rep.Job, row.ID))
	}
	if row.State.Terminal() {
		v.Violations = append(v.Violations, fmt.Sprintf("job %s is already %s, and a job is verified once:"+
			" clear the plan or fork a new job rather than re-verifying a closed one", row.ID, row.State))
	}

	switch {
	case row.ReadOnly && len(rep.ChangedPaths) > 0:
		v.Violations = append(v.Violations, fmt.Sprintf("job %s is read-only and the result claims %d changed path(s)", row.ID, len(rep.ChangedPaths)))
	case !row.ReadOnly && len(rep.ChangedPaths) == 0:
		// A holder that wrote nothing has not done the work or has not reported it, and
		// the two are the same thing to whoever is deciding whether to integrate. A job
		// that genuinely writes nothing is read_only, which is the declaration that says so.
		v.Violations = append(v.Violations, fmt.Sprintf("job %s is not read-only and the result claims no changed paths at all", row.ID))
	default:
		for _, p := range rep.ChangedPaths {
			if _, ok := matching(row.WritePaths, p); !ok {
				v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is outside the job's write paths (%s)", p, strings.Join(row.WritePaths, ", ")))
			}
		}
	}
	for _, p := range rep.ChangedPaths {
		if d, ok := matching(row.DenyPaths, p); ok {
			v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is one the job is denied (%s)", p, d))
		}
	}
	for _, id := range rep.Descendants {
		if !slices.ContainsFunc(declared, func(r types.Job) bool { return r.ID == id }) {
			v.Violations = append(v.Violations, fmt.Sprintf("the result names descendant %q and no job declares it,"+
				" so that branch of the plan is one nobody is tracking", id))
		}
	}

	if row.Check == nil && len(row.CompletionGates) == 0 {
		v.Violations = append(v.Violations, fmt.Sprintf("job %s declares no completion gate, so no recorded run can be bound to it. Declare one with a typed check", row.ID))
	}

	declaredGates := make(map[string]types.CompletionGate, len(row.CompletionGates))
	for _, gate := range row.CompletionGates {
		declaredGates[gate.ID] = gate
	}

	attemptByGate := make(map[string]types.JobAttempt, len(gateAttempts))
	for _, snapshot := range gateAttempts {
		if _, declared := declaredGates[snapshot.GateID]; !declared {
			v.Violations = append(v.Violations, fmt.Sprintf("job %s carries an attempt snapshot for undeclared completion gate %q", row.ID, snapshot.GateID))
			continue
		}
		if _, exists := attemptByGate[snapshot.GateID]; exists {
			v.Violations = append(v.Violations, fmt.Sprintf("job %s carries duplicate attempt snapshots for completion gate %q", row.ID, snapshot.GateID))
			continue
		}
		attemptByGate[snapshot.GateID] = snapshot.Attempt
	}
	evidenceByGate := make(map[string]string, len(rep.GateEvidence))
	for _, evidence := range rep.GateEvidence {
		if _, declared := declaredGates[evidence.GateID]; !declared {
			v.Violations = append(v.Violations, fmt.Sprintf("the result carries evidence for undeclared completion gate %q", evidence.GateID))
			continue
		}
		if _, exists := evidenceByGate[evidence.GateID]; exists {
			v.Violations = append(v.Violations, fmt.Sprintf("the result carries duplicate evidence for completion gate %q", evidence.GateID))
			continue
		}
		evidenceByGate[evidence.GateID] = evidence.OutputRef
	}

	statusByGate := map[string]types.GateStatus{}
	if row.Check != nil {
		statusByGate[types.PrimaryCompletionGateID] = verifyGate(row, types.CompletionGate{ID: types.PrimaryCompletionGateID, Check: *row.Check}, rep.Validation.OutputRef, att)
	}
	for _, gate := range row.CompletionGates {
		statusByGate[gate.ID] = verifyGate(row, gate, evidenceByGate[gate.ID], attemptByGate[gate.ID])
	}
	// A dependency may appear after its consumer in the declaration. Re-evaluate until
	// no status changes so a failed prerequisite propagates through the whole typed
	// graph. Declaration.Validate rejects cycles, so this always reaches a fixed point.
	for changed := true; changed; {
		changed = false
		for _, gate := range row.CompletionGates {
			status := statusByGate[gate.ID]
			for _, dependency := range gate.DependsOn {
				dependencyStatus, ok := statusByGate[dependency]
				if ok && dependencyStatus.Verified {
					continue
				}
				violation := fmt.Sprintf("depends_on completion gate %q has not verified", dependency)
				if !slices.Contains(status.Violations, violation) {
					status.Violations = append(status.Violations, violation)
				}
				if status.Verified {
					status.Verified = false
					changed = true
				}
			}
			statusByGate[gate.ID] = status
		}
	}
	if row.Check != nil && len(row.CompletionGates) > 0 {
		v.Gates = append(v.Gates, statusByGate[types.PrimaryCompletionGateID])
	}
	if row.Check != nil {
		v.Violations = append(v.Violations, prefixedGateViolations(statusByGate[types.PrimaryCompletionGateID])...)
	}
	for _, gate := range row.CompletionGates {
		status := statusByGate[gate.ID]
		v.Gates = append(v.Gates, status)
		v.Violations = append(v.Violations, prefixedGateViolations(status)...)
	}
	for _, dependency := range row.DependsOn {
		i := slices.IndexFunc(declared, func(candidate types.Job) bool { return candidate.ID == dependency })
		switch {
		case i < 0:
			v.Violations = append(v.Violations, fmt.Sprintf("job %s depends_on %q, but no such job is declared", row.ID, dependency))
		case declared[i].State != types.StatePass:
			v.Violations = append(v.Violations, fmt.Sprintf("job %s depends_on %q, which is %s rather than pass", row.ID, dependency, declared[i].State))
		}
	}
	v.Verified = len(v.Violations) == 0
	return v
}

func prefixedGateViolations(status types.GateStatus) []string {
	if status.Verified {
		return nil
	}
	out := make([]string, 0, len(status.Violations))
	for _, violation := range status.Violations {
		out = append(out, fmt.Sprintf("completion gate %q: %s", status.ID, violation))
	}
	return out
}

func verifyGate(row types.Job, gate types.CompletionGate, ref string, attempt types.JobAttempt) types.GateStatus {
	status := types.GateStatus{ID: gate.ID, OutputRef: strings.TrimSpace(ref)}
	if status.OutputRef == "" {
		status.Violations = append(status.Violations, "carries no output_ref, so there is no run to reopen")
		return status
	}
	if !attempt.Found {
		status.Violations = append(status.Violations, fmt.Sprintf("no run is recorded for output ref %q", status.OutputRef))
		return status
	}
	if row.Created > 0 {
		// Declaration time is persisted in seconds while run evidence is in
		// milliseconds. Require the next whole second so same-second historical
		// output never closes a job it predates.
		declaredAt := row.Created*1000 + 999
		switch {
		case attempt.TimestampMs == 0:
			status.Violations = append(status.Violations, "records no timestamp, so Magus cannot prove the evidence was captured after this job was declared")
		case attempt.TimestampMs < declaredAt:
			status.Violations = append(status.Violations, fmt.Sprintf("was captured before job declaration (%d < %d), so historical output cannot close this job", attempt.TimestampMs, declaredAt))
		}
	}
	if !bindsTo(gate.Check, attempt) {
		status.Violations = append(status.Violations, fmt.Sprintf("output ref %q records `%s` and this gate's check is `%s`, so the evidence is from a different run", status.OutputRef, attempt, gate.Check))
	}
	if attempt.Failed {
		status.Violations = append(status.Violations, fmt.Sprintf("the run behind output ref %q failed, so its check did not pass", status.OutputRef))
	}
	status.Verified = len(status.Violations) == 0
	return status
}

// bindsTo reports whether a recorded run is a run of this check.
//
// The spell filter is compared only when BOTH sides name one. A row that names
// `go::go-test` and an attempt the store recorded under a bare target are the same run
// selected two ways, and rejecting that pair would make the rule fire on spelling rather
// than on identity. Target and project are compared always: those are what a run IS.
//
// The args past `--` are NOT compared: the output store records a run by spell, target and
// project and holds no argv, so a rule keyed on them would reject every ref there is.
func bindsTo(c types.LeaseCheck, a types.JobAttempt) bool {
	spell, target, _ := strings.Cut(c.Target, "::")
	if target == "" {
		spell, target = "", spell
	}
	// A charm is a way of running the target, not another target: `generate:rw` and
	// `generate` are one identity to the output store.
	target, _, _ = strings.Cut(target, ":")
	if target != a.Target || path.Clean(c.Project) != path.Clean(a.Project) {
		return false
	}
	return spell == "" || a.Spell == "" || spell == a.Spell
}

// matching is the first declaration in decls that covers p. The declaration comes back
// rather than a bool because a rejection has to name which one fired.
func matching(decls []string, p string) (string, bool) {
	for _, d := range decls {
		if covers(d, p) {
			return d, true
		}
	}
	return "", false
}

// covers decides whether one declared path covers a written one.
//
// Two rules, because a declaration means two different things. A declaration holding glob
// metacharacters is matched AS a glob, through the matcher every other consumer of a
// declared glob uses. A literal one names a file or a directory, and a directory covers
// what is under it: `internal/ledger` has to cover `internal/ledger/store.go` or every
// ordinary lease reads as a boundary violation.
//
// The glob arm deliberately does NOT fall back to its literal prefix. That fallback is
// right for types.LiteralPrefix's caller, which over-reports collisions between two
// declarations on purpose; here over-reporting means silently ACCEPTING a write the
// declaration excludes, and the two directions of error are not symmetrical.
func covers(declared, p string) bool {
	// A blank declaration claims nothing, on types.PathsIntersect's rule: it cleans to
	// ".", which the whole-tree arm below would read as a claim on everything.
	if strings.TrimSpace(declared) == "" || strings.TrimSpace(p) == "" {
		return false
	}
	declared, p = path.Clean(strings.TrimSpace(declared)), path.Clean(strings.TrimSpace(p))
	if p == "." {
		return false
	}
	if declared == "." {
		// The repository root covers every path under it. Refusing "." at declaration
		// instead would leave a single-lease plan over the whole tree undeclarable, and
		// the row would read as owning nothing rather than as owning everything.
		return true
	}
	if types.LiteralPrefix(declared) != declared {
		return types.MatchesAnyGlob([]string{declared}, p)
	}
	return p == declared || strings.HasPrefix(p, declared+"/")
}
