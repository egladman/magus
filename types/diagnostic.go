package types

import (
	"context"
	"strings"

	"github.com/egladman/magus/libs/diagnostics"
)

// magus's diagnostic codes are the MGS#### family. The MECHANISM (the Code/Error types, the rendering, the
// errors.Is matching, the run-time sink) lives in the shared github.com/egladman/magus/libs/diagnostics framework; this file
// is magus's INSTANTIATION of it - the MGS docs-URL layout, the MGS catalog, and thin re-exports so the
// ~20 in-tree consumers keep using types.DiagnosticCode / DiagnosticError / DiagnosticErrorf unchanged.
// gopherbuzz instantiates the same framework separately for its own BZZ#### codes; the two namespaces
// never share a code.

// Diagnostic codes (MGS####): 1000=magusfile authoring, 2000=sandbox, 3000=workspace-scope, 4000=race detection, 5000=services, 6000=charms, 7000=knowledge-graph extraction, 8000=output references, 9000=auth/connector.

// Base URLs for diagnostic documentation, keyed by code-prefix subdir.
//
// These point at the rendered docs SITE, not at the Markdown source on the repo host.
// A code in a terminal is something a reader clicks while stuck, so it should land on a
// styled page with its navigation, search, and cross-links intact - not on a raw file
// view. It also means the URL survives the source moving, since the site keeps a
// redirect for a page that relocates and the blob URL would simply 404.
//
// No ".md": the site serves each code as a directory URL.
const (
	diagnosticSandboxBase   = "https://eli.gladman.cc/magus/reference/codes/sandbox/"
	diagnosticRaceBase      = "https://eli.gladman.cc/magus/reference/codes/race/"
	diagnosticMagusfileBase = "https://eli.gladman.cc/magus/reference/codes/magusfile/"
	diagnosticServicesBase  = "https://eli.gladman.cc/magus/reference/codes/services/"
	diagnosticCharmsBase    = "https://eli.gladman.cc/magus/reference/codes/charms/"
	diagnosticKnowledgeBase = "https://eli.gladman.cc/magus/reference/codes/knowledge/"
	diagnosticOutputRefBase = "https://eli.gladman.cc/magus/reference/codes/outputref/"
	diagnosticAuthBase      = "https://eli.gladman.cc/magus/reference/codes/auth/"
)

// DiagnosticCode identifies a stable diagnostic (MGS#### code). It aliases the framework's Code type, so
// every consumer keeps referring to types.DiagnosticCode while the machinery is shared.
type DiagnosticCode = diagnostics.Code

// DiagnosticError is a typed error carrying an MGS code and message (the framework's Error). It implements
// error, and a DiagnosticCode is itself an errors.Is sentinel, so a caller matches one idiomatically:
// errors.Is(err, types.ExecDenied).
type DiagnosticError = diagnostics.Error

// DiagnosticEvent is one diagnostic fired during a run (the framework's Event).
type DiagnosticEvent = diagnostics.Event

// DiagnosticSink records diagnostics fired during a run (the framework's Sink).
type DiagnosticSink = diagnostics.Sink

// ErrDiag is a sentinel for use with errors.Is on DiagnosticError values.
var ErrDiag = diagnostics.ErrSentinel

// mgs is the magus diagnostic domain: it maps an MGS code to its docs page by prefix range. Every magus
// coded error is minted through it so the docs URL is captured for rendering.
var mgs = diagnostics.New(func(c DiagnosticCode) string {
	switch {
	case strings.HasPrefix(string(c), "MGS9"):
		return diagnosticAuthBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS8"):
		return diagnosticOutputRefBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS7"):
		return diagnosticKnowledgeBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS6"):
		return diagnosticCharmsBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS5"):
		return diagnosticServicesBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS4"):
		return diagnosticRaceBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS1"):
		return diagnosticMagusfileBase + string(c) + "/"
	default:
		return diagnosticSandboxBase + string(c) + "/"
	}
})

