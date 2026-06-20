package types

import "time"

// Node is a single project node in a structured graph output.
type Node struct {
	Path        string   `json:"path" yaml:"path"`
	SpellName   string   `json:"spell_name,omitempty" yaml:"spell_name,omitempty"`
	Children    []string `json:"children" yaml:"children"`
	Dir         string   `json:"dir,omitempty" yaml:"dir,omitempty"`
	Exclusive   bool     `json:"exclusive,omitempty" yaml:"exclusive,omitempty"`
	BlastRadius int      `json:"blast_radius,omitempty" yaml:"blast_radius,omitempty"`
	DurationMs  int64    `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
	// Churn, Authors, and LastCommit are populated by the churn heatmap and omitted
	// by the plain dependency graph: how many recent commits touched the project,
	// how many distinct authors made them, and when the most recent one landed.
	Churn      int        `json:"churn,omitempty" yaml:"churn,omitempty"`
	Authors    int        `json:"authors,omitempty" yaml:"authors,omitempty"`
	LastCommit *time.Time `json:"last_commit,omitempty" yaml:"last_commit,omitempty"`
}

// GraphOutput is the full structured graph for JSON/YAML serialisation or
// rendering. Named to sit alongside the other *Output result types (e.g.
// StatusOutput, TargetGraphOutput) rather than the bare, ungrounded Output.
type GraphOutput struct {
	Direction string   `json:"direction" yaml:"direction"`
	SpellName string   `json:"spell_name,omitempty" yaml:"spell_name,omitempty"`
	Roots     []string `json:"roots,omitempty" yaml:"roots,omitempty"`
	Nodes     []Node   `json:"nodes" yaml:"nodes"`
}
