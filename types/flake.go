package types

import "time"

// FlakeReport is the per-(project, target) flakiness read the console serves at
// GET /api/v1/flake. It is computed in-daemon from the shared runtime-history file: a
// pure file read plus the Wilson-score compute, no shell-out and no workspace graph.
// Threshold is the configured Wilson lower-bound above which a target is treated as
// flaky (Flake.Threshold); a target's Flaky field is Score >= Threshold.
type FlakeReport struct {
	Threshold float64       `json:"threshold" yaml:"threshold"`
	Targets   []FlakeTarget `json:"targets"   yaml:"targets"`
}

// FlakeTarget is one (project, target) pair's recorded flakiness: the Wilson lower-bound
// Score against the threshold, the recent-outcome tallies, how many outcomes are retained,
// and the most recent passing (or flaking) run. Pass/Fail/Flake count the retained window.
type FlakeTarget struct {
	Project  string    `json:"project"             yaml:"project"`
	Target   string    `json:"target"              yaml:"target"`
	Score    float64   `json:"score"               yaml:"score"`
	Flaky    bool      `json:"flaky,omitempty"     yaml:"flaky,omitempty"`
	Pass     int       `json:"pass"                yaml:"pass"`
	Fail     int       `json:"fail"                yaml:"fail"`
	Flake    int       `json:"flake"               yaml:"flake"`
	Samples  int       `json:"samples"             yaml:"samples"`
	LastPass time.Time `json:"last_pass,omitempty" yaml:"last_pass,omitempty"`
}
