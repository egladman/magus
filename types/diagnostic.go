package types

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Diagnostic codes (MGS####): 1000=magusfile authoring, 2000=sandbox, 3000=workspace-scope, 4000=race detection, 5000=services, 6000=charms, 7000=knowledge-graph extraction, 8000=output references.

// Base URLs for diagnostic documentation, keyed by code-prefix subdir.
const (
	diagnosticSandboxBase   = "https://github.com/egladman/magus/blob/main/docs/codes/sandbox/"
	diagnosticRaceBase      = "https://github.com/egladman/magus/blob/main/docs/codes/race/"
	diagnosticMagusfileBase = "https://github.com/egladman/magus/blob/main/docs/codes/magusfile/"
	diagnosticServicesBase  = "https://github.com/egladman/magus/blob/main/docs/codes/services/"
	diagnosticCharmsBase    = "https://github.com/egladman/magus/blob/main/docs/codes/charms/"
	diagnosticKnowledgeBase = "https://github.com/egladman/magus/blob/main/docs/codes/knowledge/"
	diagnosticOutputRefBase = "https://github.com/egladman/magus/blob/main/docs/codes/outputref/"
)

// DiagnosticCode identifies a stable diagnostic (MGS#### code).
type DiagnosticCode string

// URL returns the documentation URL for this code.
func (c DiagnosticCode) URL() string {
	switch {
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
}

const (
	NoCITarget                DiagnosticCode = "MGS1001"
	SpellShadowed             DiagnosticCode = "MGS1002"
	BespokePhaseFragmentName  DiagnosticCode = "MGS1003"
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
)

// allDiagnosticCodes lists every registered code in ascending MGS order. Keep it
// in sync with the const block above; it is the enumeration source for tooling
// (the knowledge graph turns each into a diagnostic node) since Go const blocks
// are not reflectable.
var allDiagnosticCodes = []DiagnosticCode{
	NoCITarget, SpellShadowed, BespokePhaseFragmentName,
	PathReadDenied, PathWriteDenied, EnvStripped, AllowlistUnresolved,
	SandboxUnsupported, PathShimSuspected, ExecDenied, DaemonSocketWithheld,
	NetEgress, SandboxPolicyMismatch,
	DescendantBoundaryCrossed,
	RaceDetected, OutputOverlapDetected, NondeterministicOutput, MissingDependencyDetected,
	NearDuplicateServices, ServiceOpDetached, CommandOpNeverExits,
	CharmPatchInvalid,
	UnresolvableBuzzImport, DanglingDocReference,
	OutputRefMissing, OutputRefAmbiguous, OutputRefMalformed,
}

// AllDiagnosticCodes returns every registered diagnostic code in ascending MGS
// order. The returned slice is a copy; callers may mutate it freely.
func AllDiagnosticCodes() []DiagnosticCode {
	out := make([]DiagnosticCode, len(allDiagnosticCodes))
	copy(out, allDiagnosticCodes)
	return out
}

// DiagnosticError is a typed error carrying an MGS code and message.
type DiagnosticError struct {
	Code DiagnosticCode
	Msg  string
}

// ErrDiag is a sentinel for use with errors.Is on DiagnosticError values.
var ErrDiag = errors.New("diag")

func (e *DiagnosticError) Error() string {
	return fmt.Sprintf("[%s] %s\n  see: %s", e.Code, e.Msg, e.Code.URL())
}

// Is matches ErrDiag or another DiagnosticError with the same code.
func (e *DiagnosticError) Is(target error) bool {
	if target == ErrDiag {
		return true
	}
	other, ok := target.(*DiagnosticError)
	return ok && other.Code == e.Code
}

// DiagnosticErrorf builds a DiagnosticError with a code and formatted message.
func DiagnosticErrorf(c DiagnosticCode, format string, args ...any) *DiagnosticError {
	return &DiagnosticError{Code: c, Msg: fmt.Sprintf(format, args...)}
}

// FormatDiagnostic formats a diagnostic message with code and doc URL for slog logging.
func FormatDiagnostic(c DiagnosticCode, msg string) string {
	return fmt.Sprintf("[%s] %s (see %s)", c, msg, c.URL())
}

// DiagnosticEvent is one diagnostic fired during a run: the code, a message, and
// the unit that emitted it ("<project>:<target>", or a project path, or empty).
type DiagnosticEvent struct {
	Code    DiagnosticCode `json:"code"              yaml:"code"`
	Message string         `json:"message,omitempty" yaml:"message,omitempty"`
	Unit    string         `json:"unit,omitempty"    yaml:"unit,omitempty"`
}

// DiagnosticSink records diagnostics fired during a run; it must be safe for
// concurrent use. A run installs one in its context so emission sites reach it via
// EmitDiagnostic; consumers (the runtime shard, the report stream) drain it.
type DiagnosticSink interface {
	RecordDiagnostic(DiagnosticEvent)
}

type diagSinkKey struct{}

// WithDiagnosticSink returns ctx carrying s, so a deep emission site can reach the
// sink without threading it through every signature.
func WithDiagnosticSink(ctx context.Context, s DiagnosticSink) context.Context {
	return context.WithValue(ctx, diagSinkKey{}, s)
}

// EmitDiagnostic records ev to the sink in ctx, or is a no-op when none is
// installed (the common CLI path).
func EmitDiagnostic(ctx context.Context, ev DiagnosticEvent) {
	if s, ok := ctx.Value(diagSinkKey{}).(DiagnosticSink); ok && s != nil {
		s.RecordDiagnostic(ev)
	}
}
