package spells

// DiagnosticFormat names the convention a tool prints its findings in. Nothing
// reads it. TODO: delete with Tool.Diagnostics (ADR 0021).
type DiagnosticFormat string

const (
	// DiagnosticNone is the zero value: prose as far as magus is concerned.
	DiagnosticNone DiagnosticFormat = ""
	// DiagnosticGNU is the GNU Coding Standards format,
	// `[program:]file:line[:column]: severity: message`.
	DiagnosticGNU DiagnosticFormat = "gnu"
)
