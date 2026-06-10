package types

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is the canonical "miss" sentinel for I/O-backed lookups.
var ErrNotFound = errors.New("magus: not found")

// ErrSpellNotRegistered is returned by magus.WithSpell when the named spell is not registered.
var ErrSpellNotRegistered = errors.New("magus: spell not registered")

// ErrSpellNameRequired is returned by magus.WithSpell when called with an empty name.
var ErrSpellNameRequired = errors.New("magus: spell name required")

// ErrUnregisteredDep is returned by (*Workspace).Graph in Strict mode when a
// declared dependency path has not been registered.
var ErrUnregisteredDep = errors.New("magus: dependency not registered")

// UnregisteredDep is one missing-dep observation found while building the graph.
type UnregisteredDep struct {
	Consumer   string // project path that declared the dep
	Dep        string // dep path that did not resolve
	DidYouMean string // nearest registered path, or ""
}

// UnregisteredDepError aggregates every UnregisteredDep found during a Graph call.
type UnregisteredDepError struct {
	Missing []UnregisteredDep
}

// Error returns an end-user-readable description of every missing dependency.
func (e *UnregisteredDepError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "magus: dependency not registered (%d unresolved)\n", len(e.Missing))
	for _, m := range e.Missing {
		if m.DidYouMean != "" {
			fmt.Fprintf(&sb, "  - %s -> %s   (did you mean: %s)\n", m.Consumer, m.Dep, m.DidYouMean)
		} else {
			fmt.Fprintf(&sb, "  - %s -> %s\n", m.Consumer, m.Dep)
		}
	}
	sb.WriteString("\nfix: register the missing project(s) with magus.project.register(\"<path>\", fun(p, cb) { cb({...}); })\n")
	sb.WriteString("     in a magusfile, or correct the path passed to magus.WithDependsOn\n")
	sb.WriteString("     in the consuming magusfile.")
	return sb.String()
}

// Is returns true for ErrUnregisteredDep.
func (*UnregisteredDepError) Is(target error) bool {
	return target == ErrUnregisteredDep
}

// SpellError records the error from a single spell during multi-spell fan-out.
type SpellError struct {
	Spell string
	Err   error
}

// SpellErrors aggregates failures across multiple spells running the same target.
type SpellErrors struct {
	Project string
	Target  string
	Failed  []SpellError
}

func (e *SpellErrors) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "magus %s %s: %d spell(s) failed\n", e.Target, e.Project, len(e.Failed))
	for _, f := range e.Failed {
		fmt.Fprintf(&sb, "  [%s] %s\n", f.Spell, f.Err)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Unwrap satisfies errors.Is/As.
func (e *SpellErrors) Unwrap() []error {
	errs := make([]error, len(e.Failed))
	for i, f := range e.Failed {
		errs[i] = f.Err
	}
	return errs
}

// Diagnostic codes (MGS####): 1000=magusfile authoring, 2000=sandbox, 3000=workspace-scope, 4000=race detection.

// DiagnosticDocBase is the base URL for sandbox diagnostic documentation.
// Forks may override this variable before any DiagnosticCode.URL() call.
var DiagnosticDocBase = "https://github.com/egladman/tack/blob/main/magus/docs/codes/sandbox/"
var diagnosticRaceBase = "https://github.com/egladman/tack/blob/main/magus/docs/codes/race/"
var diagnosticMagusfileBase = "https://github.com/egladman/tack/blob/main/magus/docs/codes/magusfile/"

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
		return DiagnosticDocBase + string(c) + ".md"
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
