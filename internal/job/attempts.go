package job

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

const (
	// maxPrior is how many ended attempts a row keeps, newest last.
	maxPrior = 3
	// maxFolded caps each list one ended attempt keeps.
	maxFolded = 8
	// maxDenialsRead caps the denials read from the trail for one attempt, before the fold
	// keeps the newest maxFolded.
	maxDenialsRead = 64
)

// wideningRules are the guard rules that deny a holder a path outside its job's boundary:
// another attempt inside the same boundary meets the same denial. internal/guard cannot be
// imported here, so a test holds each name to a rule the guard catalogs.
var wideningRules = []string{"claimed-declaration", "focus-read", "lease-write"}

// outsideWritePaths is the phrase [VerifyGates] words a changed path outside the job's write
// paths with.
const outsideWritePaths = "outside the job's write paths"

// nextGeneration is row's Generation once written over prev: 1 for a new row, and one more
// than prev's for a write that changes the declared spec ([specChanges]). An observation
// declares nothing, and neither does a write that only widens the write paths: evidence
// taken under the narrower set still asks what the wider one asks.
func nextGeneration(prev, row types.Job, exists bool, kind grading) int {
	if !exists {
		return 1
	}
	generation := max(prev.Generation, 1)
	if kind != asObservation && changesSpec(prev, row) {
		generation++
	}
	return generation
}

// changesSpec reports whether row declares anything prev did not, other than write paths
// that each hold every path prev held.
func changesSpec(prev, row types.Job) bool {
	changes := specChanges(prev, row)
	if len(changes) == 1 && changes[0] == "write_paths" {
		return !widens(prev.WritePaths, row.WritePaths)
	}
	return len(changes) > 0
}

// widens reports whether every path in held lies within some path of next.
func widens(held, next []string) bool {
	return !slices.ContainsFunc(held, func(h string) bool {
		return !slices.ContainsFunc(next, func(n string) bool { return types.PathWithin(h, n) })
	})
}

// refuseAttempt refuses a fork of ended row prev, declared again as row, that starts an
// attempt past row's max_attempts. The cap read is the new record's, so a fork that raises
// it in the same write passes.
func refuseAttempt(id string, prev, row types.Job) error {
	used := max(prev.Attempts, 1)
	if row.MaxAttempts == 0 || used < row.MaxAttempts {
		return nil
	}
	err := fmt.Errorf("%w: %s used %d of its %d attempt(s): fork it with a record raising max_attempts (`%s`), or the root does the job itself",
		ErrMaxAttempts, id, used, row.MaxAttempts, hint.JobFork.With("--stdin"))
	return types.NonRetryable(err, types.RetryAfterPerson, "a person raises max_attempts or does the job")
}

// foldAttempt is ended row prev as the attempt the next one inherits: what it ended as,
// what its last wait refused, what it left unresolved and the failed runs to reopen, with
// denials the guard's refusals of its holder and known whether the trail still reached back
// to its start. now stands in for an end prev never stamped.
func foldAttempt(prev types.Job, denials []types.JobDenial, known bool, now int64) types.PriorAttempt {
	p := types.PriorAttempt{
		N:                  max(prev.Attempts, 1),
		State:              prev.State,
		ObservedGeneration: prev.Generation,
		EndReason:          prev.EndReason,
		Checkpoint:         prev.Checkpoint,
		ReportedBase:       prev.ReportedBase,
		Ended:              cmp.Or(prev.Updated, now),
		ReworkRounds:       prev.ReworkRounds,
		Widenings:          prev.Widenings,
		Denials:            slices.Clone(denials[max(len(denials)-maxFolded, 0):]),
		DenialsKnown:       known,
	}
	if prev.ModelTier != nil {
		p.ModelTier = prev.ModelTier.Name
	}
	if r := prev.Rejection; r != nil {
		p.Unmet = cloneGates(r.Unmet[:min(len(r.Unmet), maxFolded)])
		p.Violations = slices.Clone(r.Violations[:min(len(r.Violations), maxFolded)])
	}
	if r := prev.Result; r != nil {
		p.UnresolvedRisks = slices.Clone(r.UnresolvedRisks[:min(len(r.UnresolvedRisks), maxFolded)])
	}
	runs := make([]types.JobAttempt, 0, len(prev.GateAttempts)+1)
	if prev.Attempt != nil {
		runs = append(runs, *prev.Attempt)
	}
	for _, g := range prev.GateAttempts {
		runs = append(runs, g.Attempt)
	}
	for _, run := range runs {
		if run.Failed && run.Ref != "" && !slices.Contains(p.FailingRefs, run.Ref) && len(p.FailingRefs) < maxFolded {
			p.FailingRefs = append(p.FailingRefs, run.Ref)
		}
	}
	p.Retry = classifyAttempt(p, prev.Prior)
	return p
}

