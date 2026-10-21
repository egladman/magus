package types

import "github.com/egladman/magus/types/enum"

// The case lists of this package's closed string types, excluding each zero value.
// cmd/magus-utils's boundaryEnums names the same values for the Buzz mirror, and
// TestBoundaryEnumsMatchTheirSets holds the two to one list.

var signAlgorithms = enum.Set[SignAlgorithm]{"ed25519"}

func (v SignAlgorithm) Values() []string { return signAlgorithms.Strings() }
func (v SignAlgorithm) Valid() bool      { return signAlgorithms.Valid(v) }
func (v SignAlgorithm) String() string   { return enum.String(v) }

var termStyles = enum.Set[TermStyle]{"1", "2", "31", "32", "33", "2;32", "2;37", "1;32"}

func (v TermStyle) Values() []string { return termStyles.Strings() }
func (v TermStyle) Valid() bool      { return termStyles.Valid(v) }
func (v TermStyle) String() string   { return enum.String(v) }

var timeLayouts = enum.Set[TimeLayout]{
	"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.999999999Z07:00", "2006-01-02",
	"15:04:05", "2006-01-02 15:04:05", "Mon, 02 Jan 2006 15:04:05 MST", "3:04PM",
}

func (v TimeLayout) Values() []string { return timeLayouts.Strings() }
func (v TimeLayout) Valid() bool      { return timeLayouts.Valid(v) }
func (v TimeLayout) String() string   { return enum.String(v) }

var logLevels = enum.Set[LogLevel]{"trace", "debug", "info", "warn", "error"}

func (v LogLevel) Values() []string { return logLevels.Strings() }
func (v LogLevel) Valid() bool      { return logLevels.Valid(v) }
func (v LogLevel) String() string   { return enum.String(v) }

var platformStyles = enum.Set[PlatformStyle]{"go", "uname"}

func (v PlatformStyle) Values() []string { return platformStyles.Strings() }
func (v PlatformStyle) Valid() bool      { return platformStyles.Valid(v) }
func (v PlatformStyle) String() string   { return enum.String(v) }

var checkStatuses = enum.Set[CheckStatus]{"ok", "fail", "advice"}

func (v CheckStatus) Values() []string { return checkStatuses.Strings() }
func (v CheckStatus) Valid() bool      { return checkStatuses.Valid(v) }
func (v CheckStatus) String() string   { return enum.String(v) }

var eventOutcomes = enum.Set[EventOutcome]{"waiting", "permission", "failed", "finished", "diagnostic", "update", "other"}

func (v EventOutcome) Values() []string { return eventOutcomes.Strings() }
func (v EventOutcome) Valid() bool      { return eventOutcomes.Valid(v) }
func (v EventOutcome) String() string   { return enum.String(v) }

var eventSeverities = enum.Set[EventSeverity]{"info", "notice", "warning", "critical"}

func (v EventSeverity) Values() []string { return eventSeverities.Strings() }
func (v EventSeverity) Valid() bool      { return eventSeverities.Valid(v) }
func (v EventSeverity) String() string   { return enum.String(v) }

var serviceStates = enum.Set[ServiceState]{"starting", "running", "idle", "failed"}

func (v ServiceState) Values() []string { return serviceStates.Strings() }
func (v ServiceState) Valid() bool      { return serviceStates.Valid(v) }
func (v ServiceState) String() string   { return enum.String(v) }

var patternTypes = enum.Set[PatternType]{"glob", "regex", "literal"}

func (v PatternType) Values() []string { return patternTypes.Strings() }
func (v PatternType) Valid() bool      { return patternTypes.Valid(v) }
func (v PatternType) String() string   { return enum.String(v) }

var symbolIndexFreshnesses = enum.Set[SymbolIndexFreshness]{"up-to-date", "out-of-date", "not-indexed", "unvouched"}

func (v SymbolIndexFreshness) Values() []string { return symbolIndexFreshnesses.Strings() }
func (v SymbolIndexFreshness) Valid() bool      { return symbolIndexFreshnesses.Valid(v) }
func (v SymbolIndexFreshness) String() string   { return enum.String(v) }

