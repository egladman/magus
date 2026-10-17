package job

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

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
	// ChangedRevision is the bare revision Changed was read from, for a caller that reads
	// more of the same diff; ChangedFrom may carry prose around it.
	ChangedRevision string
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
	// Signatures is what the graph says about each symbol a signature goal named, read
	// against the goal's declared signature and keyed by the name. Known under SymbolsKnown,
	// since both come from one pass over the graph.
	Signatures map[string]SymbolFact
	// conflicts are the names two goals declared different signatures for, which one
	// graph read cannot answer for both. Only inherited goals reach here that way: a row's
	// own are refused at declaration.
	conflicts map[string]bool
	// Regions are the declarations the lines of Changed land in, read over the same
	// revision and only for those paths. RegionsKnown says the VCS answered, so an empty
	// Regions means "no line of the diff" rather than "nobody looked"; a backend that
	// declines the capability leaves it false. RegionsReason says why, for a reader who
	// would otherwise take the silence for an empty footprint.
	//
	// Graded only against a job's declaration claims; see claimViolations.
	Regions       []types.RegionChange
	RegionsKnown  bool
	RegionsReason string
	// GreenGate is the newest green ci gate recorded for this checkout's branch, with
	// the tier of the change since it. The zero value means none was found or assessed.
	GreenGate GreenGate
	// Stale holds, by [attemptKey], each run behind the job's evidence that read a changed path
	// other than it reads now, with why: the job wrote after it, so its pass graded other
	// files. A run absent from it read the tree as it stands, or was never compared.
	Stale map[string]string
	// DefaultCharms are the default_charms in effect in the job's checkout, which `magus
	// run` stacks under the charms a target names. A check is resolved against them before
	// it is compared with a recorded run; nil resolves every check to the charms it names.
	DefaultCharms []string
	// AddedExports are the exported symbols the diff added, and LeftUnreferenced the
	// symbols that lost their last use in it and are still defined. SymbolDeltaKnown says
	// both were read. They feed report fields and never decide a verdict.
	AddedExports     []string
	LeftUnreferenced []string
	SymbolDeltaKnown bool
	// anyLease accepts a run recorded under any lease. Integration grades runs in the
	// integrator's own tree, where its validators run checks under their own leases.
	anyLease bool
}

// GreenGate is a passing ci gate and what changed since it.
type GreenGate struct {
	// Commit is the revision the gate ran at; empty means there is no gate.
	Commit string
	// Projects are the projects the gate covered.
	Projects []string
	// Tier is the tier of the change from Commit to the tree now.
	Tier types.RiskTier
}

// covers reports whether the gate vouches for a check that runs ci: the gate covered
// the check's project, and the change since it tiers trivial, so no step of ci can
// observe it. The gate may predate the job; the tier, not the timestamp, is what rules
// out a stale verdict.
func (g GreenGate) covers(c types.LeaseCheck) bool {
	project := path.Clean(c.Project)
	if c.Project == "" {
		project = "."
	}
	return g.Commit != "" && c.Target == types.TargetCI && g.Tier == types.RiskTrivial && slices.Contains(g.Projects, project)
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
	// Signature is the declaration as the index renders it, empty when it holds none.
	// SignatureMatch says a declared signature the reader was asked about matched it, and is
	// false when none was asked about.
	Signature      string
	SignatureMatch bool
}

// Defined reports whether the symbol resolves at all. Derived rather than stored: a bool
// beside DefinedIn is a second copy of len(DefinedIn) > 0, and the two can disagree.
func (f SymbolFact) Defined() bool { return len(f.DefinedIn) > 0 }

