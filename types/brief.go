package types

// BriefFormat is the shape a spawn or continuation brief takes. markdown is prose and runs
// only the brief rules the guard always runs; buzz is one Brief literal the guard
// type-checks before the worker sees it.
type BriefFormat string

const (
	// BriefFormatMarkdown is a prose brief, the default.
	BriefFormatMarkdown BriefFormat = "markdown"
	// BriefFormatBuzz is a brief written as one Brief literal.
	BriefFormatBuzz BriefFormat = "buzz"
)

// Brief is what a buzz-format brief declares beyond the job record: the job it is for,
// the goal and why, the decisions already made, and the evidence behind them. A field with
// no omitempty is required.
type Brief struct {
	// Job is the id of the live row the brief is for.
	Job string `json:"job"`
	// Goal is what the worker is to achieve, in a sentence.
	Goal string `json:"goal"`
	// Why is why the goal matters.
	Why string `json:"why,omitempty"`
	// Decisions are the choices already made, so the worker does not reopen them.
	Decisions []BriefDecision `json:"decisions,omitempty"`
	// Evidence are pointers the worker can open, never summaries of them.
	Evidence []BriefRef `json:"evidence,omitempty"`
}

// BriefDecision is one decision a brief hands on.
type BriefDecision struct {
	What string `json:"what"`
	Why  string `json:"why,omitempty"`
}

// BriefRefKind is what a BriefRef points at.
type BriefRefKind string

const (
	BriefRefOutput     BriefRefKind = "output"
	BriefRefVerdict    BriefRefKind = "verdict"
	BriefRefInvocation BriefRefKind = "invocation"
	BriefRefFile       BriefRefKind = "file"
	BriefRefPlan       BriefRefKind = "plan"
	BriefRefCommit     BriefRefKind = "commit"
)

// BriefRef is one piece of evidence: an output ref, a guard verdict, a file:line, a plan
// path or a commit, with what it says.
type BriefRef struct {
	Kind BriefRefKind `json:"kind"`
	ID   string       `json:"id"`
	Says string       `json:"says,omitempty"`
}
