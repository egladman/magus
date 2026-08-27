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
// is a statement about whose judgment is involved.
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

// Evidence says what a verdict RESTS ON, which is a different question from what the
// verdict is.
//
// magus already draws this distinction in one place and it is the best behavior in the
// tool: `magus refs` answering "not indexed" rather than "no matches", because those
// are different facts and collapsing them turns an absence of knowledge into a claim.
// Everywhere else a verdict arrived bare, so a check that could not run rendered
// identically to one that ran and found nothing wrong.
//
// A reader deciding whether to trust an `ok` needs this more than they need the `ok`.
type Evidence string

// The four are ordered by how much they entitle you to believe.
//
// EvidenceMeasured means magus observed the actual state: it walked the tree, dialed the
// socket, parsed the file, ran the probe. EvidenceDeclared means magus read what the
// workspace ASSERTS - a magusfile declaration, a config key - and took it at its word
// without confirming it, so the finding is only as true as the declaration. Those two
// differ exactly where it matters: a declared memory_mb and a measured peak disagreeing
// is what checkMemoryDeclarations exists to report.
//
// EvidenceInferred means the answer came from a derived model - the knowledge graph,
// static extraction, a near-miss heuristic - which can be stale, partial, or wrong in
// ways the source it models is not.
//
// EvidenceUnknown means the check did not run. It is not a pass. A skipped check
// rendering as ok is the single most common way a green report has lied here.
const (
	EvidenceMeasured Evidence = "measured"
	EvidenceDeclared Evidence = "declared"
	EvidenceInferred Evidence = "inferred"
	EvidenceUnknown  Evidence = "unknown"
)

// DoctorCheck is one validation and what it found.
type DoctorCheck struct {
	Name    string            `json:"name" yaml:"name"`
	Status  DoctorCheckStatus `json:"status" yaml:"status"`
	Message string            `json:"message,omitempty" yaml:"message,omitempty"`
	Details []string          `json:"details,omitempty" yaml:"details,omitempty"`
	// Evidence is what this particular run of the check rests on. A check declares its
	// usual evidence in the registry; a run that could not look sets EvidenceUnknown
	// here, and one that looked harder than usual (tool-readiness under --probe) raises
	// it to EvidenceMeasured.
	Evidence Evidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	// Fix is the magus command that remedies this finding, as argv without the leading
	// "magus" - nil when there is nothing mechanical to run. `doctor --fix` runs it; with
	// no --fix it is printed, so the report always names the cure even when it is not
	// applying it.
	//
	// An EXISTING first-class command, never a private repair routine. That is the whole
	// safety property: --fix can only do things you could have typed yourself and can
	// inspect afterwards, and a check whose remedy needs judgment (narrow this glob, or
	// accept the volatility?) simply declares no Fix and stays a report. It is also why a
	// config remedy is `config set ...` rather than a writer of its own - there is exactly
	// one thing in magus that edits config, and this is not a second one.
	Fix []string `json:"fix,omitempty" yaml:"fix,omitempty"`
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
	// Unknown counts checks that did not run, and is deliberately carved OUT of OK
	// rather than added beside it: "44 ok" that silently included six checks which
	// never looked was the number this field exists to stop reporting. It is not a
	// failure and does not affect the exit status - nothing was found to be wrong,
	// because nothing was looked at.
	Unknown int `json:"unknown" yaml:"unknown"`
}

// DoctorReport is the full doctor output: every check, and the counts.
type DoctorReport struct {
	Workspace string        `json:"workspace" yaml:"workspace"`
	Checks    []DoctorCheck `json:"checks" yaml:"checks"`
	Summary   DoctorSummary `json:"summary" yaml:"summary"`
}
