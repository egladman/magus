package tier

import "github.com/egladman/magus/libs/safetyscissors/internal/enum"

func skillVariants() enum.Set[SkillVariant] {
	return enum.Set[SkillVariant]{SkillVariantShort, SkillVariantFull}
}

func (v SkillVariant) Values() []string { return skillVariants().Strings() }
func (v SkillVariant) Valid() bool      { return skillVariants().Valid(v) }
func (v SkillVariant) String() string   { return enum.String(v) }

// Full and Short let a skill body branch with {{if .Full}}: text/template cannot name a
// package constant, so the predicate hangs off the value being rendered. Short brackets
// text only the shorter reader sees, so it suits wording, never a step.
func (v SkillVariant) Full() bool  { return v == SkillVariantFull }
func (v SkillVariant) Short() bool { return v == SkillVariantShort }

func sources() enum.Set[Source] {
	return enum.Set[Source]{SourceJob, SourceMatrix, SourceUserMatrix, SourceDefault}
}

func (v Source) Values() []string { return sources().Strings() }
func (v Source) Valid() bool      { return sources().Valid(v) }
func (v Source) String() string   { return enum.String(v) }
