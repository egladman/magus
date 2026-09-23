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

var symbolIndexFreshnesses = enum.Set[SymbolIndexFreshness]{"up-to-date", "out-of-date", "not-indexed"}

func (v SymbolIndexFreshness) Values() []string { return symbolIndexFreshnesses.Strings() }
func (v SymbolIndexFreshness) Valid() bool      { return symbolIndexFreshnesses.Valid(v) }
func (v SymbolIndexFreshness) String() string   { return enum.String(v) }

var diffUncoveredReasons = enum.Set[DiffUncoveredReason]{"no-indexer"}

func (v DiffUncoveredReason) Values() []string { return diffUncoveredReasons.Strings() }
func (v DiffUncoveredReason) Valid() bool      { return diffUncoveredReasons.Valid(v) }
func (v DiffUncoveredReason) String() string   { return enum.String(v) }

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
