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
	// SchemaVersion is the shape this report was written in, and it is REQUIRED: the
	// decoder rejects a version it does not know by name, so a worker whose harness is a
	// release ahead reads "this magus accepts version 1" instead of watching a field it
	// was told to send be rejected as unknown. See [ReportSchemaVersion].
	SchemaVersion int `json:"schema_version" yaml:"schema_version"`
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
// log magus stored, so the root can reopen the bytes with `magus query output <ref>`, and
// the store's own record of that run says which command produced it and how it ended.
//
// THERE IS NO `passed` FIELD, and its absence is the point. A worker's verdict on its own
// run is exactly the assertion this type exists to replace: the one that reads identical
// whether the check ran or not. [Accept] derives the outcome from the stored attempt.
type ReportValidation struct {
	// Command is the check as the worker ran it, kept for a person reading the report.
	// The row's own validation is what the evidence is bound to, so a command here that
	// disagrees with the ref is a discrepancy a reader sees rather than a rule.
	Command string `json:"command" yaml:"command"`
	// OutputRef is the reference id magus stored that run's captured output under.
	OutputRef string `json:"output_ref" yaml:"output_ref"`
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

// Attempt is what the output store recorded for one captured run: which command produced
// it, and whether that command failed.
//
// The identity is STRUCTURED because the store holds it that way. A worker's ref either
// names a run of the check its lease was assigned or it does not, and comparing
// project/target/spell answers that without either side having to spell a command line
// the same way.
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

// OutputLookup resolves an output ref against this workspace's store. The error arm is
// for a store that could not be read at all, which must not read as a worker filing a bad
// ref; an unknown ref answers with a zero Attempt and no error.
type OutputLookup func(ref string) (Attempt, error)

// Accept grades a worker's report against the lease it was handed: every changed path
// inside the declared boundary, a change set that is not empty on a row that writes, and
// an evidence ref that resolves to a run OF THAT ROW'S CHECK which the store recorded as
// passing.
//
// IT GRADES EVIDENCE, NOT ASSERTIONS, which is the difference between this and reading
// the report. Every rule below turns on something magus already holds: the row's own
// check, the ref's recorded identity, its exit status, the declared boundary. A report
// with an empty change set, a ref from an unrelated run, and a cheerful summary was
// accepted by the version that trusted the worker's own `passed` (measured 2026-09-11);
// nothing a worker writes decides an outcome here now.
//
// MECHANICAL, and that is the whole of its claim. Whether the work is GOOD, and whether
// the criteria in Goal were met, stay the orchestrator's reading.
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
	case !row.ReadOnly && len(rep.ChangedPaths) == 0:
		// A worker that wrote nothing has not done the work or has not reported it, and
		// the two are the same thing to a root deciding whether to integrate. A lease
		// that genuinely writes nothing is read_only, which is the row that says so.
		v.Violations = append(v.Violations, fmt.Sprintf("lease %s is not read-only and the report claims no changed paths at all", row.ID))
	default:
		for _, p := range rep.ChangedPaths {
			if !ownedBy(row.OwnedPaths, p) {
				v.Violations = append(v.Violations, fmt.Sprintf("changed path %q is outside the lease's owned paths (%s)", p, strings.Join(row.OwnedPaths, ", ")))
			}
		}
	}

	ev, err := evidence(row, rep, lookup)
	if err != nil {
		return Verdict{}, err
	}
	v.Violations = append(v.Violations, ev...)

	v.Accepted = len(v.Violations) == 0
	return v, nil
}

// evidence grades the ref against the row's check, returning one violation per rule that
// failed. The error arm is a store that could not answer.
func evidence(row types.Lease, rep Report, lookup OutputLookup) ([]string, error) {
	ref := strings.TrimSpace(rep.Validation.OutputRef)
	if ref == "" {
		return []string{"the report carries no validation output_ref, so there is no run to reopen"}, nil
	}
	att, err := lookup(ref)
	if err != nil {
		return nil, fmt.Errorf("ledger: look up output %s: %w", ref, err)
	}
	if !att.Found {
		return []string{fmt.Sprintf("output ref %q is not in this workspace's output store", ref)}, nil
	}

	var out []string
	check, ok := parseCheck(row.Validation)
	switch {
	case !ok:
		out = append(out, fmt.Sprintf("lease %s is assigned %q, which is not a `magus run <target> <project>` line,"+
			" so no stored run can be bound to it", row.ID, row.Validation))
	case !check.matches(att):
		out = append(out, fmt.Sprintf("output ref %q records %s and this lease's validation is %s,"+
			" so the evidence is from a different run", ref, att.command(), check))
	}
	if att.Failed {
		out = append(out, fmt.Sprintf("the run behind output ref %q failed, so its validation did not pass", ref))
	}
	return out, nil
}

// check is the run one validation string names: what the output store records about a
// run, in the same three parts, so the two can be compared without either side spelling
// a command line the same way.
type check struct {
	Spell   string
	Target  string
	Project string
}

// parseCheck reads the `magus run <target> <project>` a validation field names, or
// reports that it names none.
//
// It reads POSITIONS and ignores everything else: argv[0] however the row spelled the
// binary, flags, and everything after `--` (which belongs to the tool being run, not to
// magus). What is left is the target and the project, which is the whole of a run's
// identity as the output store records it.
func parseCheck(s string) (check, bool) {
	fields := strings.Fields(s)
	if i := slices.Index(fields, "--"); i >= 0 {
		fields = fields[:i]
	}
	var words []string
	for i, f := range fields {
		if i == 0 || strings.HasPrefix(f, "-") {
			continue
		}
		words = append(words, f)
	}
	if len(words) < 2 || words[0] != "run" {
		return check{}, false
	}
	spell, target, _ := strings.Cut(words[1], "::")
	if target == "" {
		spell, target = "", spell
	}
	// A charm is a way of running the target, not another target: `generate:rw` and
	// `generate` are one identity to the output store.
	target, _, _ = strings.Cut(target, ":")
	c := check{Spell: spell, Target: target, Project: "."}
	if len(words) > 2 {
		c.Project = words[2]
	}
	c.Project = path.Clean(c.Project)
	return c, target != ""
}

// matches reports whether a stored attempt is a run of this check.
//
// The spell filter is compared only when BOTH sides name one. A row that names
// `go::go-test` and an attempt the store recorded under a bare target are the same run
// selected two ways, and rejecting that pair would make the rule fire on spelling rather
// than on identity. Target and project are compared always: those are what a run IS.
func (c check) matches(a Attempt) bool {
	project := path.Clean(a.Project)
	if project == "" {
		project = "."
	}
	if c.Target != a.Target || c.Project != project {
		return false
	}
	return c.Spell == "" || a.Spell == "" || c.Spell == a.Spell
}

func (c check) String() string { return "`" + renderCheck(c.Spell, c.Target, c.Project) + "`" }

// command renders what the store recorded, in the same spelling a check renders, so a
// rejection puts the two side by side.
func (a Attempt) command() string { return "`" + renderCheck(a.Spell, a.Target, a.Project) + "`" }

func renderCheck(spell, target, project string) string {
	if spell != "" {
		target = spell + "::" + target
	}
	if project == "" {
		project = "."
	}
	return "magus run " + target + " " + project
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
