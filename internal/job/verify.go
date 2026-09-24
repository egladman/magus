package job

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
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

// Observer answers what magus itself saw of a job, for the gates that are graded against
// the tree rather than against the output store. Supplied by the caller for the same
// reason AttemptResolver is: the work is IO, and the store's lock must not be held across
// it. A nil Observer means nobody looked, which is what leaves Observed zero and fails any
// gate that needed the answer.
type Observer func(ctx context.Context, row types.Job) (Observed, error)

// Observed is what magus itself saw of the job, as opposed to what the holder reported.
//
// Every observation carries a Known flag, so its absence is stated rather than assumed: a
// gate that needed an observation nobody could make fails naming it, instead of passing
// on an empty value.
type Observed struct {
	// Changed are the paths that actually differ from the job's checkpoint, as the
	// CALLER computed them. The caller resolves it for the same reason it resolves an
	// attempt: nothing here reads a VCS, a file or a graph while the store's lock is held.
	Changed []string
	// ChangedKnown says the diff was computed, so an empty Changed means "this job
	// changed nothing" rather than "nobody looked". Without it the two are one value
	// and a gate would be satisfied by a failed observation.
	ChangedKnown bool
	// ChangedFrom is the checkpoint the diff was taken against, named in violations so
	// a reader can re-run the comparison.
	ChangedFrom string
	// Present are the paths that exist in the tree right now, and PresentKnown says
	// somebody looked. Separate from Changed because they answer different questions: a
	// file can exist and be untouched, or be deleted and therefore changed.
	Present      []string
	PresentKnown bool
	// Symbols is what the knowledge graph says about each symbol a gate named, keyed by
	// the name as declared. SymbolsKnown says the graph was readable; a name the graph
	// could answer for but did not find is present in the map with Defined false, which
	// is a real answer and not a missing one.
	Symbols      map[string]SymbolFact
	SymbolsKnown bool
	// Regions are the declarations the lines of Changed land in, read over the same
	// revision and only for those paths. RegionsKnown says the VCS answered, so an empty
	// Regions means "no line of the diff" rather than "nobody looked"; a backend that
	// declines the capability leaves it false. RegionsReason says why, for a reader who
	// would otherwise take the silence for an empty footprint.
	//
	// No gate reads them: the footprint is reported, never graded.
	Regions       []types.RegionChange
	RegionsKnown  bool
	RegionsReason string
}

// SymbolFact is what the graph knows about one symbol a gate named.
type SymbolFact struct {
	// DefinedIn are the files that define the symbol, so a `symbol` + `changed` gate can
	// ask whether the diff touched one of them. Empty means the graph resolved nothing.
	DefinedIn []string
	// ReferenceCount is how many places reference it, which is what `unreferenced` grades.
	ReferenceCount int
	// SameNameDefinitions is one defining file per graph symbol a BARE name matched, filled only
	// when it matched more than one: the graph grades the top-ranked match silently, so a
	// gate on an ambiguous name would grade whichever symbol happened to rank first.
	SameNameDefinitions []string
}

// Defined reports whether the symbol resolves at all. Derived rather than stored: a bool
// beside DefinedIn is a second copy of len(DefinedIn) > 0, and the two can disagree.
func (f SymbolFact) Defined() bool { return len(f.DefinedIn) > 0 }

// missing names the observation this kind and condition need and did not get, or "" when
// magus looked. Absence of evidence never satisfies a gate: a guard that cannot ask must
// stand down, and a gate that cannot verify must refuse, or the cheapest way to pass one
// is to break the observation.
func (o Observed) missing(kind types.GateKind, expect types.GateExpect) string {
	switch {
	case kind == types.GateKindPaths && expect == types.ExpectChanged && !o.ChangedKnown:
		return "magus could not read what this job changed, so there is no diff to hold the declared paths against"
	case kind == types.GateKindPaths && expect != types.ExpectChanged && !o.PresentKnown:
		return "magus could not read the tree, so it cannot say whether the declared paths are there"
	case kind == types.GateKindSymbol && expect == types.ExpectChanged && !o.ChangedKnown:
		return "magus could not read what this job changed, so it cannot say whether the declared symbols moved"
	case kind == types.GateKindSymbol && !o.SymbolsKnown:
		return "magus could not read the symbol graph, so it cannot say what the declared symbols are. `" + hint.GraphBuild.String() + "` builds it"
	}
	return ""
}

