package tier

import "fmt"

// SkillVariant is which body of a magus skill a reader loads. Both come from one
// curated source, so they share a content digest and go stale together.
type SkillVariant string

const (
	// SkillVariantShort sheds enumeration and keeps judgment: a capable reader can
	// re-derive the mechanical steps, not which failures are silent or load-bearing.
	SkillVariantShort SkillVariant = "short"
	// SkillVariantFull spells out every mechanical step and why each is the right move.
	SkillVariantFull SkillVariant = "full"
)

// Source says which declaration chose a job's model tier.
type Source string

const (
	// SourceJob is a tier or model the job named.
	SourceJob Source = "job"
	// SourceMatrix is the workspace's registered matrix function's answer.
	SourceMatrix Source = "matrix"
	// SourceUserMatrix is the person's own matrix module's answer.
	SourceUserMatrix Source = "user-matrix"
	// SourceDefault is the declaration's default.
	SourceDefault Source = "default"
)

// Assignment is the tier recorded on a job when it was assigned.
// It holds the case name and rank, the skill variant its worker loads,
// which declaration chose it and why, and that declaration's file:line.
// Model is the alias the host spawns.
type Assignment struct {
	Name   string       `json:"name"             yaml:"name"`
	Rank   int          `json:"rank"             yaml:"rank"`
	Skills SkillVariant `json:"skills"           yaml:"skills"`
	Source Source       `json:"source"           yaml:"source"`
	Reason string       `json:"reason,omitempty" yaml:"reason,omitempty"`
	Site   string       `json:"site,omitempty"   yaml:"site,omitempty"`
	Model  string       `json:"model,omitempty"  yaml:"model,omitempty"`
	// Shared is [Set.SharedAliases] for the tier when it was assigned.
	Shared []string `json:"shared,omitempty" yaml:"shared,omitempty"`
}

// String renders the assignment as `fork` and `describe job` print it: the name and rank,
// the source that chose it and, where the source gave them, its reason and site.
// Each Shared line follows.
func (t Assignment) String() string {
	line := fmt.Sprintf("%s (rank %d), %s", t.Name, t.Rank, t.Source)
	if t.Reason != "" {
		line += fmt.Sprintf(": %q", t.Reason)
	}
	if t.Site != "" {
		line += " at " + t.Site
	}
	for _, s := range t.Shared {
		line += "; " + s
	}
	return line
}
