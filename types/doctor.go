package types

// Doctor's report, as a domain type rather than an internal one.
//
// It lives here because a magusfile and a script read it: magus.doctor returns
// DoctorReport, so a caller iterates checks and branches on status instead of
// grepping console text for the word "fail". internal/doctor aliases these, so the
// checks themselves keep their local vocabulary and nothing there changed.

// DoctorCheckStatus is one check's outcome. Advice is deliberately not a failure:
// it is worth knowing and never a gate, which is the distinction the CI surface and
// the tool share one word for.
type DoctorCheckStatus string

const (
	DoctorOK     DoctorCheckStatus = "ok"
	DoctorFail   DoctorCheckStatus = "fail"
	DoctorAdvice DoctorCheckStatus = "advice"
)

// DoctorCheck is one validation and what it found.
type DoctorCheck struct {
	Name    string            `json:"name" yaml:"name"`
	Status  DoctorCheckStatus `json:"status" yaml:"status"`
	Message string            `json:"message,omitempty" yaml:"message,omitempty"`
	Details []string          `json:"details,omitempty" yaml:"details,omitempty"`
}

// DoctorSummary counts check outcomes.
//
// OK carries a buzz tag because the mirror generator lowercases only the FIRST rune of
// a field name, which turns the initialism OK into `oK`. The tag is the sanctioned
// override (FileInfo.IsDir uses it the same way) and keeps the Go field idiomatic.
type DoctorSummary struct {
	OK     int `json:"ok" yaml:"ok" buzz:"ok"`
	Fail   int `json:"fail" yaml:"fail"`
	Advice int `json:"advice" yaml:"advice"`
}

// DoctorReport is the full doctor output: every check, and the counts.
type DoctorReport struct {
	Workspace string        `json:"workspace" yaml:"workspace"`
	Checks    []DoctorCheck `json:"checks" yaml:"checks"`
	Summary   DoctorSummary `json:"summary" yaml:"summary"`
}