// missing names the observation this kind and condition need and did not get, or "" when
// magus looked. Absence of evidence never satisfies a gate: a guard that cannot ask must
// stand down, and a gate that cannot verify must refuse, or the cheapest way to pass one
// is to break the observation.
func (o Observed) missing(kind types.GoalKind, expect types.GoalExpect) string {
	switch {
	case kind == types.GoalKindPaths && expect == types.ExpectChanged && !o.ChangedKnown:
		return "magus could not read what this job changed, so there is no diff to hold the declared paths against"
	case kind == types.GoalKindPaths && expect != types.ExpectChanged && !o.PresentKnown:
		return "magus could not read the tree, so it cannot say whether the declared paths are there"
	case kind == types.GoalKindSymbol && expect == types.ExpectChanged && !o.ChangedKnown:
		return "magus could not read what this job changed, so it cannot say whether the declared symbols moved"
	case kind == types.GoalKindSymbol && !o.SymbolsKnown:
		return "magus could not read the symbol graph, so it cannot say what the declared symbols are. `" + hint.GraphBuild.String() + "` builds it"
	}
	return ""
}

// holds grades ONE declared subject, and returns the reason when it does not. The reason
// is per subject rather than per gate because a partial result is the common case: the
// migration lands and the test beside it does not, and a verdict naming only the gate
// makes the reader re-derive which half is missing.
func (o Observed) holds(kind types.GoalKind, expect types.GoalExpect, declared, signature string) (bool, string) {
	from := o.ChangedFrom
	if from == "" {
		from = "the job's checkpoint"
	}
	switch kind {
	case types.GoalKindPaths:
		switch expect {
		case types.ExpectChanged:
			if o.covering(declared, o.Changed) {
				return true, ""
			}
			return false, fmt.Sprintf("nothing matching %q changed since %s, so this goal is unmet", declared, from)
		case types.ExpectPresent:
			if o.covering(declared, o.Present) {
				return true, ""
			}
			return false, fmt.Sprintf("nothing matching %q is in the tree, so this goal is unmet", declared)
		default:
			if !o.covering(declared, o.Present) {
				return true, ""
			}
			return false, fmt.Sprintf("%q is still in the tree, and this goal expects it gone", declared)
		}
	default:
		if expect == types.ExpectSignature {
			return o.signatureHolds(declared, signature)
		}
		return o.symbolHolds(expect, declared, from)
	}
}

// signatureHolds grades a signature goal. Every answer short of a compared match is unmet:
// an index with no signature for the symbol has nothing to compare, not a match.
func (o Observed) signatureHolds(declared, signature string) (bool, string) {
	if o.conflicts[declared] {
		return false, fmt.Sprintf("this job and an ancestor declare different signatures for %q, and a symbol has one", declared)
	}
	fact, asked := o.Signatures[declared]
	switch {
	case !asked:
		return false, fmt.Sprintf("magus did not ask the symbol graph about the signature of %q, so this goal is unmet", declared)
	case !fact.Defined():
		return false, fmt.Sprintf("%q is defined nowhere the graph can see, so it has no signature to hold", declared)
	case fact.Signature == "":
		return false, fmt.Sprintf("the symbol index holds no signature for %q, so nothing could be compared with `%s`", declared, signature)
	case !fact.SignatureMatch:
		return false, fmt.Sprintf("%q is declared `%s`, and this goal expects `%s`", declared, fact.Signature, signature)
	}
	return true, ""
}

