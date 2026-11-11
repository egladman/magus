package types

import (
	"slices"

	"github.com/egladman/magus/types/enum"
)

// The case lists of this package's closed string types, excluding each zero value.
// buzzgen derives the Buzz mirror's cases from the constants, and
// TestBoundaryEnumsMatchTheirSets holds each set equal to them.

var diagnosticOrigins = enum.Set[DiagnosticOrigin]{"builtin", "workspace"}

func (v DiagnosticOrigin) Values() []string { return diagnosticOrigins.Strings() }
func (v DiagnosticOrigin) Valid() bool      { return diagnosticOrigins.Valid(v) }
func (v DiagnosticOrigin) String() string   { return enum.String(v) }

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

var patternTypes = enum.Set[PatternType]{"glob", "regex", "literal"}

func (v PatternType) Values() []string { return patternTypes.Strings() }
func (v PatternType) Valid() bool      { return patternTypes.Valid(v) }
func (v PatternType) String() string   { return enum.String(v) }

var symbolIndexFreshnesses = enum.Set[SymbolIndexFreshness]{"up-to-date", "out-of-date", "not-indexed", "unvouched"}

func (v SymbolIndexFreshness) Values() []string { return symbolIndexFreshnesses.Strings() }
func (v SymbolIndexFreshness) Valid() bool      { return symbolIndexFreshnesses.Valid(v) }
func (v SymbolIndexFreshness) String() string   { return enum.String(v) }

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

var skillForms = enum.Set[SkillForm]{SkillFormBoth, SkillFormShort, SkillFormFull}

func (v SkillForm) Values() []string { return skillForms.Strings() }
func (v SkillForm) Valid() bool      { return skillForms.Valid(v) }
func (v SkillForm) String() string   { return enum.String(v) }

// toolCapabilities orders the files of a tool rows directory, one per capability.
var toolCapabilities = enum.Set[ToolCapability]{ToolRuns, ToolReads, ToolWrites, ToolSpawns, ToolNetwork, ToolInert}

func (v ToolCapability) Values() []string { return toolCapabilities.Strings() }
func (v ToolCapability) Valid() bool      { return toolCapabilities.Valid(v) }
func (v ToolCapability) String() string   { return enum.String(v) }

// ToolCapabilities is every capability, each the name of one file in a tool rows directory.
func ToolCapabilities() []ToolCapability { return slices.Clone(toolCapabilities) }

var hookPoints = enum.Set[HookPoint]{
	HookPointShell, HookPointEdit, HookPointStop, HookPointSubagentStop, HookPointSpawn,
	HookPointSessionStart, HookPointCompact, HookPointMCP, HookPointAttach,
}

func (v HookPoint) Values() []string { return hookPoints.Strings() }
func (v HookPoint) Valid() bool      { return hookPoints.Valid(v) }
func (v HookPoint) String() string   { return enum.String(v) }

var bindingStatuses = enum.Set[BindingStatus]{Bound, Unsupported}

func (v BindingStatus) Values() []string { return bindingStatuses.Strings() }
func (v BindingStatus) Valid() bool      { return bindingStatuses.Valid(v) }
func (v BindingStatus) String() string   { return enum.String(v) }

var mcpTransports = enum.Set[McpTransport]{McpStdio, McpHTTP}

func (v McpTransport) Values() []string { return mcpTransports.Strings() }
func (v McpTransport) Valid() bool      { return mcpTransports.Valid(v) }
func (v McpTransport) String() string   { return enum.String(v) }

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

var jobEvidenceKinds = enum.Set[JobEvidenceKind]{
	JobEvidenceOutput, JobEvidenceVerdict, JobEvidenceInvocation, JobEvidenceFile, JobEvidencePlan, JobEvidenceCommit,
}

func (v JobEvidenceKind) Values() []string { return jobEvidenceKinds.Strings() }
func (v JobEvidenceKind) Valid() bool      { return jobEvidenceKinds.Valid(v) }
func (v JobEvidenceKind) String() string   { return enum.String(v) }

var modelTierSources = enum.Set[ModelTierSource]{ModelTierSourceRow, ModelTierSourceMatrix, ModelTierSourceUserMatrix, ModelTierSourceDefault}

func (v ModelTierSource) Values() []string { return modelTierSources.Strings() }
func (v ModelTierSource) Valid() bool      { return modelTierSources.Valid(v) }
func (v ModelTierSource) String() string   { return enum.String(v) }

var claimKinds = enum.Set[ClaimKind]{ClaimRefs, ClaimTest, ClaimOutput, ClaimReleased, ClaimDeadHolder}

func (v ClaimKind) Values() []string { return claimKinds.Strings() }
func (v ClaimKind) Valid() bool      { return claimKinds.Valid(v) }
func (v ClaimKind) String() string   { return enum.String(v) }

var conformanceMetrics = enum.Set[ConformanceMetric]{
	ConformanceFileLines, ConformanceFuncLines, ConformanceTypeMethods, ConformanceFileSymbols, ConformanceDepFanout,
	ConformanceCloneTokens, ConformanceExportScope, ConformanceCallSequence, ConformanceSharedBody, ConformanceSharedReach,
	ConformanceCallerSpread, ConformanceModeKnobs, ConformanceAliasShare, ConformanceNameClash,
	ConformanceMainLogic, ConformanceStorePathExport, ConformanceFileSplit,
}

func (v ConformanceMetric) Values() []string { return conformanceMetrics.Strings() }
func (v ConformanceMetric) Valid() bool      { return conformanceMetrics.Valid(v) }
func (v ConformanceMetric) String() string   { return enum.String(v) }

var conformancePopulations = enum.Set[ConformancePopulation]{ConformancePopSource, ConformancePopMain, ConformancePopTest}

func (v ConformancePopulation) Values() []string { return conformancePopulations.Strings() }
func (v ConformancePopulation) Valid() bool      { return conformancePopulations.Valid(v) }
func (v ConformancePopulation) String() string   { return enum.String(v) }