// CodeURL returns the documentation URL for an MGS code. (URL resolution is domain-specific, so it is a
// function on the magus domain rather than a method on the shared Code type.)
func CodeURL(c DiagnosticCode) string { return mgs.URL(c) }

const (
	NoCITarget               DiagnosticCode = "MGS1001"
	SpellShadowed            DiagnosticCode = "MGS1002"
	BespokePhaseFragmentName DiagnosticCode = "MGS1003"
	UnreachedFootprintDecl   DiagnosticCode = "MGS1004"
	RedundantFootprintGlob   DiagnosticCode = "MGS1005"
	UnknownTarget            DiagnosticCode = "MGS1006"
	TargetDependencyCycle    DiagnosticCode = "MGS1007"
	TargetMissingContext     DiagnosticCode = "MGS1008"
	TargetNeverReplays       DiagnosticCode = "MGS1009"
	AffectedSetUncomputable  DiagnosticCode = "MGS1010"
	CrossOutputOwnerUnknown  DiagnosticCode = "MGS1011"
	CrossOutputCycle         DiagnosticCode = "MGS1012"
	CrossOutputGlobEscapes   DiagnosticCode = "MGS1013"
	CrossOutputNotProduced   DiagnosticCode = "MGS1014"
	CrossDepOwnerUnknown     DiagnosticCode = "MGS1015"
	GoModReplaceDrift        DiagnosticCode = "MGS1016"
	MagusfileIsNotASpell     DiagnosticCode = "MGS1017"
	DeadOutputGlob           DiagnosticCode = "MGS1018"
	SelfStalingOutput        DiagnosticCode = "MGS1019"
	OutputOwnedByTwoTargets  DiagnosticCode = "MGS1020"
	WorkspaceNeedsNewerMagus DiagnosticCode = "MGS1021"
	MagusfileOnlyMember      DiagnosticCode = "MGS1022"
	ProviderPathRejected     DiagnosticCode = "MGS1023"
	ProviderProjectShadowed  DiagnosticCode = "MGS1024"
	MagusfileAPIRemoved      DiagnosticCode = "MGS1025"
	CacheableSecretRead      DiagnosticCode = "MGS1026"
	// SecretGrantInvalid covers every way a secret grant is unusable: a missing field, a
	// wildcard or non-ASCII host, a header that is not a legal field name. ONE code
	// rather than one per rule, because the resolution is the same in every case - fix
	// the declaration the error names - and a caller branching on it wants "this grant
	// is malformed", not which clause caught it. The message carries the specifics.
	SecretGrantInvalid DiagnosticCode = "MGS1027"
	// UndeclaredSeedingFile is a changed file no project declares that still pulled a
	// project into the affected set through directory containment. It reruns targets
	// while moving no cache key, so the work is real and its result was already
	// correct - the expensive half of an under-declaration, with the silent half
	// (nothing reruns when the file DOES matter) waiting behind it.
	UndeclaredSeedingFile     DiagnosticCode = "MGS1028"
	PathReadDenied            DiagnosticCode = "MGS2001"
	PathWriteDenied           DiagnosticCode = "MGS2002"
	EnvStripped               DiagnosticCode = "MGS2003"
	AllowlistUnresolved       DiagnosticCode = "MGS2004"
	SandboxUnsupported        DiagnosticCode = "MGS2005"
	PathShimSuspected         DiagnosticCode = "MGS2006"
	ExecDenied                DiagnosticCode = "MGS2007"
	DaemonSocketWithheld      DiagnosticCode = "MGS2008"
	SandboxPolicyMismatch     DiagnosticCode = "MGS2010"
	SecretTooShortToMask      DiagnosticCode = "MGS2011"
	DescendantBoundaryCrossed DiagnosticCode = "MGS3001"
	VCSUnavailable            DiagnosticCode = "MGS3002"
	ToolNotOnPath             DiagnosticCode = "MGS3003"
	// ToolNotReady is ToolNotOnPath one level deeper: the binary IS present, but the
	// service it talks to is not reachable. Same category - the environment, not the
	// code - so it sits beside it rather than in a family of its own.
	ToolNotReady DiagnosticCode = "MGS3004"
	// ToolTooOld is the fourth question about a tool: it exists, it reports a version,
	// it is usable - and that version is below the declared minimum.
	ToolTooOld DiagnosticCode = "MGS3005"
	// ToolTooNew is ToolTooOld's other side: the version is at or above a ceiling the
	// spell or the workspace excludes. A separate code rather than a shared "version
	// rejected" because the remediation is the opposite one, and because folding both
	// into MGS3005 is exactly the defect this pair replaced - a too-new binary being
	// told it was too old.
	ToolTooNew DiagnosticCode = "MGS3006"
	// ProjectLockHeldByAncestor is a magus run that cannot proceed because a project it
	// must lock is already locked by one of its OWN ancestor invocations, which cannot
	// release it until this run exits. It sits in the environment family beside the tool
	// codes: nothing in the workspace is wrong, the process context the run was started in
	// makes it impossible. Named for the condition it detects, not for a "re-entrant lock"
	// magus does not offer.
	ProjectLockHeldByAncestor DiagnosticCode = "MGS3007"
	RaceDetected              DiagnosticCode = "MGS4001"
	OutputOverlapDetected     DiagnosticCode = "MGS4002"
	NondeterministicOutput    DiagnosticCode = "MGS4003"
	MissingDependencyDetected DiagnosticCode = "MGS4004"
	EnvironmentalDrift        DiagnosticCode = "MGS4005"
	StaleGeneratedOutput      DiagnosticCode = "MGS4006"
	NearDuplicateServices     DiagnosticCode = "MGS5001"
	ServiceOpDetached         DiagnosticCode = "MGS5002"
	CommandOpNeverExits       DiagnosticCode = "MGS5003"
	DaemonRequired            DiagnosticCode = "MGS5004"
	CharmPatchInvalid         DiagnosticCode = "MGS6001"
	UnresolvableBuzzImport    DiagnosticCode = "MGS7001"
	DanglingDocReference      DiagnosticCode = "MGS7002"
	OutputRefMissing          DiagnosticCode = "MGS8001"
	OutputRefAmbiguous        DiagnosticCode = "MGS8002"
	OutputRefMalformed        DiagnosticCode = "MGS8003"
	OutputRefForeignMachine   DiagnosticCode = "MGS8004"
	BearerRejected            DiagnosticCode = "MGS9001"
	InsecureTokenPermissions  DiagnosticCode = "MGS9002"
	ConnectorStoreTooNew      DiagnosticCode = "MGS9003"
	NoAuthToken               DiagnosticCode = "MGS9004"
	ConnectorNameExists       DiagnosticCode = "MGS9005"
	ConnectorNotFound         DiagnosticCode = "MGS9006"
)