func (o Observed) symbolHolds(expect types.GoalExpect, declared, from string) (bool, string) {
	fact, asked := o.Symbols[declared]
	if !asked {
		// A name nobody asked the graph about reads as undefined and unreferenced, which
		// would satisfy `absent` and `unreferenced` on no evidence.
		return false, fmt.Sprintf("magus did not ask the symbol graph about %q, so this goal is unmet", declared)
	}
	switch expect {
	case types.ExpectPresent:
		if fact.Defined() {
			return true, ""
		}
		return false, fmt.Sprintf("%q is defined nowhere the graph can see, so this goal is unmet", declared)
	case types.ExpectAbsent:
		if !fact.Defined() {
			return true, ""
		}
		return false, fmt.Sprintf("%q is still defined in %s, and this goal expects it gone", declared, strings.Join(fact.DefinedIn, ", "))
	case types.ExpectUnreferenced:
		if !fact.Defined() {
			// The graph counts references into a definition, so with none left a caller
			// that still names it is invisible here, and zero is not evidence.
			return false, fmt.Sprintf("%q is defined nowhere the graph can see, so it cannot count what still names it;"+
				" hold a removal to `absent` and a check that builds its callers", declared)
		}
		if fact.ReferenceCount == 0 {
			return true, ""
		}
		return false, fmt.Sprintf("%d place(s) still reference %q, and this goal expects none", fact.ReferenceCount, declared)
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
// the primary check and every goal.
//
// IT VERIFIES EVIDENCE, NOT ASSERTIONS: every rule turns on something magus already holds,
// and GateEvidence has no passed bit by design. Whether the work is GOOD, and whether the
// prose Criteria were met, stay the reading of whoever forked the job.
//
// att and gateAttempts are what an output store recorded, and seen is what the caller
// observed of the tree; nothing here resolves either, so no IO runs under the store's lock.
func VerifyGates(row types.Job, rep types.JobResult, att types.JobAttempt, gateAttempts []types.JobGateAttempt, declared []types.Job, seen Observed) Status {
	v := Status{
		Job: row.ID, UnresolvedRisks: rep.UnresolvedRisks, Command: rep.Validation.Command,
		Footprint: seen.Regions, FootprintKnown: seen.RegionsKnown, FootprintReason: seen.RegionsReason,
		FootprintUnclaimed: unclaimedFootprint(row, seen.Regions),
		SymbolDeltaKnown:   seen.SymbolDeltaKnown,
		LeftUnreferenced:   sortedCopy(seen.LeftUnreferenced),
		UndeclaredExports:  undeclaredExports(row, seen.AddedExports),
	}
	v.LinesAdded, v.LinesDeleted = netLines(seen.Regions)

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
			if _, ok := matching(row.WritePaths, p); ok {
				continue
			}
			// magus rewrites such a file beside a job's change (the merge driver writes
			// .gitattributes from the output globs), so no job declares it.
			if types.IsMagusMaintained(p) {
				v.Notes = append(v.Notes, fmt.Sprintf("changed path %q is outside the job's write paths and is one magus maintains itself,"+
					" regenerated beside the change, so it was not graded against them", p))
				continue
			}
			v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is %s (%s)", p, outsideWritePaths, strings.Join(row.WritePaths, ", ")))
		}
	}
	if !row.ReadOnly {
		v.Violations = append(v.Violations, diffViolations(row, rep, seen)...)
		v.Violations = append(v.Violations, claimViolations(row, seen, v.FootprintUnclaimed)...)
	}
	v.Violations = append(v.Violations, denyViolations(row, rep, seen)...)
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

	if row.Check == nil && len(row.Goals) == 0 {
		v.Violations = append(v.Violations, fmt.Sprintf("job %s declares no check and no goal, so nothing magus can grade says it is done."+
			" Declare one in the job record's check or goals; a fact no target checks can be shown by a script run with"+
			" `magus buzz --record`, whose printed ref is evidence anyone can reopen", row.ID))
	}

	declaredGates := make(map[string]types.Goal, len(row.Goals))
	for _, gate := range row.Goals {
		declaredGates[gate.ID] = gate
	}

	attemptByGate := make(map[string]types.JobAttempt, len(gateAttempts))
	for _, snapshot := range gateAttempts {
		if _, declared := declaredGates[snapshot.GateID]; !declared {
			v.Violations = append(v.Violations, fmt.Sprintf("job %s carries an attempt snapshot for undeclared goal %q", row.ID, snapshot.GateID))
			continue
		}
		if _, exists := attemptByGate[snapshot.GateID]; exists {
			v.Violations = append(v.Violations, fmt.Sprintf("job %s carries duplicate attempt snapshots for goal %q", row.ID, snapshot.GateID))
			continue
		}
		attemptByGate[snapshot.GateID] = snapshot.Attempt
	}
	evidenceByGate := make(map[string]string, len(rep.GateEvidence))
	for _, evidence := range rep.GateEvidence {
		if _, declared := declaredGates[evidence.GateID]; !declared {
			v.Violations = append(v.Violations, fmt.Sprintf("the result carries evidence for undeclared goal %q", evidence.GateID))
			continue
		}
		if _, exists := evidenceByGate[evidence.GateID]; exists {
			v.Violations = append(v.Violations, fmt.Sprintf("the result carries duplicate evidence for goal %q", evidence.GateID))
			continue
		}
		evidenceByGate[evidence.GateID] = evidence.OutputRef
	}

	// ONE list, from EffectiveGoals, so the primary check is graded by the same loop as
	// every other goal and lands in Gates like one. Grading it separately meant a job
	// declaring only `--check` reported no goals at all to anything that read Gates,
	// which is how `describe job` came to show an empty goal list on a job with a check.
	gates := row.EffectiveGoals()
	evidenceByGate[types.PrimaryGoalID] = rep.Validation.OutputRef
	attemptByGate[types.PrimaryGoalID] = att

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
				violation := fmt.Sprintf("depends_on goal %q has not verified", dependency)
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

// netLines sums the lines each side of the footprint covers.
func netLines(regions []types.RegionChange) (added, deleted int) {
	for _, r := range regions {
		n := r.Lines[1] - r.Lines[0] + 1
		if n <= 0 {
			continue
		}
		if r.Side == types.RegionOld {
			deleted += n
		} else {
			added += n
		}
	}
	return added, deleted
}

// undeclaredExports are the added exports no symbol goal of row names, sorted. row already
// carries its ancestors' symbol goals (see inheritGoals), so an export the root declared
// two levels up is declared here. A goal names an export by its qualified form
// (`Goal#Signature`) or by its last segment.
func undeclaredExports(row types.Job, added []string) []string {
	named := map[string]bool{}
	for _, goal := range row.Goals {
		if goal.Resolve().Kind != types.GoalKindSymbol {
			continue
		}
		for _, name := range goal.Symbols {
			named[strings.TrimSpace(name)] = true
		}
	}
	var out []string
	for _, export := range added {
		last := export[strings.LastIndexAny(export, "#.")+1:]
		if !named[export] && !named[last] {
			out = append(out, export)
		}
	}
	return sortedCopy(out)
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
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
		out = append(out, fmt.Sprintf("goal %q: %s", status.ID, violation))
	}
	return out
}

