package types

// ChangeRiskScore is complexity^2 * (1-coverage)^3 + complexity, with
// coverage a ratio in [0,1]. A fully covered function scores its complexity.
func ChangeRiskScore(complexity int, coverage float64) float64 {
	c := float64(complexity)
	miss := 1 - coverage
	return c*c*miss*miss*miss + c
}

// FunctionGap is a touched function magus could not judge, and why. It is
// reported, never dropped: a function that cannot be scored has not passed.
type FunctionGap struct {
	Symbol   string
	Label    string
	Path     string
	Language string
	Line     int
	// Complexity is decision points plus one, or 0 when it is unknown.
	Complexity int
	// Reason says what is missing, such as "spell bash declares no coverage".
	Reason string
}

// ChangeRiskFunction is one function's measured score.
type ChangeRiskFunction struct {
	Symbol   string
	Label    string
	Path     string
	Language string
	// Digest is the symbol's BodyDigest: the text that was scored.
	Digest     string
	Line       int
	EndLine    int
	Complexity int
	Covered    int
	Coverable  int
	Score      float64
	// The Base fields measure the same function in the tree base forked from.
	// BaseReason is "" exactly when they hold a measure.
	BaseScore      float64
	BaseComplexity int
	BaseCovered    int
	BaseCoverable  int
	BaseReason     string
}

// Coverage is Covered over Coverable, or 0 when no line is coverable. Such a
// function has nothing a test could reach; the caller reports it as a gap.
func (f ChangeRiskFunction) Coverage() float64 {
	if f.Coverable == 0 {
		return 0
	}
	return float64(f.Covered) / float64(f.Coverable)
}

// ChangeRisk is the change risk answer for a change: what it scored and what
// it could not.
type ChangeRisk struct {
	// Definition names the formula so a reader need not know it.
	Definition string
	// Base is the revision the touched set was taken against, or "" when
	// every function in the workspace was scored.
	Base      string
	Functions []ChangeRiskFunction
	Gaps      []FunctionGap
	// Lines are coverage findings in the changed files: a kept line that lost
	// coverage, an added line no test covers, or a file not compared.
	Lines []Finding
}
