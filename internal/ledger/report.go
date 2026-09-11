package ledger

import (
	_ "embed"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// ReportSchemaVersion is the version of the report shape this magus accepts. A worker
// sends it, the decoder rejects what it does not know by name, and the schema requires
// it. See types.LeaseSchemaVersion for the row's half of the same rule.
const ReportSchemaVersion = 1

// ReportSchema is the JSON Schema for [Report], embedded so a harness can give a worker a
// response format without magus having to render one. Generated from the struct itself;
// see [RowSchema].
//
//go:embed gen/report.schema.json
var ReportSchema string

// Report is what a worker returns when its lease is done: changed paths, validation
// evidence, descendants created, unresolved risks.
//
// A struct rather than the prose the skill asked for since it was written. Prose on both
// ends means acceptance is the root READING a paragraph, and a worker that ran a filtered
// subset writes the same paragraph as one that did the work. This does not make a worker
// honest; it makes a dishonest report CHECKABLE, which is what [Grade] then does.
//
// JSON only: it is decoded from stdin and never emitted, so it carries no yaml tags.
type Report struct {
	// SchemaVersion is the shape this report was written in, and it is REQUIRED: the
	// decoder rejects a version it does not know by name, so a worker whose harness is a
	// release ahead reads "this magus accepts version 1" instead of watching a field it
	// was told to send be rejected as unknown. See [ReportSchemaVersion].
	SchemaVersion int `json:"schema_version"`
	// Lease is the id the worker believes it was handed. Optional, and checked against the
	// row when present: a report filed under the wrong id happens when several workers
	// share a prompt template, and it reads as success because every other field is
	// plausible.
	Lease string `json:"lease,omitempty"`
	// ChangedPaths are the workspace-relative paths this worker wrote, as it reports them.
	// Never proof: the integration pass compares the ledger against the ACTUAL diff since
	// the checkpoint. This is the CLAIM, and checking it against the declared boundary is
	// what catches the worker that widened its own scope.
	ChangedPaths []string `json:"changed_paths"`
	// Validation is the one check the lease was assigned, and how it ended.
	Validation ReportValidation `json:"validation"`
	// Descendants are the lease ids this worker handed work on to. One the ledger does not
	// carry is a branch of the plan nobody is tracking, which [Grade] reports.
	Descendants []string `json:"descendants,omitempty"`
	// UnresolvedRisks is what the worker could not settle. Never omitempty: an absent list
	// and an empty one are the same JSON once the key can vanish, so a worker that never
	// considered the question would be indistinguishable from one that found nothing.
	UnresolvedRisks []string `json:"unresolved_risks"`
}

// ReportValidation is the evidence for the lease's assigned check. OutputRef is what makes
// it evidence rather than an assertion: it names a log magus stored, and the store's own
// record of that run says which command produced it and how it ended.
//
// THERE IS NO `passed` FIELD, and its absence is the point: a worker's verdict on its own
// run reads identical whether the check ran or not. [Grade] derives the outcome from the
// stored attempt.
type ReportValidation struct {
	// Command is the check as the worker ran it, rendered beside the verdict for a person.
	// The row's own validation is what the evidence is bound to, so a command here that
	// disagrees with the ref is a discrepancy a reader sees rather than a rule.
	Command string `json:"command"`
	// OutputRef is the reference id magus stored that run's captured output under.
	OutputRef string `json:"output_ref"`
}

// Verdict is the result of grading one report against one row: every rule that failed,
// and nothing about the rules that held.
//
// Violations rather than a bare boolean, because the caller is an orchestrator deciding
// what to do next and "not accepted" sends it nowhere. A path outside the boundary means
// repartition; a missing output ref means ask for the run again.
type Verdict struct {
	Lease      string   `json:"lease"                yaml:"lease"`
	Accepted   bool     `json:"accepted"             yaml:"accepted"`
	Violations []string `json:"violations,omitempty" yaml:"violations,omitempty"`
	// Risks and Command are carried through from the report so the one reader who has to
	// act on them sees them. A required field nobody renders teaches a worker that filling
	// it in is theater.
	Risks   []string `json:"unresolved_risks,omitempty" yaml:"unresolved_risks,omitempty"`
	Command string   `json:"command,omitempty"          yaml:"command,omitempty"`
}

// Attempt is what the output store recorded for one captured run: which command produced
// it, and whether that command failed. The identity is STRUCTURED because the store holds
// it that way, so comparing project/target/spell binds a ref to a check without either
// side having to spell a command line the same way.
type Attempt struct {
	// Found is false for a ref no longer in the store, which is not an error: the root
	// cannot reopen it either way, and that is the fact acceptance turns on.
	Found bool
	// Project, Target and Spell are the run's recorded identity. Spell is the
	// `spell::op` filter that selected the definition, empty when the run named none.
	Project string
	Target  string
	Spell   string
	// Failed is the run's own outcome, which is what [Accept] reads INSTEAD of a
	// worker's claim to have passed.
	Failed bool
}

// Grade grades a worker's report against the lease it was handed: every changed path
// inside the declared boundary and outside the declared deny list, a change set that is
// not empty on a row that writes, descendants the plan carries, and an evidence ref
// recording a PASSING run of that row's own check.
//
// IT GRADES EVIDENCE, NOT ASSERTIONS, which is the difference between this and reading
// the report: every rule turns on something magus already holds. Whether the work is GOOD,
// and whether the criteria in Goal were met, stay the orchestrator's reading.
//
// att is what the output store recorded for the report's ref, and the zero Attempt when
// the report names none; resolving it is the caller's, so no rule here reads a file while
// the ledger's lock is held. declared is the rest of the plan, which is what the report's
// descendant ids are checked against.
func Grade(row types.Lease, rep Report, att Attempt, declared []types.Lease) Verdict {
	v := Verdict{Lease: row.ID, Risks: rep.UnresolvedRisks, Command: rep.Validation.Command}

	if rep.Lease != "" && rep.Lease != row.ID {
		v.Violations = append(v.Violations, fmt.Sprintf("the report is filed under lease %q and this row is %q", rep.Lease, row.ID))
	}
	if row.State.Terminal() {
		v.Violations = append(v.Violations, fmt.Sprintf("lease %s is already %s, and a row is graded once:"+
			" clear the plan or declare a new row rather than re-grading a closed one", row.ID, row.State))
	}

	switch {
	case row.ReadOnly && len(rep.ChangedPaths) > 0:
		v.Violations = append(v.Violations, fmt.Sprintf("lease %s is read-only and the report claims %d changed path(s)", row.ID, len(rep.ChangedPaths)))
	case !row.ReadOnly && len(rep.ChangedPaths) == 0:
		// A worker that wrote nothing has not done the work or has not reported it, and
		// the two are the same thing to a root deciding whether to integrate. A lease
		// that genuinely writes nothing is read_only, which is the row that says so.
		v.Violations = append(v.Violations, fmt.Sprintf("lease %s is not read-only and the report claims no changed paths at all", row.ID))
	default:
		for _, p := range rep.ChangedPaths {
			if _, ok := matching(row.OwnedPaths, p); !ok {
				v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is outside the lease's owned paths (%s)", p, strings.Join(row.OwnedPaths, ", ")))
			}
		}
	}
	for _, p := range rep.ChangedPaths {
		if d, ok := matching(row.ForbiddenPaths, p); ok {
			v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is one the lease is forbidden (%s)", p, d))
		}
	}
	for _, id := range rep.Descendants {
		if !slices.ContainsFunc(declared, func(r types.Lease) bool { return r.ID == id }) {
			v.Violations = append(v.Violations, fmt.Sprintf("the report names descendant %q and no row declares it,"+
				" so that branch of the plan is one nobody is tracking", id))
		}
	}

	v.Violations = append(v.Violations, evidence(row, rep, att)...)
	v.Accepted = len(v.Violations) == 0
	return v
}

// evidence grades the ref against the row's check, one violation per rule that failed.
func evidence(row types.Lease, rep Report, att Attempt) []string {
	ref := strings.TrimSpace(rep.Validation.OutputRef)
	if ref == "" {
		return []string{"the report carries no validation output_ref, so there is no run to reopen"}
	}
	if !att.Found {
		return []string{fmt.Sprintf("output ref %q is not in this workspace's output store", ref)}
	}

	var out []string
	switch {
	case row.Check == nil:
		out = append(out, fmt.Sprintf("lease %s declares no check, so no stored run can be bound to it."+
			" Declare one as `<target> <project> [-- args]` and have the worker run it", row.ID))
	case !bindsTo(*row.Check, att):
		out = append(out, fmt.Sprintf("output ref %q records `%s` and this lease's check is `%s`,"+
			" so the evidence is from a different run", ref, att, row.Check))
	}
	if att.Failed {
		out = append(out, fmt.Sprintf("the run behind output ref %q failed, so its validation did not pass", ref))
	}
	return out
}

// bindsTo reports whether a stored attempt is a run of this check.
//
// The spell filter is compared only when BOTH sides name one. A row that names
// `go::go-test` and an attempt the store recorded under a bare target are the same run
// selected two ways, and rejecting that pair would make the rule fire on spelling rather
// than on identity. Target and project are compared always: those are what a run IS.
//
// The args past `--` are NOT compared: the output store records a run by spell, target and
// project and holds no argv, so a rule keyed on them would reject every ref there is.
func bindsTo(c types.LeaseCheck, a Attempt) bool {
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

// String renders what the store recorded, in the same spelling a check renders, so a
// rejection puts the two side by side.
func (a Attempt) String() string {
	target := a.Target
	if a.Spell != "" {
		target = a.Spell + "::" + target
	}
	return types.LeaseCheck{Target: target, Project: a.Project}.String()
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