var diffUncoveredReasons = enum.Set[DiffUncoveredReason]{"no-indexer"}

func (v DiffUncoveredReason) Values() []string { return diffUncoveredReasons.Strings() }
func (v DiffUncoveredReason) Valid() bool      { return diffUncoveredReasons.Valid(v) }
func (v DiffUncoveredReason) String() string   { return enum.String(v) }

var diffReadStates = enum.Set[DiffReadState]{"known", "unknown"}

func (v DiffReadState) Values() []string { return diffReadStates.Strings() }
func (v DiffReadState) Valid() bool      { return diffReadStates.Valid(v) }
func (v DiffReadState) String() string   { return enum.String(v) }

var targetRunStates = enum.Set[TargetRunState]{"queued", "running", "passed", "failed", "cached"}

func (v TargetRunState) Values() []string { return targetRunStates.Strings() }
func (v TargetRunState) Valid() bool      { return targetRunStates.Valid(v) }
func (v TargetRunState) String() string   { return enum.String(v) }

var vcsSources = enum.Set[VCSSource]{"explicit", "auto", "default", "disabled"}

func (v VCSSource) Values() []string { return vcsSources.Strings() }
func (v VCSSource) Valid() bool      { return vcsSources.Valid(v) }
func (v VCSSource) String() string   { return enum.String(v) }

var conflictKinds = enum.Set[ConflictKind]{"content", "deleted", "both-deleted"}

func (v ConflictKind) Values() []string { return conflictKinds.Strings() }
func (v ConflictKind) Valid() bool      { return conflictKinds.Valid(v) }
func (v ConflictKind) String() string   { return enum.String(v) }

var precedentFamilies = enum.Set[PrecedentFamily]{
	PrecedentDepDirection, PrecedentDepFanout, PrecedentErrSentinelName, PrecedentTestPackageName,
	PrecedentParamNameByType, PrecedentParamOrder, PrecedentCtxFirst, PrecedentErrorLast,
}

func (v PrecedentFamily) Values() []string { return precedentFamilies.Strings() }
func (v PrecedentFamily) Valid() bool      { return precedentFamilies.Valid(v) }
func (v PrecedentFamily) String() string   { return enum.String(v) }

var markerFamilies = enum.Set[MarkerFamily]{MarkerDiagram, MarkerCalls, MarkerSkills, MarkerObserved, MarkerHarness}

func (v MarkerFamily) Values() []string { return markerFamilies.Strings() }
func (v MarkerFamily) Valid() bool      { return markerFamilies.Valid(v) }
func (v MarkerFamily) String() string   { return enum.String(v) }

var markerVerbs = enum.Set[MarkerVerb]{MarkerPoint, MarkerBlock}

func (v MarkerVerb) Values() []string { return markerVerbs.Strings() }
func (v MarkerVerb) Valid() bool      { return markerVerbs.Valid(v) }
func (v MarkerVerb) String() string   { return enum.String(v) }

var knowledgeResolutions = enum.Set[KnowledgeResolution]{ResolvedID, ResolvedPath, ResolvedFuzzy}

func (v KnowledgeResolution) Values() []string { return knowledgeResolutions.Strings() }
func (v KnowledgeResolution) Valid() bool      { return knowledgeResolutions.Valid(v) }
func (v KnowledgeResolution) String() string   { return enum.String(v) }

// SandboxMode's String names the zero value as off rather than unset (see sandbox.go).
var sandboxModes = enum.Set[SandboxMode]{SandboxModeOff, SandboxModeBestEffort, SandboxModeRequired}

func (v SandboxMode) Values() []string { return sandboxModes.Strings() }
func (v SandboxMode) Valid() bool      { return sandboxModes.Valid(v) }

var skillVariants = enum.Set[SkillVariant]{SkillVariantShort, SkillVariantFull}

func (v SkillVariant) Values() []string { return skillVariants.Strings() }
func (v SkillVariant) Valid() bool      { return skillVariants.Valid(v) }
func (v SkillVariant) String() string   { return enum.String(v) }

