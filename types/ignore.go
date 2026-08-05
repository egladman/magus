package types

// PatternType identifies the matching strategy for an [IgnorePattern]. Glob is the
// default; regex and literal are escape hatches for cases globs cannot express.
type PatternType string

const (
	PatternGlob    PatternType = "glob"    // doublestar `**`-aware glob; default for bare CLI values
	PatternRegex   PatternType = "regex"   // Go regexp; for rules globs cannot express
	PatternLiteral PatternType = "literal" // matches any path segment at any depth (like .gitignore bare entry)
)

// IgnorePattern is one watch ignore rule ("glob", "regex", or "literal").
type IgnorePattern struct {
	Type    PatternType `json:"type" yaml:"type" validate:"required,oneof=glob regex literal"`
	Pattern string      `json:"pattern" yaml:"pattern" validate:"required"`
}