// allDiagnosticCodes lists every registered code in ascending MGS order. Keep it
// in sync with the const block above; it is the enumeration source for tooling
// (the knowledge graph turns each into a diagnostic node) since Go const blocks
// are not reflectable.
var allDiagnosticCodes = []DiagnosticCode{
	NoCITarget, SpellShadowed, BespokePhaseFragmentName,
	UnreachedFootprintDecl, RedundantFootprintGlob, UnknownTarget, TargetDependencyCycle,
	TargetMissingContext, TargetNeverReplays, AffectedSetUncomputable,
	CrossOutputOwnerUnknown, CrossOutputCycle, CrossOutputGlobEscapes, CrossOutputNotProduced,
	CrossDepOwnerUnknown, GoModReplaceDrift, MagusfileIsNotASpell, DeadOutputGlob,
	SelfStalingOutput, OutputOwnedByTwoTargets, WorkspaceNeedsNewerMagus,
	MagusfileOnlyMember, ProviderPathRejected, ProviderProjectShadowed,
	MagusfileAPIRemoved, CacheableSecretRead, SecretGrantInvalid, UndeclaredSeedingFile,
	PathReadDenied, PathWriteDenied, EnvStripped, AllowlistUnresolved,
	SandboxUnsupported, PathShimSuspected, ExecDenied, DaemonSocketWithheld,
	SandboxPolicyMismatch, SecretTooShortToMask,
	DescendantBoundaryCrossed, VCSUnavailable, ToolNotOnPath, ToolNotReady, ToolTooOld, ToolTooNew,
	ProjectLockHeldByAncestor,
	RaceDetected, OutputOverlapDetected, NondeterministicOutput, MissingDependencyDetected,
	EnvironmentalDrift, StaleGeneratedOutput,
	NearDuplicateServices, ServiceOpDetached, CommandOpNeverExits, DaemonRequired,
	CharmPatchInvalid,
	UnresolvableBuzzImport, DanglingDocReference,
	OutputRefMissing, OutputRefAmbiguous, OutputRefMalformed, OutputRefForeignMachine,
	BearerRejected, InsecureTokenPermissions, ConnectorStoreTooNew,
	NoAuthToken, ConnectorNameExists, ConnectorNotFound,
}

