package types

import (
	"context"
	"strings"

	"github.com/egladman/magus/libs/diag"
)

// magus's diagnostic codes are the MGS#### family. The MECHANISM (the Code/Error types, the rendering, the
// errors.Is matching, the run-time sink) lives in the shared github.com/egladman/magus/libs/diag framework; this file
// is magus's INSTANTIATION of it - the MGS docs-URL layout, the MGS catalog, and thin re-exports so the
// ~20 in-tree consumers keep using types.DiagnosticCode / DiagnosticError / DiagnosticErrorf unchanged.
// gopherbuzz instantiates the same framework separately for its own BZZ#### codes; the two namespaces
// never share a code.

// Diagnostic codes (MGS####): 1000=magusfile authoring, 2000=sandbox, 3000=workspace-scope, 4000=race detection, 5000=services, 6000=charms, 7000=knowledge-graph extraction, 8000=output references, 9000=auth/connector.

// Base URLs for diagnostic documentation, keyed by code-prefix subdir.
const (
	diagnosticSandboxBase   = "https://github.com/egladman/magus/blob/main/docs/codes/sandbox/"
	diagnosticRaceBase      = "https://github.com/egladman/magus/blob/main/docs/codes/race/"
	diagnosticMagusfileBase = "https://github.com/egladman/magus/blob/main/docs/codes/magusfile/"
	diagnosticServicesBase  = "https://github.com/egladman/magus/blob/main/docs/codes/services/"
	diagnosticCharmsBase    = "https://github.com/egladman/magus/blob/main/docs/codes/charms/"
	diagnosticKnowledgeBase = "https://github.com/egladman/magus/blob/main/docs/codes/knowledge/"
	diagnosticOutputRefBase = "https://github.com/egladman/magus/blob/main/docs/codes/outputref/"
	diagnosticAuthBase      = "https://github.com/egladman/magus/blob/main/docs/codes/auth/"
)

// DiagnosticCode identifies a stable diagnostic (MGS#### code). It aliases the framework's Code type, so
// every consumer keeps referring to types.DiagnosticCode while the machinery is shared.
type DiagnosticCode = diag.Code

// DiagnosticError is a typed error carrying an MGS code and message (the framework's Error). It implements
// error, and a DiagnosticCode is itself an errors.Is sentinel, so a caller matches one idiomatically:
// errors.Is(err, types.ExecDenied).
type DiagnosticError = diag.Error

// DiagnosticEvent is one diagnostic fired during a run (the framework's Event).
type DiagnosticEvent = diag.Event

// DiagnosticSink records diagnostics fired during a run (the framework's Sink).
type DiagnosticSink = diag.Sink

// ErrDiag is a sentinel for use with errors.Is on DiagnosticError values.
var ErrDiag = diag.ErrSentinel

// mgs is the magus diagnostic domain: it maps an MGS code to its docs page by prefix range. Every magus
// coded error is minted through it so the docs URL is captured for rendering.
var mgs = diag.New(func(c DiagnosticCode) string {
	switch {
	case strings.HasPrefix(string(c), "MGS9"):
		return diagnosticAuthBase + string(c) + ".md"
	case strings.HasPrefix(string(c), "MGS8"):
		return diagnosticOutputRefBase + string(c) + ".md"
	case strings.HasPrefix(string(c), "MGS7"):
		return diagnosticKnowledgeBase + string(c) + ".md"
	case strings.HasPrefix(string(c), "MGS6"):
		return diagnosticCharmsBase + string(c) + ".md"
	case strings.HasPrefix(string(c), "MGS5"):
		return diagnosticServicesBase + string(c) + ".md"
	case strings.HasPrefix(string(c), "MGS4"):
		return diagnosticRaceBase + string(c) + ".md"
	case strings.HasPrefix(string(c), "MGS1"):
		return diagnosticMagusfileBase + string(c) + ".md"
	default:
		return diagnosticSandboxBase + string(c) + ".md"
	}
})

// CodeURL returns the documentation URL for an MGS code. (URL resolution is domain-specific, so it is a
// function on the magus domain rather than a method on the shared Code type.)
func CodeURL(c DiagnosticCode) string { return mgs.URL(c) }

const (
	NoCITarget                DiagnosticCode = "MGS1001"
	SpellShadowed             DiagnosticCode = "MGS1002"
	BespokePhaseFragmentName  DiagnosticCode = "MGS1003"
	UnreachedFootprintDecl    DiagnosticCode = "MGS1004"
	RedundantFootprintGlob    DiagnosticCode = "MGS1005"
	UnknownTarget             DiagnosticCode = "MGS1006"
	TargetDependencyCycle     DiagnosticCode = "MGS1007"
	PathReadDenied            DiagnosticCode = "MGS2001"
	PathWriteDenied           DiagnosticCode = "MGS2002"
	EnvStripped               DiagnosticCode = "MGS2003"
	AllowlistUnresolved       DiagnosticCode = "MGS2004"
	SandboxUnsupported        DiagnosticCode = "MGS2005"
	PathShimSuspected         DiagnosticCode = "MGS2006"
	ExecDenied                DiagnosticCode = "MGS2007"
	DaemonSocketWithheld      DiagnosticCode = "MGS2008"
	NetEgress                 DiagnosticCode = "MGS2009"
	SandboxPolicyMismatch     DiagnosticCode = "MGS2010"
	DescendantBoundaryCrossed DiagnosticCode = "MGS3001"
	RaceDetected              DiagnosticCode = "MGS4001"
	OutputOverlapDetected     DiagnosticCode = "MGS4002"
	NondeterministicOutput    DiagnosticCode = "MGS4003"
	MissingDependencyDetected DiagnosticCode = "MGS4004"
	NearDuplicateServices     DiagnosticCode = "MGS5001"
	ServiceOpDetached         DiagnosticCode = "MGS5002"
	CommandOpNeverExits       DiagnosticCode = "MGS5003"
	CharmPatchInvalid         DiagnosticCode = "MGS6001"
	UnresolvableBuzzImport    DiagnosticCode = "MGS7001"
	DanglingDocReference      DiagnosticCode = "MGS7002"
	OutputRefMissing          DiagnosticCode = "MGS8001"
	OutputRefAmbiguous        DiagnosticCode = "MGS8002"
	OutputRefMalformed        DiagnosticCode = "MGS8003"
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
	PathReadDenied, PathWriteDenied, EnvStripped, AllowlistUnresolved,
	SandboxUnsupported, PathShimSuspected, ExecDenied, DaemonSocketWithheld,
	NetEgress, SandboxPolicyMismatch,
	DescendantBoundaryCrossed,
	RaceDetected, OutputOverlapDetected, NondeterministicOutput, MissingDependencyDetected,
	NearDuplicateServices, ServiceOpDetached, CommandOpNeverExits,
	CharmPatchInvalid,
	UnresolvableBuzzImport, DanglingDocReference,
	OutputRefMissing, OutputRefAmbiguous, OutputRefMalformed,
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
	return diag.WithSink(ctx, s)
}

// EmitDiagnostic records ev to the sink in ctx, or is a no-op when none is
// installed (the common CLI path).
func EmitDiagnostic(ctx context.Context, ev DiagnosticEvent) {
	diag.Emit(ctx, ev)
}