func verifyGate(row types.Job, gate types.Goal, ref string, attempt types.JobAttempt, seen Observed) types.GateStatus {
	if kind := gate.Kind; kind != types.GoalKindCheck {
		return verifySubjectGate(gate, seen)
	}
	status := types.GateStatus{ID: gate.ID, OutputRef: strings.TrimSpace(ref)}
	if seen.GreenGate.covers(gate.Check) {
		status.Verified = true
		return status
	}
	if status.OutputRef == "" {
		status.Violations = append(status.Violations, "carries no output_ref, so there is no run to reopen")
		return status
	}
	if !attempt.Found {
		status.Violations = append(status.Violations, fmt.Sprintf("no run is recorded for output ref %q", status.OutputRef))
		return status
	}
	if row.Created > 0 {
		// Compared in whole seconds, the resolution the declaration is stamped in: a run in
		// the declaring second cannot be ordered against it either way, and a check run
		// straight after its fork is exactly that run.
		captured := attempt.TimestampMs / 1000
		switch {
		case attempt.TimestampMs == 0:
			status.Violations = append(status.Violations, "records no timestamp, so Magus cannot prove the evidence was captured after this job was declared")
		case captured < row.Created:
			status.Violations = append(status.Violations, fmt.Sprintf("was captured before job declaration (second %d < %d), so historical output cannot close this job", captured, row.Created))
		}
	}
	if why, ok := seen.Stale[attemptKey(attempt)]; ok {
		status.Violations = append(status.Violations, fmt.Sprintf("the run behind output ref %q predates the job's last write: it proves the work only if %s", status.OutputRef, why))
	}
	// An unleased run is accepted: the orchestrator runs a check under no lease.
	if !seen.anyLease && attempt.Lease != "" && attempt.Lease != row.ID {
		status.Violations = append(status.Violations, fmt.Sprintf("the run behind output ref %q acted under lease %q, not this job's lease %q, so it is another job's evidence",
			status.OutputRef, attempt.Lease, row.ID))
	}
	if !ranCheck(gate.Check, attempt, seen.DefaultCharms) {
		status.Violations = append(status.Violations, fmt.Sprintf("output ref %q records `%s` and this goal's check runs `%s`, so the evidence is from a different run",
			status.OutputRef, attempt, resolveCheck(gate.Check, seen.DefaultCharms)))
	}
	if attempt.Failed {
		status.Violations = append(status.Violations, fmt.Sprintf("the run behind output ref %q failed, so its check did not pass", status.OutputRef))
	}
	status.Verified = len(status.Violations) == 0
	return status
}