// holds grades ONE declared subject, and returns the reason when it does not. The reason
// is per subject rather than per gate because a partial result is the common case: the
// migration lands and the test beside it does not, and a verdict naming only the gate
// makes the reader re-derive which half is missing.
func (o Observed) holds(kind types.GateKind, expect types.GateExpect, declared string) (bool, string) {
	from := o.ChangedFrom
	if from == "" {
		from = "the job's checkpoint"
	}
	switch kind {
	case types.GateKindPaths:
		switch expect {
		case types.ExpectChanged:
			if o.covering(declared, o.Changed) {
				return true, ""
			}
			return false, fmt.Sprintf("nothing matching %q changed since %s, so this gate is unmet", declared, from)
		case types.ExpectPresent:
			if o.covering(declared, o.Present) {
				return true, ""
			}
			return false, fmt.Sprintf("nothing matching %q is in the tree, so this gate is unmet", declared)
		default:
			if !o.covering(declared, o.Present) {
				return true, ""
			}
			return false, fmt.Sprintf("%q is still in the tree, and this gate expects it gone", declared)
		}
	default:
		return o.symbolHolds(expect, declared, from)
	}
}

func (o Observed) symbolHolds(expect types.GateExpect, declared, from string) (bool, string) {
	fact := o.Symbols[declared]
	switch expect {
	case types.ExpectPresent:
		if fact.Defined() {
			return true, ""
		}
		return false, fmt.Sprintf("%q is defined nowhere the graph can see, so this gate is unmet", declared)
	case types.ExpectAbsent:
		if !fact.Defined() {
			return true, ""
		}
		return false, fmt.Sprintf("%q is still defined in %s, and this gate expects it gone", declared, strings.Join(fact.DefinedIn, ", "))
	case types.ExpectUnreferenced:
		if fact.ReferenceCount == 0 {
			return true, ""
		}
		return false, fmt.Sprintf("%d place(s) still reference %q, and this gate expects none", fact.ReferenceCount, declared)
	default:
		if !fact.Defined() {
			return false, fmt.Sprintf("%q is defined nowhere the graph can see, so nothing of it could have changed", declared)
		}
		// covers(), not a bare equality: a definition file is compared with the same
		// matcher a paths gate uses, so one question is not answered two ways.
		for _, file := range fact.DefinedIn {
			if o.covering(file, o.Changed) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%q is defined in %s, and none of that changed since %s", declared, strings.Join(fact.DefinedIn, ", "), from)
	}
}

// covering reports whether any observed path is covered by the declared glob.
func (o Observed) covering(declared string, paths []string) bool {
	return slices.ContainsFunc(paths, func(p string) bool { return covers(declared, p) })
}

// VerifyGates checks a holder's result against the job it was given: every changed path
// inside the declared write paths, outside the deny list and in the diff magus observed, no
// descendant still live, descendants the store carries, and a recorded passing run behind
// the primary check and every explicit completion gate.
//
// IT VERIFIES EVIDENCE, NOT ASSERTIONS: every rule turns on something magus already holds,
// and GateEvidence has no passed bit by design. Whether the work is GOOD, and whether the
// prose Criteria were met, stay the reading of whoever forked the job.
//
// att and gateAttempts are what an output store recorded, and seen is what the caller
// observed of the tree; nothing here resolves either, so no IO runs under the store's lock.
func VerifyGates(row types.Job, rep types.JobResult, att types.JobAttempt, gateAttempts []types.JobGateAttempt, declared []types.Job, seen Observed) Status {
	v := Status{
		Job: row.ID, Risks: rep.UnresolvedRisks, Command: rep.Validation.Command,
		Footprint: seen.Regions, FootprintKnown: seen.RegionsKnown, FootprintReason: seen.RegionsReason,
	}

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
	if !row.ReadOnly {
		v.Violations = append(v.Violations, diffViolations(row, rep, seen)...)
	}
	for _, p := range rep.ChangedPaths {
		if d, ok := matching(row.DenyPaths, p); ok {
			v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is one the job is denied (%s)", p, d))
		}
	}
	var live []string
	for _, d := range types.JobDescendants(declared, row.ID) {
		if d.State.Live() {
			live = append(live, fmt.Sprintf("%s (%s)", d.ID, d.State))
		}
	}
	if len(live) > 0 {
		v.Violations = append(v.Violations, fmt.Sprintf("job %s still has live descendants, %s, and a job is not done while work it"+
			" handed out is: wait on them, or end them with `%s`", row.ID, strings.Join(live, ", "), hint.JobExit.With("<job>")))
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

	// ONE list, from EffectiveCompletionGates, so the primary check is graded by the same
	// loop as every other gate and lands in Gates like one. Grading it separately meant a
	// job declaring only `--check` reported no gates at all to anything that read Gates,
	// which is how `describe job --gates` came to exit 0 on a job with a gate.
	gates := row.EffectiveCompletionGates()
	evidenceByGate[types.PrimaryCompletionGateID] = rep.Validation.OutputRef
	attemptByGate[types.PrimaryCompletionGateID] = att

	statusByGate := map[string]types.GateStatus{}
	for _, gate := range gates {
		statusByGate[gate.ID] = verifyGate(row, gate, evidenceByGate[gate.ID], attemptByGate[gate.ID], seen)
	}
	// A dependency may appear after its consumer in the declaration. Re-evaluate until
	// no status changes so a failed prerequisite propagates through the whole typed
	// graph. Declaration.Validate rejects cycles, so this always reaches a fixed point.
	for changed := true; changed; {
		changed = false
		for _, gate := range gates {
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
	for _, gate := range gates {
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

// diffViolations holds a writing job's claim to what magus observed since its checkpoint:
// every claimed path is in the diff, and something in the diff is inside the write paths.
// A diff nobody could read fails, since a claim checked against nothing is an attestation.
func diffViolations(row types.Job, rep types.JobResult, seen Observed) []string {
	if !seen.ChangedKnown {
		return []string{fmt.Sprintf("magus could not read what job %s changed since its checkpoint, so its changed_paths"+
			" cannot be checked against the tree; declare the job with a checkpoint (`%s`)", row.ID, hint.VCSCheckpoint.With("-o", "name"))}
	}
	from := seen.ChangedFrom
	if from == "" {
		from = "the job's checkpoint"
	}
	var out []string
	for _, p := range rep.ChangedPaths {
		if !slices.ContainsFunc(seen.Changed, func(c string) bool { return covers(p, c) }) {
			out = append(out, fmt.Sprintf("the result claims %q changed, and the diff since %s does not show it", p, from))
		}
	}
	if !slices.ContainsFunc(seen.Changed, func(c string) bool { _, ok := matching(row.WritePaths, c); return ok }) {
		out = append(out, fmt.Sprintf("nothing in the diff since %s is inside its write paths (%s)", from, strings.Join(row.WritePaths, ", ")))
	}
	return out
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

func verifyGate(row types.Job, gate types.CompletionGate, ref string, attempt types.JobAttempt, seen Observed) types.GateStatus {
	if kind := gate.Kind; kind != types.GateKindCheck {
		return verifySubjectGate(gate, seen)
	}
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
// A CHARM is part of that identity, so `generate` and `generate:rw` are two checks, not
// one spelled twice. They are different runs: charmless `generate` gates drift and fails
// on it, while `generate:rw` writes the output and cannot fail. Treating them as one
// identity made the drift gate satisfiable by the very run that produces the drift. The
// store records what was invoked (cache.reproTarget renders `name:charm`), so a check
// that means the written form says so in its target, `generate:rw`, and one that names no
// charm means the charmless run.
//
// The args past `--` are NOT compared: the output store records a run by spell, target and
// project and holds no argv, so a rule keyed on them would reject every ref there is.
func bindsTo(c types.LeaseCheck, a types.JobAttempt) bool {
	spell, target, _ := strings.Cut(c.Target, "::")
	if target == "" {
		spell, target = "", spell
	}
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

// verifyPathsGate grades a gate against the DIFF, which is the half a holder cannot
// assert its way past.
//
// Every declared glob must be matched by something that actually changed, and a glob
// matched by nothing is named individually: told only that the gate failed, a reader has
// to re-derive which half of the gate is unmet, and a partial result is the common case
// (the migration landed, the test beside it did not).
//
// The diff NOT being available is a failure and never a pass. Absence of evidence is the
// one thing a gate must not read as evidence, and a caller that could not compute the diff
// says so through ChangedKnown rather than by handing back an empty list that looks like a
// job which changed nothing.
func verifySubjectGate(gate types.CompletionGate, seen Observed) types.GateStatus {
	status := types.GateStatus{ID: gate.ID}
	kind, expect := gate.Kind, gate.Expect

	// The observation this pair needs, and whether magus actually made it. Asked once, up
	// front, because every arm below has the same answer for the same reason: a subject
	// nobody looked at is not a subject that satisfies anything.
	if why := seen.missing(kind, expect); why != "" {
		status.Violations = append(status.Violations, why)
		return status
	}
	for _, declared := range gate.Subject() {
		if declared = strings.TrimSpace(declared); declared == "" {
			continue
		}
		if held, why := seen.holds(kind, expect, declared); !held {
			status.Violations = append(status.Violations, why)
		}
	}
	status.Verified = len(status.Violations) == 0
	return status
}