// Full and Short let a skill body branch with {{if .Full}}: text/template cannot name a
// package constant, so the predicate hangs off the value being rendered. Short brackets
// text only the shorter reader sees, so it suits wording, never a step.
func (v SkillVariant) Full() bool  { return v == SkillVariantFull }
func (v SkillVariant) Short() bool { return v == SkillVariantShort }

var jobKinds = enum.Set[JobKind]{JobKindCheck, JobKindSymbol, JobKindPaths, JobKindScript, JobKindProse}

func (v JobKind) Values() []string { return jobKinds.Strings() }
func (v JobKind) Valid() bool      { return jobKinds.Valid(v) }
func (v JobKind) String() string   { return enum.String(v) }

var groupingShapes = enum.Set[GroupingShape]{GroupingChained, GroupingSplit, GroupingSolo}

func (v GroupingShape) Values() []string { return groupingShapes.Strings() }
func (v GroupingShape) Valid() bool      { return groupingShapes.Valid(v) }
func (v GroupingShape) String() string   { return enum.String(v) }

var groupingVerdicts = enum.Set[GroupingVerdict]{GroupingMeasured, GroupingUnknown}

func (v GroupingVerdict) Values() []string { return groupingVerdicts.Strings() }
func (v GroupingVerdict) Valid() bool      { return groupingVerdicts.Valid(v) }
func (v GroupingVerdict) String() string   { return enum.String(v) }

var briefFormats = enum.Set[BriefFormat]{BriefFormatMarkdown, BriefFormatBuzz}

func (v BriefFormat) Values() []string { return briefFormats.Strings() }
func (v BriefFormat) Valid() bool      { return briefFormats.Valid(v) }
func (v BriefFormat) String() string   { return enum.String(v) }

var briefRefKinds = enum.Set[BriefRefKind]{
	BriefRefOutput, BriefRefVerdict, BriefRefInvocation, BriefRefFile, BriefRefPlan, BriefRefCommit,
}

func (v BriefRefKind) Values() []string { return briefRefKinds.Strings() }
func (v BriefRefKind) Valid() bool      { return briefRefKinds.Valid(v) }
func (v BriefRefKind) String() string   { return enum.String(v) }

var modelTierSources = enum.Set[ModelTierSource]{ModelTierSourceRow, ModelTierSourceMatrix, ModelTierSourceDefault}

func (v ModelTierSource) Values() []string { return modelTierSources.Strings() }
func (v ModelTierSource) Valid() bool      { return modelTierSources.Valid(v) }
func (v ModelTierSource) String() string   { return enum.String(v) }

var conformanceMetrics = enum.Set[ConformanceMetric]{
	ConformanceFileLines, ConformanceFuncLines, ConformanceTypeMethods, ConformanceFileSymbols, ConformanceDepFanout,
	ConformanceCloneTokens, ConformanceExportScope, ConformanceCallSequence, ConformanceSharedBody, ConformanceSharedReach,
	ConformanceCallerSpread, ConformanceModeKnobs, ConformanceAliasShare, ConformanceNameClash,
}

func (v ConformanceMetric) Values() []string { return conformanceMetrics.Strings() }
func (v ConformanceMetric) Valid() bool      { return conformanceMetrics.Valid(v) }
func (v ConformanceMetric) String() string   { return enum.String(v) }

var conformancePopulations = enum.Set[ConformancePopulation]{ConformancePopSource, ConformancePopMain, ConformancePopTest}

func (v ConformancePopulation) Values() []string { return conformancePopulations.Strings() }
func (v ConformancePopulation) Valid() bool      { return conformancePopulations.Valid(v) }
func (v ConformancePopulation) String() string   { return enum.String(v) }

var conformanceSources = enum.Set[ConformanceSource]{ConformanceFromRatchet, ConformanceFromAnchor}

func (v ConformanceSource) Values() []string { return conformanceSources.Strings() }
func (v ConformanceSource) Valid() bool      { return conformanceSources.Valid(v) }
func (v ConformanceSource) String() string   { return enum.String(v) }