// ranCheck reports whether a recorded run is a run of this check.
//
// The spell filter is compared only when BOTH sides name one. A row that names
// `go::go-test` and an attempt the store recorded under a bare target are the same run
// selected two ways, and rejecting that pair would make the rule fire on spelling rather
// than on identity. Target and project are compared always: those are what a run IS.
//
// A CHARM SET is part of that identity, so `generate` and `generate:rw` are two runs, not
// one spelled twice: charmless `generate` gates drift and fails on it, while
// `generate:rw` writes the output and cannot fail. The store records the charms a run
// actually executed under (cache.reproTarget renders `name:charm`), which in a workspace
// setting default_charms includes the defaults. So the check is resolved the way `magus
// run` resolves it, defaults stacked under its own charms, before the sets are compared:
// otherwise `magus run test .` records `test:rw` and can never satisfy its own check.
//
// The args past `--` are compared when the attempt records them, so a run narrowed with
// `-- -run TestOne` does not stand in for the whole check. Nil args are not compared: the
// run forwarded none, or its record predates the field, and either way it narrowed nothing
// it can show.
func ranCheck(c types.LeaseCheck, a types.JobAttempt, defaults []string) bool {
	if a.ExtraArgs != nil && !slices.Equal(a.ExtraArgs, c.Args) {
		return false
	}
	if c.Script != "" {
		// `magus buzz --record` stores its run under `buzz <script>` in the workspace
		// project, with no charms.
		script, ran := strings.CutPrefix(a.Target, "buzz ")
		return ran && path.Clean(script) == path.Clean(c.Script) && path.Clean(a.Project) == "."
	}
	spell, target, _ := strings.Cut(resolveCheck(c, defaults).Target, "::")
	if target == "" {
		spell, target = "", spell
	}
	if !sameRun(target, a.Target) || path.Clean(c.Project) != path.Clean(a.Project) {
		return false
	}
	return spell == "" || a.Spell == "" || spell == a.Spell
}

// resolveCheck is c with the charm set `magus run` would execute it under: defaults first
// unless the check opts out of them, the check's own charms stacked on top, duplicates
// dropped (cmd/magus withDefaultCharms), and the write-granting charms removed from ci,
// which RunCI never runs with (magus.CharmsForCI).
func resolveCheck(c types.LeaseCheck, defaults []string) types.LeaseCheck {
	if c.NoDefaultCharms {
		defaults = nil
	}
	spell, target, filtered := strings.Cut(c.Target, "::")
	if !filtered {
		spell, target = "", spell
	}
	name, named := splitCharms(target)
	charms := make([]string, 0, len(defaults)+len(named))
	for _, charm := range append(slices.Clone(defaults), named...) {
		if charm = types.NormalizeCharm(charm); !slices.Contains(charms, charm) {
			charms = append(charms, charm)
		}
	}
	if name == types.TargetCI {
		charms = slices.DeleteFunc(charms, func(charm string) bool {
			return charm == types.CharmReadWrite || charm == types.CharmUpdate
		})
	}
	if len(charms) > 0 {
		name += ":" + strings.Join(charms, ",")
	}
	if filtered {
		name = spell + "::" + name
	}
	c.Target = name
	return c
}

// sameRun reports whether two `name[:charm,...]` targets name one target under one charm
// set. The order of the charms is not part of the identity.
func sameRun(a, b string) bool {
	nameA, charmsA := splitCharms(a)
	nameB, charmsB := splitCharms(b)
	if nameA != nameB || len(charmsA) != len(charmsB) {
		return false
	}
	for _, charm := range charmsA {
		if !slices.Contains(charmsB, charm) {
			return false
		}
	}
	return true
}

