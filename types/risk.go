package types

// RiskTier is how much of a change's gate magus can prove unnecessary, lowest first.
// A tier is only ever assigned on proof; a file magus cannot bound lands in RiskFull.
type RiskTier string

const (
	// RiskTrivial needs no gate: prose, and generated output whose generator the
	// same change left untouched.
	RiskTrivial RiskTier = "trivial"
	// RiskMechanical changes no code semantics (comment-only, or a Go file whose
	// comment-free AST equals the base's), so only lint and the drift check run.
	RiskMechanical RiskTier = "mechanical"
	// RiskScoped changes Go packages whose reverse-dependency closure is known, so
	// the Go tests narrow to that closure.
	RiskScoped RiskTier = "scoped"
	// RiskFull runs the invoked target over every affected project.
	RiskFull RiskTier = "full"
)

// RiskEvidence is one reason behind a RiskReport. Path names a changed file and
// the tier it earned; Target (with Project) names a gate step statistical pruning
// dropped, with the numbers in Why.
type RiskEvidence struct {
	Path    string   `json:"path,omitempty"    yaml:"path,omitempty"`
	Project string   `json:"project,omitempty" yaml:"project,omitempty"`
	Target  string   `json:"target,omitempty"  yaml:"target,omitempty"`
	Tier    RiskTier `json:"tier"              yaml:"tier"`
	Why     string   `json:"why"               yaml:"why"`
	// Packages are the Go import paths a scoped file put under test.
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
}

// RiskGateStep is one runnable command of a reduced gate. Argv is the whole
// command line; the other fields are what it was built from.
type RiskGateStep struct {
	Target   string   `json:"target"             yaml:"target"`
	Projects []string `json:"projects"           yaml:"projects"`
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
	Argv     []string `json:"argv"               yaml:"argv"`
}

// TargetRisk is one (project, target) pair's recorded failure rate over the runs
// the history kept for it inside the configured window. Rate is Failures/Runs, 0
// when Runs is 0; a volatile outcome counts as a failure. Pruned is true when the
// pair has at least the configured minimum of runs and no failure among them.
type TargetRisk struct {
	Project  string  `json:"project"  yaml:"project"`
	Target   string  `json:"target"   yaml:"target"`
	Rate     float64 `json:"rate"     yaml:"rate"`
	Runs     int     `json:"runs"     yaml:"runs"`
	Failures int     `json:"failures" yaml:"failures"`
	Window   string  `json:"window"   yaml:"window"`
	Pruned   bool    `json:"pruned"   yaml:"pruned"`
}

// RiskReport is `magus affected <target> --risk`: the change's tier, the evidence
// for it, and Gate, the commands that are sufficient to validate it. An empty Gate
// is only ever paired with RiskTrivial.
type RiskReport struct {
	// Base is the revision the diff and every base-side read were taken against,
	// "paths" when the changed set came from --stdin with no --base.
	Base     string         `json:"base"               yaml:"base"`
	Target   string         `json:"target"             yaml:"target"`
	Tier     RiskTier       `json:"tier"               yaml:"tier"`
	Affected []string       `json:"affected"           yaml:"affected"`
	Evidence []RiskEvidence `json:"evidence"           yaml:"evidence"`
	Gate     []RiskGateStep `json:"gate"               yaml:"gate"`
	Risk     []TargetRisk   `json:"risk,omitempty"     yaml:"risk,omitempty"`
}
