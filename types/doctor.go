package types

import "github.com/egladman/magus/types/check"

// Doctor's report, as a domain type rather than an internal one.
//
// It lives here because a magusfile and a script read it: magus.doctor returns
// DoctorReport, so a caller iterates checks and branches on status instead of
// grepping console text for the word "fail". internal/doctor names these types
// directly; there is no second spelling of them anywhere.

// DoctorSummary counts check outcomes.
//
// OK carries a buzz tag because the mirror generator lowercases only the FIRST rune of
// a field name, which turns the initialism OK into `oK`. The tag is the sanctioned
// override (FileInfo.IsDir uses it the same way) and keeps the Go field idiomatic.
type DoctorSummary struct {
	OK     int `json:"ok" yaml:"ok" buzz:"ok"`
	Fail   int `json:"fail" yaml:"fail"`
	Advice int `json:"advice" yaml:"advice"`
	// Unknown counts checks that did not run, and is deliberately carved OUT of OK
	// rather than added beside it: "44 ok" that silently included six checks which
	// never looked was the number this field exists to stop reporting. It is not a
	// failure and does not affect the exit status: nothing was found to be wrong,
	// because nothing was looked at.
	Unknown int `json:"unknown" yaml:"unknown"`
}

// DoctorReport is the full doctor output: every check, and the counts.
type DoctorReport struct {
	Workspace string        `json:"workspace" yaml:"workspace"`
	Checks    []check.Check `json:"checks" yaml:"checks"`
	Summary   DoctorSummary `json:"summary" yaml:"summary"`
}
