package ledger

import (
	_ "embed"
	"fmt"
	"path"
	"strings"

	"github.com/egladman/magus/types"
)

// ReportSchema is the JSON Schema for [Report], embedded so a harness can give a
// worker a response format without magus having to render one.
//
// A file beside the struct rather than reflection over it: the schema is the CONTRACT a
// host's typed-output mode compiles against, and one generated from Go tags would change
// shape whenever the struct's internals did. TestReportSchemaMatchesTheStruct keeps the
// two honest in the only direction that matters, which is that neither grows a field the
// other does not have.
//
//go:embed report.schema.json
var ReportSchema string

// Report is what a worker returns when its lease is done.
//
// The magus-multi-agent skill has demanded these four things in prose since it was
// written: changed paths, validation evidence, descendants created, unresolved risks.
// Prose on both ends means acceptance is the root READING a paragraph, and a worker that
// ran a filtered subset or quietly restated its criteria writes the same paragraph as one
// that did the work. A struct does not make a worker honest; it makes the dishonest report
// CHECKABLE, which is what [Accept] then does.
type Report struct {
	// Lease is the id the worker believes it was handed. Optional, and checked against
	// the row when present: a report filed under the wrong id is a real failure mode
	// when several workers share a prompt template, and one that reads as success
	// because every other field is plausible.
	Lease string `json:"lease,omitempty" yaml:"lease,omitempty"`
	// ChangedPaths are the workspace-relative paths this worker wrote, as it reports
	// them. Never proof: step 1 of the skill's integration pass compares the ledger
	// against the ACTUAL diff since the checkpoint, and that is the half that does not
	// depend on a worker cooperating. This is the CLAIM, and checking it against the
	// declared boundary is what catches the worker that widened its own scope.
	ChangedPaths []string `json:"changed_paths" yaml:"changed_paths"`
	// Validation is the one check the lease was assigned, and how it ended.
	Validation ReportValidation `json:"validation" yaml:"validation"`
	// Descendants are the lease ids this worker handed work on to. Empty is the ordinary
	// answer; a non-empty one the root ledger does not carry is a branch of the plan
	// nobody is tracking.
	Descendants []string `json:"descendants,omitempty" yaml:"descendants,omitempty"`
	// UnresolvedRisks is what the worker could not settle. Never omitempty, and that is
	// deliberate: an absent list and an empty one are the same JSON once the key can
	// vanish, so a worker that never considered the question would be indistinguishable
	// from one that considered it and found nothing. A mis-scoped worker saying so here
	// is the cheapest signal in the whole loop.
	UnresolvedRisks []string `json:"unresolved_risks" yaml:"unresolved_risks"`
}

// ReportValidation is the evidence for the lease's assigned check.
//
// OutputRef is the field that makes this evidence rather than an assertion. A ref names a
// log magus stored, so the root can reopen the bytes with `magus query output <ref>`; a
// worker's "tests passed" cannot be reopened at all.
type ReportValidation struct {
	// Command is the check as the worker ran it.
	Command string `json:"command" yaml:"command"`
	// OutputRef is the reference id magus stored that run's captured output under.
	OutputRef string `json:"output_ref" yaml:"output_ref"`
	// Passed is the worker's verdict on its own run. Checked, never trusted alone: it
	// is one boolean beside a ref that either resolves or does not.
	Passed bool `json:"passed" yaml:"passed"`
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
}

// OutputLookup reports whether an output ref still resolves in this workspace's store.
// The error arm is for a store that could not be read at all, which must not read as a
// worker filing a bad ref.
type OutputLookup func(ref string) (bool, error)

// Accept grades a worker's report against the lease it was handed: every changed path
// inside the declared boundary, the validation passed, and its evidence ref still
// resolving in the output store.
//
// MECHANICAL, and that is the whole of its claim. It does not judge whether the work is
// good, whether the criteria in Goal were met, or whether the command the worker ran was
// the one it was assigned; those are the root's reading. What it settles is the set of
// failures a root reading prose reliably misses, because each of them appears in a report
// that otherwise reads exactly like a success.
//
// It returns a verdict for a FAILING report rather than an error. A rejection is an answer
// about the report; the error arm is reserved for a store that could not answer at all.
func Accept(row types.Lease, rep Report, lookup OutputLookup) (Verdict, error) {
	v := Verdict{Lease: row.ID}

	if rep.Lease != "" && rep.Lease != row.ID {
		v.Violations = append(v.Violations, fmt.Sprintf("the report is filed under lease %q and this row is %q", rep.Lease, row.ID))
	}

	switch {
	case row.ReadOnly && len(rep.ChangedPaths) > 0:
		v.Violations = append(v.Violations, fmt.Sprintf("lease %s is read-only and the report claims %d changed path(s)", row.ID, len(rep.ChangedPaths)))
	default:
		for _, p := range rep.ChangedPaths {
			if !ownedBy(row.OwnedPaths, p) {
				v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is outside the lease's owned paths (%s)", p, strings.Join(row.OwnedPaths, ", ")))
			}
		}
	}

	if !rep.Validation.Passed {
		v.Violations = append(v.Violations, "the report does not claim its validation passed")
	}

	switch ref := strings.TrimSpace(rep.Validation.OutputRef); ref {
	case "":
		v.Violations = append(v.Violations, "the report carries no validation output_ref, so there is no run to reopen")
	default:
		found, err := lookup(ref)
		if err != nil {
			return Verdict{}, fmt.Errorf("ledger: look up output %s: %w", ref, err)
		}
		if !found {
			v.Violations = append(v.Violations, fmt.Sprintf("output ref %q is not in this workspace's output store", ref))
		}
	}

	v.Accepted = len(v.Violations) == 0
	return v, nil
}

// ownedBy reports whether any declaration covers p.
func ownedBy(owned []string, p string) bool {
	for _, d := range owned {
		if covers(d, p) {
			return true
		}
	}
	return false
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
	declared, p = path.Clean(strings.TrimSpace(declared)), path.Clean(strings.TrimSpace(p))
	if declared == "." || p == "." {
		return false
	}
	if types.LiteralPrefix(declared) != declared {
		return types.MatchesAnyGlob([]string{declared}, p)
	}
	return p == declared || strings.HasPrefix(p, declared+"/")
}
