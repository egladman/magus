package types

import (
	"errors"
	"fmt"
	"strings"
)

// Diagnostic codes (MGS####): 1000=magusfile authoring, 2000=sandbox, 3000=workspace-scope, 4000=race detection.

// Base URLs for diagnostic documentation, keyed by code-prefix subdir.
const (
	diagnosticSandboxBase   = "https://github.com/egladman/tack/blob/main/magus/docs/codes/sandbox/"
	diagnosticRaceBase      = "https://github.com/egladman/tack/blob/main/magus/docs/codes/race/"
	diagnosticMagusfileBase = "https://github.com/egladman/tack/blob/main/magus/docs/codes/magusfile/"
)

// DiagnosticCode identifies a stable diagnostic (MGS#### code).
type DiagnosticCode string

// URL returns the documentation URL for this code.
func (c DiagnosticCode) URL() string {
	switch {
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
)

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
