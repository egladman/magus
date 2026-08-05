package types

// Doctor's report, as a domain type rather than an internal one.
//
// It lives here because a magusfile and a script read it: magus.doctor returns
// DoctorReport, so a caller iterates checks and branches on status instead of
// grepping console text for the word "fail". internal/doctor names these types
// directly; there is no second spelling of them anywhere.

// DoctorCheckStatus is one check's outcome. Advice is deliberately not a failure:
// it is worth knowing and never a gate, which is the distinction the CI surface and
// the tool share one word for.
type DoctorCheckStatus string

// DoctorFail and DoctorAdvice are a deliberate split, and which one a check returns
// is a statement about whose judgement is involved.
//
// DoctorFail is for a workspace that is WRONG in a way nobody's taste can rescue:
// a dependency cycle, a magusfile that will not parse, two targets claiming one
// output, a policy naming a target that does not exist. These are facts, they
// break the build or corrupt the cache, and failing on them is not an opinion.
//
// DoctorAdvice is for a convention magus RECOMMENDS: how targets are named,
// whether every project binds a language spell, whether a spell target carries a
// doc comment. These are conventions that have worked well, documented so you can
// take them - not requirements, because magus does not get to decide how your
// repository is laid out. `ci` is the one reserved target, and everything past it
// is yours.
//
// The distinction is not cosmetic. When doctor had only ok and fail, a convention
// check had two options: fail (and dictate) or not exist. What actually happened
// is that each one grew its own private escape hatch - no_language for language
// coverage, and briefly allow_bespoke_name for target naming - so the config
// surface grew one key per opinion, and taking magus's advice became mandatory
// unless you wrote a paragraph explaining yourself. Advice that exits zero needs
// no escape hatch at all.
//
// There is deliberately no switch that promotes advice to failure. A knob for
// that would just be the imposition again with an opt-in label on it, and the
// workspace that wants a convention enforced can enforce it - in its own lint
// target, with its own tools, on its own terms. magus reports what it noticed and
// gets out of the way.
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
