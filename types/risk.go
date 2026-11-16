package types

import (
	"context"
	"strings"

	"github.com/egladman/magus/types/check"
)

// TierGateStep is one runnable command of a sized gate. Argv is the whole
// command line; the other fields are what it was built from.
type TierGateStep struct {
	Target   string   `json:"target"             yaml:"target"`
	Projects []string `json:"projects"           yaml:"projects"`
	// Op, when set, is the spell op (`go::go-test`) whose package arguments a sized run
	// narrows to Packages wherever Target's body invokes it. Argv runs the target whole,
	// since no command line can narrow an op inside a target body.
	Op       string   `json:"op,omitempty"       yaml:"op,omitempty"`
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
	Argv     []string `json:"argv"               yaml:"argv"`
}

// TierReport is a change's tier, the evidence for it, and Gate, the commands
// sufficient to validate it. An empty Gate pairs only with RiskTrivial.
type TierReport struct {
	// Base is the revision the change was measured from.
	Base     string               `json:"base"     yaml:"base"`
	Target   string               `json:"target"   yaml:"target"`
	Tier     check.Tier           `json:"tier"     yaml:"tier"`
	Affected []string             `json:"affected" yaml:"affected"`
	Evidence []check.TierEvidence `json:"evidence" yaml:"evidence"`
	Gate     []TierGateStep       `json:"gate"     yaml:"gate"`
}

// TierOptions are what a change is assessed against.
// They are magus\change().tier's options, and the workspace's RiskAssessor's.
type TierOptions struct {
	// Base is the revision the change is measured from; empty is the configured affected
	// base.
	Base string `json:"base,omitempty" buzz:"-"`
	// Paths replaces the version-control diff when non-nil: the change is these paths.
	Paths []string `json:"paths,omitempty"`
	// Checkout is the root of another checkout of the same repository whose change is
	// assessed, such as a job's standing checkout; empty is the workspace's own.
	Checkout string `json:"checkout,omitempty"`
}

// RiskAssessor is a workspace that proves a change's risk tier, recovered from the
// workspace on a context by assertion because the package that implements it imports the
// ones that need it.
type RiskAssessor interface {
	AssessRisk(ctx context.Context, target string, o TierOptions) (TierReport, error)
}

// Lines renders one line per evidence entry, every changed path included, because the
// reader of a sized or skipped gate must be able to reconstruct the decision.
func (r TierReport) Lines() []string {
	out := make([]string, len(r.Evidence))
	for i, e := range r.Evidence {
		out[i] = e.Line()
	}
	return out
}

// Commands renders the gate as the command lines a person runs, a narrowed step as
// the whole target it narrows.
func (r TierReport) Commands() []string {
	out := make([]string, len(r.Gate))
	for i, g := range r.Gate {
		out[i] = strings.Join(g.Argv, " ")
	}
	return out
}
