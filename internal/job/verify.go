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
const ResultSchemaVersion = 1

// ResultSchema is the JSON Schema for [types.JobResult], embedded so a harness can give a
// holder a response format without magus having to render one. Generated from the struct
// itself; see [DeclarationSchema].
//
//go:embed gen/result.schema.json
var ResultSchema string

// Status is what waiting on a job answers: every rule that failed, and nothing about the
// rules that held.
//
// Violations rather than a bare boolean, because the caller is deciding what to do next
// and "not verified" sends it nowhere. A path outside the lanes means repartition; a
// missing output ref means ask for the run again.
type Status struct {
	Job        string   `json:"job"                  yaml:"job"`
	Verified   bool     `json:"verified"             yaml:"verified"`
	Violations []string `json:"violations,omitempty" yaml:"violations,omitempty"`
	// Risks and Command are carried through from the result so the one reader who has to
	// act on them sees them. A required field nobody renders teaches a holder that filling
	// it in is theater.
	Risks   []string `json:"unresolved_risks,omitempty" yaml:"unresolved_risks,omitempty"`
	Command string   `json:"command,omitempty"          yaml:"command,omitempty"`
}

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

	v.Violations = append(v.Violations, evidence(row, rep, att)...)
	v.Verified = len(v.Violations) == 0
	return v
}

// evidence checks the ref against the job's check, one violation per rule that failed.
func evidence(row types.Job, rep types.JobResult, att types.JobAttempt) []string {
	ref := strings.TrimSpace(rep.Validation.OutputRef)
	if ref == "" {
		return []string{"the result carries no validation output_ref, so there is no run to reopen"}
	}
	if !att.Found {
		return []string{fmt.Sprintf("no run is recorded for output ref %q", ref)}
	}

	var out []string
	switch {
	case row.Check == nil:
		out = append(out, fmt.Sprintf("job %s declares no check, so no recorded run can be bound to it."+
			" Declare one as `<target> <project> [-- args]` and have its holder run it", row.ID))
	case !bindsTo(*row.Check, att):
		out = append(out, fmt.Sprintf("output ref %q records `%s` and this job's check is `%s`,"+
			" so the evidence is from a different run", ref, att, row.Check))
	}
	if att.Failed {
		out = append(out, fmt.Sprintf("the run behind output ref %q failed, so its check did not pass", ref))
	}
	return out
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