// classifyAttempt says what must change before another attempt like p could help, earlier
// being the attempts before it, newest last. A boundary denial or a changed path outside the
// write paths needs a widening; a goal unmet in p and in the attempt before it needs a
// person, since a second agent already failed it. Anything else, an end the sweep wrote
// included, is retryable as is.
func classifyAttempt(p types.PriorAttempt, earlier []types.PriorAttempt) types.RetryVerdict {
	for _, d := range p.Denials {
		if slices.Contains(wideningRules, d.Rule) {
			reason := "the guard denied its holder under " + d.Rule
			if line := firstLine(d.Reason); line != "" {
				reason += ": " + line
			}
			return types.RetryVerdict{Change: types.RetryAfterWidening, Reason: reason}
		}
	}
	for _, v := range p.Violations {
		if strings.Contains(v, outsideWritePaths) {
			return types.RetryVerdict{Change: types.RetryAfterWidening, Reason: v}
		}
	}
	if n := len(earlier); n > 0 {
		before := earlier[n-1]
		for _, goal := range p.Unmet {
			if slices.ContainsFunc(before.Unmet, func(g types.GateStatus) bool { return g.ID == goal.ID }) {
				return types.RetryVerdict{Change: types.RetryAfterPerson,
					Reason: fmt.Sprintf("goal %s was unmet in attempts %d and %d", goal.ID, before.N, p.N)}
			}
		}
	}
	return types.RetryVerdict{Retryable: true}
}

// evidenceAt is the evidence row holds for a grade at its own generation: its result and the
// runs behind its check and goals, with prior the oldest generation among the records taken
// against an earlier spec, zero when none was.
//
// A run taken at an earlier generation is set aside, since it ran for a spec that asked for
// something else. A result filed then still grades, its changed paths checked against the
// diff either way, and only marks prior. A record with no stamp is unknown and grades as
// current.
func evidenceAt(row types.Job) (ev graded, prior int) {
	older := func(observed int) bool {
		if !row.StaleAt(observed) {
			return false
		}
		if prior == 0 || observed < prior {
			prior = observed
		}
		return true
	}
	ev.result = types.JobResult{Job: row.ID}
	if row.Result != nil {
		ev.result = *row.Result
		older(row.Result.ObservedGeneration)
	}
	if row.Attempt != nil && !older(row.Attempt.ObservedGeneration) {
		ev.attempt = *row.Attempt
	}
	for _, g := range row.GateAttempts {
		if !older(g.Attempt.ObservedGeneration) {
			ev.gateAttempts = append(ev.gateAttempts, g)
		}
	}
	return ev, prior
}

// unmetGates are the goals among gates that did not verify, at most maxFolded, copied.
func unmetGates(gates []types.GateStatus) []types.GateStatus {
	var out []types.GateStatus
	for _, g := range gates {
		if !g.Verified && len(out) < maxFolded {
			out = append(out, g)
		}
	}
	return cloneGates(out)
}

func cloneGates(in []types.GateStatus) []types.GateStatus {
	if in == nil {
		return nil
	}
	out := slices.Clone(in)
	for i := range out {
		out[i].Violations = slices.Clone(out[i].Violations)
	}
	return out
}
