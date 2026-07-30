package types

// PatternType identifies the matching strategy for an [IgnorePattern].
type PatternType string

// IgnorePattern is one watch ignore rule ("glob", "regex", or "literal").
type IgnorePattern struct {
	Type    PatternType `json:"type" yaml:"type" validate:"required,oneof=glob regex literal"`
	Pattern string      `json:"pattern" yaml:"pattern" validate:"required"`
}
