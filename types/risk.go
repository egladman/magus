package types

import "strings"

// RiskTier is how much of a change's gate magus can prove unnecessary, lowest first. A
// tier is only ever assigned on proof; a path magus cannot bound is RiskFull.
type RiskTier string

const (
	// RiskTrivial needs no gate: prose nothing reads, and generated output whose
	// generator the same change left untouched.
	RiskTrivial RiskTier = "trivial"
	// RiskMechanical changes no code semantics (comment-only, or code a prover showed
	// equivalent to its base), so only the drift check and lint run.
	RiskMechanical RiskTier = "mechanical"
	// RiskScoped changes packages a prover placed, so the tests narrow to what can
	// observe them.
	RiskScoped RiskTier = "scoped"
	// RiskFull runs the invoked target over every affected project.
	RiskFull RiskTier = "full"
)

// RiskEvidence is one reason behind a RiskReport. Path names a changed file, its class
// and the tier it earned; without a Path, Project and Target name the gate step the
// reason is about.
type RiskEvidence struct {
	Path    string   `json:"path,omitempty"    yaml:"path,omitempty"`
	Project string   `json:"project,omitempty" yaml:"project,omitempty"`
	Target  string   `json:"target,omitempty"  yaml:"target,omitempty"`
	Class   string   `json:"class,omitempty"   yaml:"class,omitempty"`
	Tier    RiskTier `json:"tier"              yaml:"tier"`
	Why     string   `json:"why"               yaml:"why"`
	// Packages are the packages a scoped path put under test.
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
}

// Line renders the evidence as `<path>: <tier> (<class>: <why>)`, with the gate step as
// its subject when it names no path, and no subject when it names neither.
func (e RiskEvidence) Line() string {
	subject := e.Path
	if subject == "" {
		subject = strings.TrimSpace(e.Project + " " + e.Target)
	}
	why := e.Why
	if e.Class != "" {
		why = e.Class + ": " + why
	}
	line := string(e.Tier) + " (" + why + ")"
	if subject == "" {
		return line
	}
	return subject + ": " + line
}

// RiskGateStep is one runnable command of a sized gate. Argv is the whole command line;
// the other fields are what it was built from.
type RiskGateStep struct {
	Target   string   `json:"target"             yaml:"target"`
	Projects []string `json:"projects"           yaml:"projects"`
	// Op, when set, is the spell op (`go::go-test`) whose package arguments a sized run
	// narrows to Packages wherever Target's body invokes it. Argv runs the target whole,
	// since no command line can narrow an op inside a target body.
	Op       string   `json:"op,omitempty"       yaml:"op,omitempty"`
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
	Argv     []string `json:"argv"               yaml:"argv"`
}

// RiskReport is a change's tier, the evidence for it, and Gate, the commands sufficient
// to validate it. An empty Gate is only ever paired with RiskTrivial.
type RiskReport struct {
	// Base is the revision the change was measured from.
	Base     string         `json:"base"     yaml:"base"`
	Target   string         `json:"target"   yaml:"target"`
	Tier     RiskTier       `json:"tier"     yaml:"tier"`
	Affected []string       `json:"affected" yaml:"affected"`
	Evidence []RiskEvidence `json:"evidence" yaml:"evidence"`
	Gate     []RiskGateStep `json:"gate"     yaml:"gate"`
}

// Lines renders one line per evidence entry, every changed path included, because the
// reader of a sized or skipped gate must be able to reconstruct the decision.
func (r RiskReport) Lines() []string {
	out := make([]string, len(r.Evidence))
	for i, e := range r.Evidence {
		out[i] = e.Line()
	}
	return out
}

// Commands renders the gate as the command lines a person runs, a narrowed step as
// the whole target it narrows.
func (r RiskReport) Commands() []string {
	out := make([]string, len(r.Gate))
	for i, g := range r.Gate {
		out[i] = strings.Join(g.Argv, " ")
	}
	return out
}
