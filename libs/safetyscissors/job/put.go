package job

import "github.com/egladman/magus/libs/codec"

// Put is one put against a job row: the fields it changes and nothing else. A nil
// field is left as the row has it; a non-nil field is written, and a non-nil empty value
// is an explicit clear. That is why every field is a pointer: the second put in a job's
// lifecycle (state running) advances the state without erasing what an earlier put
// declared.
//
// The json tags are the keys a put is written under.
type Put struct {
	Parent *string `json:"parent,omitzero"`
	// Agent replaces the row's whole agent section; the zero Agent clears it.
	Agent      *Agent  `json:"agent,omitzero"`
	Checkpoint *string `json:"checkpoint,omitzero"`
	// BriefFormat sets the row's brief format; empty clears it back to the workspace's.
	BriefFormat *BriefFormat `json:"brief_format,omitzero"`
	// Delivery and ParentHead: see [Job.Delivery].
	Delivery   *string `json:"delivery,omitzero"`
	ParentHead *string `json:"parent_head,omitzero"`
	// Follows sets the row's link to the job it continues; see [Job.Follows].
	Follows *string `json:"follows,omitzero"`
	// Timeout is a length such as "45m", never an instant: the deadline is stamped when
	// the store applies the put. "0" or "" clears it.
	Timeout *string `json:"timeout,omitzero"`
	// Enter enters the job at a path and declares nothing, so a put carrying it carries no
	// other field.
	Enter *string `json:"enter,omitzero"`
	State *State  `json:"state,omitzero"`
	// Check is the declared check, as opaque JSON; nil leaves the row's as it is. The
	// caller's merge decides which value clears it.
	Check     codec.RawMessage `json:"check,omitzero"`
	Paths     PathsPut         `json:"paths,omitzero"`
	DependsOn *[]string        `json:"depends_on,omitzero"`
	Breaks    *[]string        `json:"breaks,omitzero"`
	Goals     *[]Goal          `json:"goals,omitzero"`
	// Deviations replaces the row's deviations; each is validated as a declaration's would be.
	Deviations *[]Deviation `json:"deviations,omitzero"`
	ReadOnly   *bool        `json:"read_only,omitzero"`
	// MaxAttempts is how many times the job may be forked, its first fork included; 0
	// clears the cap.
	MaxAttempts *int `json:"max_attempts,omitzero"`
	// MaxClaims is how many proved claims one attempt takes; 0 clears it to the default.
	MaxClaims *int `json:"max_claims,omitzero"`
	// Weight is the job's lean on the machine budget, in (0,1]. See [Job.Weight].
	Weight *float64 `json:"weight,omitzero"`
}

// PathsPut is the paths a put changes, each left, written or cleared as a
// [Put] field is. A put never sets soft paths: only the store narrows them.
type PathsPut struct {
	Write *[]string `json:"write,omitzero"`
	Read  *[]string `json:"read,omitzero"`
	Deny  *[]string `json:"deny,omitzero"`
}

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
