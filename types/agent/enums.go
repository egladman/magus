package agent

import "github.com/egladman/magus/types/enum"

var skillVariants = enum.Set[SkillVariant]{SkillVariantShort, SkillVariantFull}

func (v SkillVariant) Values() []string { return skillVariants.Strings() }
func (v SkillVariant) Valid() bool      { return skillVariants.Valid(v) }
func (v SkillVariant) String() string   { return enum.String(v) }

// Full and Short let a skill body branch with {{if .Full}}: text/template cannot name a
// package constant, so the predicate hangs off the value being rendered. Short brackets
// text only the shorter reader sees, so it suits wording, never a step.
func (v SkillVariant) Full() bool  { return v == SkillVariantFull }
func (v SkillVariant) Short() bool { return v == SkillVariantShort }

var modelTierSources = enum.Set[ModelTierSource]{ModelTierSourceRow, ModelTierSourceMatrix, ModelTierSourceUserMatrix, ModelTierSourceDefault}

func (v ModelTierSource) Values() []string { return modelTierSources.Strings() }
func (v ModelTierSource) Valid() bool      { return modelTierSources.Valid(v) }
func (v ModelTierSource) String() string   { return enum.String(v) }

var workerTypes = enum.Set[WorkerType]{WorkerTypeResumed, WorkerTypeNew}

func (v WorkerType) Values() []string { return workerTypes.Strings() }
func (v WorkerType) Valid() bool      { return workerTypes.Valid(v) }
func (v WorkerType) String() string   { return enum.String(v) }