// splitCharms is a target's normalized name and charms, read the way types.ParseTarget
// reads them but without its validation: a recorded run was validated when it ran.
func splitCharms(target string) (string, []string) {
	name, list, ok := strings.Cut(target, ":")
	if !ok || list == "" {
		return types.Normalize(name), nil
	}
	charms := strings.Split(list, ",")
	for i, charm := range charms {
		charms[i] = types.NormalizeCharm(charm)
	}
	return types.Normalize(name), charms
}

// claimViolations holds a job that claims declarations (`run.go#executeStages`) to them: each
// unclaimed location of its footprint is a violation, and so is a footprint nobody could
// read, since a claim checked against nothing is an attestation. A job claiming no
// declaration gets none of this; its footprint is reported, not graded.
//
// Graded rather than reported because the footprint's placement measured 110 of 111
// placements correct on a held-out sample of 50 hunks (2026-09-25, after doc comments went
// to the declaration below them and Go var/const/type got a name), above the 80% a verdict
// needs.
func claimViolations(row types.Job, seen Observed, unclaimed []string) []string {
	if !slices.ContainsFunc(row.WritePaths, func(p string) bool { _, d := types.SplitClaim(p); return d != "" }) {
		return nil
	}
	from := seen.ChangedFrom
	if from == "" {
		from = "the job's checkpoint"
	}
	if !seen.RegionsKnown {
		reason := seen.RegionsReason
		if reason == "" {
			reason = "nothing observed the tree"
		}
		return []string{fmt.Sprintf("job %s claims declarations and its footprint is not known (%s), so no claim could be checked", row.ID, reason)}
	}
	out := make([]string, 0, len(unclaimed))
	for _, loc := range unclaimed {
		file, _ := types.SplitClaim(loc)
		out = append(out, fmt.Sprintf("the diff since %s changed %s, outside every declaration the job claims in %s (%s)",
			from, loc, file, strings.Join(ClaimedDeclarations(row, file), ", ")))
	}
	return out
}

