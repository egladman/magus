package types

import "time"

// Volatility is the per-(project, target) volatility lens: the run-outcome axis of
// insight. It is computed from the shared runtime-history file (a pure file read plus the
// Wilson-score compute, no shell-out and no workspace graph) and folded into the
// console Board (GET /api/v1/history-metrics) and the volatility metric. Threshold is the
// configured Wilson lower-bound above
// which a target is treated as volatile (Volatility.Threshold); a target's Volatile field
// is Score >= Threshold.
// ADR 0022: domain noun, not VolatilityReport.
type Volatility struct {
	Threshold float64            `json:"threshold" yaml:"threshold"`
	Targets   []VolatilityTarget `json:"targets"   yaml:"targets"`
}

// VolatilityTarget is one (project, target) pair's recorded volatility: the Wilson lower-bound
// Score against the threshold, the recent-outcome tallies, how many outcomes are retained,
// and the most recent passing (or volatile) run. Pass/Fail/VolatileCount count the retained window.
type VolatilityTarget struct {
	Project       string    `json:"project"             yaml:"project"`
	Target        string    `json:"target"              yaml:"target"`
	Score         float64   `json:"score"               yaml:"score"`
	Volatile      bool      `json:"volatile,omitempty"  yaml:"volatile,omitempty"`
	Pass          int       `json:"pass"                yaml:"pass"`
	Fail          int       `json:"fail"                yaml:"fail"`
	VolatileCount int       `json:"volatile_count"      yaml:"volatile_count"`
	Samples       int       `json:"samples"             yaml:"samples"`
	LastPass      time.Time `json:"last_pass,omitempty" yaml:"last_pass,omitempty"`
}