// AllDiagnosticCodes returns every registered diagnostic code in ascending MGS
// order. The returned slice is a copy; callers may mutate it freely.
func AllDiagnosticCodes() []DiagnosticCode {
	out := make([]DiagnosticCode, len(allDiagnosticCodes))
	copy(out, allDiagnosticCodes)
	return out
}

// DiagnosticErrorf builds a DiagnosticError with an MGS code and formatted message, capturing the code's
// docs URL for rendering.
func DiagnosticErrorf(c DiagnosticCode, format string, args ...any) *DiagnosticError {
	return mgs.Errorf(c, format, args...)
}

// FormatDiagnostic formats a diagnostic message with code and doc URL for slog logging.
func FormatDiagnostic(c DiagnosticCode, msg string) string {
	return mgs.Format(c, msg)
}

// WrapDiagnostic builds a DiagnosticError that carries an MGS code AND wraps cause, so errors.Is(err,
// cause) keeps matching while the error gains a lookupable code. Use it when a sentinel already drives
// control flow (e.g. ErrUnknownTarget) and must keep matching.
func WrapDiagnostic(c DiagnosticCode, cause error, format string, args ...any) *DiagnosticError {
	return mgs.Wrapf(c, cause, format, args...)
}

// WithDiagnosticSink returns ctx carrying s, so a deep emission site can reach the
// sink without threading it through every signature.
func WithDiagnosticSink(ctx context.Context, s DiagnosticSink) context.Context {
	return diagnostics.WithSink(ctx, s)
}

// EmitDiagnostic records ev to the sink in ctx, or is a no-op when none is
// installed (the common CLI path).
func EmitDiagnostic(ctx context.Context, ev DiagnosticEvent) {
	diagnostics.Emit(ctx, ev)
}

// Diagnostic is the shape a coded failure takes when it crosses into Buzz: the fields
// diagnostics.Error.BuzzError already produces, declared as a type rather than left as an
// undeclared map convention.
//
// It exists because `catch` hands back an untyped value - Buzz has no union types, and a
// throw can carry a str, an int, or this - so the caller narrows it at the boundary the
// way errors.As does in Go:
//
//	catch (e) {
//	    final d: Diagnostic = e;
//	    if (d.code == "MGS2001") { ... }
//	}
//
// Url is omitted when the domain captured none, so a caller testing it is asking "did this
// code come with docs", not reading an empty string that might mean either.
type Diagnostic struct {
	Code    string `json:"code" yaml:"code"`
	Message string `json:"message" yaml:"message"`
	// The buzz tag pins the Buzz name explicitly. LowerFirstWord would derive `url` from
	// URL anyway, but the tag is what a reader and a rename both see - the mirror name is
	// part of this type's contract, not a side effect of a casing rule.
	URL string `json:"url,omitempty" yaml:"url,omitempty" buzz:"url"`
}