// unclaimedFootprint is every location of regions, as its String, that lands in a file the
// row claims only by declaration (see [ClaimedDeclarations]) and that none of those
// declarations names. A file some entry covers whole is claimed whole, a file no entry covers
// is the path rules' violation rather than this one's, and a Preamble region is exempt. A
// region no driver placed locates as its whole file, which no declaration names.
func unclaimedFootprint(row types.Job, regions []types.RegionChange) []string {
	var out []string
	for _, r := range regions {
		loc := r.Location()
		if loc.Declaration == types.Preamble {
			continue
		}
		claimed := ClaimedDeclarations(row, loc.Path)
		if len(claimed) == 0 || slices.ContainsFunc(claimed, func(d string) bool { return types.NamesDeclaration(d, loc.Declaration) }) {
			continue
		}
		if s := loc.String(); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// denyViolations are the changes the job's deny paths exclude. A deny naming a declaration
// of a changed file (`run.go#A`) is graded against the footprint the way a declaration claim
// is, so a change elsewhere in the file is not one. A footprint nobody read, or a region no
// driver placed, cannot show the declaration untouched, and is one.
func denyViolations(row types.Job, rep types.JobResult, seen Observed) []string {
	var out []string
	for _, p := range rep.ChangedPaths {
		if d, ok := matching(WholeFileDenies(row.DenyPaths, p), p); ok {
			out = append(out, fmt.Sprintf("changed path %q is one the job is denied (%s)", p, d))
			continue
		}
		denied := DeniedDeclarations(row.DenyPaths, p)
		if len(denied) == 0 {
			continue
		}
		entries := make([]string, len(denied))
		for i, d := range denied {
			entries[i] = path.Clean(p) + "#" + d
		}
		if !seen.RegionsKnown {
			reason := cmp.Or(seen.RegionsReason, "nothing observed the tree")
			out = append(out, fmt.Sprintf("changed path %q has declarations the job is denied (%s) and its footprint is not known (%s), so the deny could not be checked",
				p, strings.Join(entries, ", "), reason))
			continue
		}
		for _, r := range seen.Regions {
			loc := r.Location()
			if path.Clean(loc.Path) != path.Clean(p) {
				continue
			}
			if loc.Declaration != "" && !slices.ContainsFunc(denied, func(d string) bool { return types.NamesDeclaration(d, loc.Declaration) }) {
				continue
			}
			v := fmt.Sprintf("the diff since %s changed %s, which the job is denied (%s)",
				cmp.Or(seen.ChangedFrom, "the job's checkpoint"), loc.String(), strings.Join(entries, ", "))
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

// WholeFileDenies are the deny paths that exclude file at the path level: every entry but
// one naming a declaration of file itself, which excludes only that declaration. A
// declaration of a pattern or a directory stays, since no declaration of one file can be
// read from it.
func WholeFileDenies(denyPaths []string, file string) []string {
	return slices.DeleteFunc(slices.Clone(denyPaths), func(entry string) bool {
		p, decl := types.SplitClaim(entry)
		return decl != "" && p != "" && path.Clean(p) == path.Clean(file)
	})
}

// DeniedDeclarations are the declarations denyPaths exclude in file, the repository-relative
// path of one file.
func DeniedDeclarations(denyPaths []string, file string) []string {
	var decls []string
	for _, entry := range denyPaths {
		if p, decl := types.SplitClaim(entry); decl != "" && p != "" && path.Clean(p) == path.Clean(file) {
			decls = append(decls, decl)
		}
	}
	return decls
}

// ClaimedDeclarations are the declarations row's write paths claim in file, the
// repository-relative path of one file, or nil when some entry covers the file whole or none
// covers it at all. A file row released while a pattern still matches it (see
// [NarrowConfigHold]) is held by its claims alone: the release is what ended the pattern's
// hold on it.
func ClaimedDeclarations(row types.Job, file string) []string {
	released := releasedFile(row, file)
	var decls []string
	for _, entry := range row.WritePaths {
		p, decl := types.SplitClaim(entry)
		switch {
		case decl == "" && !released && coversPath(p, file):
			return nil
		case decl != "" && p != "" && path.Clean(p) == path.Clean(file):
			decls = append(decls, decl)
		}
	}
	return decls
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
//
// A declaration claim (`run.go#executeStages`) covers its whole file here: which lines of
// the file it covers is the footprint's question, see unclaimedFootprint.
func covers(declared, p string) bool {
	declared, _ = types.SplitClaim(declared)
	return coversPath(declared, p)
}

// coversPath is covers for a declaration types.SplitClaim already cut, which must not be
// cut again: its path may hold a literal `#`.
func coversPath(declared, p string) bool {
	// A blank declaration claims nothing, on types.PathsIntersect's rule: it cleans to
	// ".", which the whole-tree arm below would read as a claim on everything.
	if declared == "" || strings.TrimSpace(p) == "" {
		return false
	}
	declared, p = path.Clean(declared), path.Clean(strings.TrimSpace(p))
	if p == "." {
		return false
	}
	if declared == "." {
		// The repository root covers every path under it. Refusing "." at declaration
		// instead would leave a single-lease plan over the whole tree undeclarable, and
		// the row would read as owning nothing rather than as owning everything.
		return true
	}
	if strings.ContainsAny(declared, globMeta) {
		return types.Glob{Pattern: declared}.Match(p)
	}
	return p == declared || strings.HasPrefix(p, declared+"/")
}

// globMeta are the characters that make a declared path a glob, as types.LiteralPrefix
// reads them.
const globMeta = "*?[{"

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
func verifySubjectGate(gate types.Goal, seen Observed) types.GateStatus {
	status := types.GateStatus{ID: gate.ID}
	kind, expect := gate.Kind, gate.Expect

	// The observation this pair needs, and whether magus actually made it. Asked once, up
	// front, because every arm below has the same answer for the same reason: a subject
	// nobody looked at is not a subject that satisfies anything.
	if why := seen.missing(kind, expect); why != "" {
		status.Violations = append(status.Violations, why)
		return status
	}
	graded := 0
	for _, declared := range gate.Subject() {
		if declared = strings.TrimSpace(declared); declared == "" {
			continue
		}
		graded++
		if held, why := seen.holds(kind, expect, declared, gate.Signature); !held {
			status.Violations = append(status.Violations, why)
		}
	}
	if graded == 0 {
		status.Violations = append(status.Violations, "names nothing to examine, so nothing could satisfy it")
	}
	status.Verified = len(status.Violations) == 0
	return status
}

// WaitIntegration grades job id's check goals, its check included, against the runs rep's
// evidence names in checkout, the caller's tree, and its symbol goals expecting present,
// absent, unreferenced or a signature against symbols, the caller's graph, and records the
// grade as the row's Integration. It is `magus job wait --integration`: the question is
// whether the work still holds once merged with other work, which the holder's own verdict
// cannot say. A nil symbols leaves those goals unmet, never passed.
//
// `changed` symbol goals, paths goals, the diff and the footprint belong to the holder's
// checkout and were graded there. A run under any lease is accepted: the runs are the
// caller's, made by whatever validators it ran in its tree. It leaves State alone and works on an ended row, so a
// pass where the work was written and a failure once merged stay two readable facts.
// Evidence naming a goal the row does not hold as a check is an error, and nothing is
// recorded.
func WaitIntegration(ctx context.Context, store *Store, id string, rep types.JobResult, resolve AttemptResolver, checkout string, symbols SymbolReader) (types.JobIntegration, error) {
	if resolve == nil {
		return types.JobIntegration{}, fmt.Errorf("job: no output resolver reads %s's runs", checkout)
	}
	rows, err := store.List()
	if err != nil {
		return types.JobIntegration{}, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return types.JobIntegration{}, fmt.Errorf("job: there is no job %q", id)
	}
	row := rows[i]
	if rep.Job != "" && rep.Job != id {
		return types.JobIntegration{}, fmt.Errorf("job: the evidence is filed under job %q and this one is %q", rep.Job, id)
	}
	var checks, held []types.Goal
	for _, gate := range row.EffectiveGoals() {
		switch {
		case gate.Kind == types.GoalKindCheck:
			checks = append(checks, gate)
		case gate.Kind == types.GoalKindSymbol && gate.Expect != types.ExpectChanged:
			held = append(held, gate)
		}
	}
	if len(checks) == 0 && len(held) == 0 {
		return types.JobIntegration{}, fmt.Errorf("job: %s declares no check, no check goal and no symbol goal another tree can hold,"+
			" so nothing in another tree can grade it", id)
	}
	refs := map[string]string{}
	if ref := strings.TrimSpace(rep.Validation.OutputRef); ref != "" {
		refs[types.PrimaryGoalID] = ref
	}
	for _, evidence := range rep.GateEvidence {
		if !slices.ContainsFunc(checks, func(g types.Goal) bool { return g.ID == evidence.GateID }) {
			return types.JobIntegration{}, fmt.Errorf("job: the evidence names goal %q, and %s holds no check goal by that id", evidence.GateID, id)
		}
		refs[evidence.GateID] = strings.TrimSpace(evidence.OutputRef)
	}
	grade := types.JobIntegration{Checkout: checkout, Verified: true}
	for _, gate := range checks {
		ref := refs[gate.ID]
		var attempt types.JobAttempt
		if ref != "" {
			if attempt, err = resolve(ctx, ref); err != nil {
				return types.JobIntegration{}, err
			}
		}
		status := verifyGate(row, gate, ref, attempt, Observed{anyLease: true})
		grade.Verified = grade.Verified && status.Verified
		grade.Gates = append(grade.Gates, status)
	}
	if len(held) > 0 {
		var seen Observed
		seen.readSymbols(ctx, held, symbols)
		for _, gate := range held {
			status := verifySubjectGate(gate, seen)
			grade.Verified = grade.Verified && status.Verified
			grade.Gates = append(grade.Gates, status)
		}
	}
	stored, err := store.mutate(ctx, id, asObservation, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: there is no job %q", id)
		}
		grade.At = now
		cur.Integration = &grade
		return nil
	})
	if err != nil {
		return types.JobIntegration{}, err
	}
	return *stored.Integration, nil
}
