package check

import (
	"fmt"
	"strings"
)

// Status is one check's outcome. Advice is never a gate: it is worth
// knowing, and only Fail breaks a report.
type Status string

// Fail is for a workspace that is wrong in a way no taste can rescue, such
// as a dependency cycle or two targets claiming one output. Advice is for a
// convention magus recommends and does not impose. No switch promotes
// advice to failure: a workspace that wants a convention enforced enforces
// it in its own lint target.
const (
	OK     Status = "ok"
	Fail   Status = "fail"
	Advice Status = "advice"
)

// Evidence says what a verdict rests on, a different question from what the
// verdict is. A check that could not run must not read as one that ran and
// found nothing wrong.
type Evidence string

// The four are ordered by how much they entitle a reader to believe.
// Measured observed the state, Declared took the workspace at its word,
// Inferred came from a derived model such as the knowledge graph. Unknown
// means the check did not run, and it is not a pass.
const (
	EvidenceMeasured Evidence = "measured"
	EvidenceDeclared Evidence = "declared"
	EvidenceInferred Evidence = "inferred"
	EvidenceUnknown  Evidence = "unknown"
)

// Check is one validation and what it found: a doctor check over the
// workspace, or a conformance check over a changed symbol.
type Check struct {
	Name    string   `json:"name" yaml:"name"`
	Status  Status   `json:"status" yaml:"status"`
	Message string   `json:"message,omitempty" yaml:"message,omitempty"`
	Details []string `json:"details,omitempty" yaml:"details,omitempty"`
	// Evidence is what this run rests on. A run that could not look sets
	// EvidenceUnknown, and one that probed raises it to EvidenceMeasured.
	Evidence Evidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	// Fix is the magus command that remedies the finding, as argv without
	// "magus", nil when nothing mechanical applies. `doctor --fix` runs it.
	Fix []string `json:"fix,omitempty" yaml:"fix,omitempty"`
}

// Tier is how much of a change's gate magus can prove unnecessary, lowest
// first. A tier is only ever assigned on proof; a path magus cannot bound is
// RiskFull.
type Tier string

const (
	// RiskTrivial needs no gate: prose nothing reads, and generated output
	// whose generator the same change left untouched.
	RiskTrivial Tier = "trivial"
	// RiskMechanical changes no code semantics (comment-only, or code a
	// prover showed equivalent to its base), so only drift and lint run.
	RiskMechanical Tier = "mechanical"
	// RiskScoped changes packages a prover placed, so the tests narrow to
	// what can observe them.
	RiskScoped Tier = "scoped"
	// RiskFull runs the invoked target over every affected project.
	RiskFull Tier = "full"
)

// Rank orders tiers lowest first, trivial at 0. An empty or unknown tier ranks
// as full: a tier nobody proved bounds nothing.
func (t Tier) Rank() int {
	switch t {
	case RiskTrivial:
		return 0
	case RiskMechanical:
		return 1
	case RiskScoped:
		return 2
	default:
		return 3
	}
}

// TierEvidence is one reason behind a tier report. Path names a changed
// file, its class and the tier it earned. Without a Path, Project and
// Target name the gate step the reason is about.
type TierEvidence struct {
	Path    string `json:"path,omitempty"    yaml:"path,omitempty"`
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Target  string `json:"target,omitempty"  yaml:"target,omitempty"`
	Class   string `json:"class,omitempty"   yaml:"class,omitempty"`
	Tier    Tier   `json:"tier"              yaml:"tier"`
	Why     string `json:"why"               yaml:"why"`
	// Packages are the packages a scoped path put under test.
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
}

// Line renders the evidence as `<path>: <tier> (<class>: <why>)`. The gate
// step is its subject when it names no path, and it has no subject when it
// names neither.
func (e TierEvidence) Line() string {
	subject := e.Path
	if subject == "" {
		subject = strings.TrimSpace(fmt.Sprintf("%s %s", e.Project, e.Target))
	}
	why := e.Why
	if e.Class != "" {
		why = fmt.Sprintf("%s: %s", e.Class, why)
	}
	line := fmt.Sprintf("%s (%s)", string(e.Tier), why)
	if subject == "" {
		return line
	}
	return fmt.Sprintf("%s: %s", subject, line)
}
