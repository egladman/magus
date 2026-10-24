package types

// JobPut is one put against a job row: the fields it changes and nothing else. A nil
// field is left as the row has it; a non-nil field is written, and a non-nil empty value
// is an explicit clear. That is why every field is a pointer: the second put in a job's
// lifecycle (state running) advances the state without erasing what an earlier put
// declared.
//
// The json tags are the keys magus\job.put takes in its opts map.
type JobPut struct {
	Parent     *string `json:"parent,omitempty"`
	Criteria   *string `json:"criteria,omitempty"`
	Checkpoint *string `json:"checkpoint,omitempty"`
	Model      *string `json:"model,omitempty"`
	// BriefFormat sets the row's brief format; empty clears it back to magus.yaml's.
	BriefFormat *BriefFormat `json:"brief_format,omitempty"`
	// Follows and FreshReason set the row's link to the job it continues; see [Job.Follows].
	Follows     *string `json:"follows,omitempty"`
	FreshReason *string `json:"fresh_reason,omitempty"`
	// Timeout is a length such as "45m", never an instant: the deadline is stamped when
	// the store applies the put. "0" or "" clears it.
	Timeout *string `json:"timeout,omitempty"`
	// Enter enters the job at a path and declares nothing, so a put carrying it carries no
	// other field.
	Enter *string   `json:"enter,omitempty"`
	State *JobState `json:"state,omitempty"`
	// Check is the declared check; one with neither a target nor a script clears it.
	Check      *LeaseCheck `json:"check,omitempty"`
	WritePaths *[]string   `json:"write_paths,omitempty"`
	DenyPaths  *[]string   `json:"deny_paths,omitempty"`
	ReadPaths  *[]string   `json:"read_paths,omitempty"`
	DependsOn  *[]string   `json:"depends_on,omitempty"`
	Breaks     *[]string   `json:"breaks,omitempty"`
	Goals      *[]Goal     `json:"goals,omitempty"`
	// Waivers replaces the row's waivers; each is validated as a declaration's would be.
	Waivers  *[]JobWaiver `json:"waivers,omitempty"`
	ReadOnly *bool        `json:"read_only,omitempty"`
	// MaxAttempts is how many times the job may be forked, its first fork included; 0
	// clears the cap.
	MaxAttempts *int `json:"max_attempts,omitempty"`
	// MaxClaims is how many proved claims one attempt takes; 0 clears it to the default.
	MaxClaims *int `json:"max_claims,omitempty"`
	// Weight is the job's lean on the machine budget, in (0,1]. See [Job.Weight].
	Weight *float64 `json:"weight,omitempty"`
}
