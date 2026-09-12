package types

// JobResult is what a holder returns when its job is done: changed paths, the evidence for
// the check it was given, the jobs it forked, and the risks it could not settle.
//
// A struct rather than the prose the skill asked for since it was written. Prose on both
// ends means verification is the forking agent READING a paragraph, and a holder that ran
// a filtered subset writes the same paragraph as one that did the work. This does not make
// a holder honest; it makes a dishonest result CHECKABLE, which is what job.Verify does.
//
// It lives here rather than beside the verifier because `job exit` FILES it onto the job,
// so it is part of the stored record every checkout of the repository reads.
type JobResult struct {
	// SchemaVersion is the shape this result was written in, and it is REQUIRED: the
	// decoder rejects a version it does not know by name, so a holder whose harness is a
	// release ahead reads "this magus accepts version 1" instead of watching a field it
	// was told to send be rejected as unknown.
	SchemaVersion int `json:"schema_version" yaml:"schema_version"`
	// Job is the id the holder believes it is filing under. Optional, and checked against
	// the stored job when present: a result filed under the wrong id happens when several
	// holders share a prompt template, and it reads as success because every other field
	// is plausible.
	Job string `json:"job,omitempty" yaml:"job,omitempty"`
	// ChangedPaths are the workspace-relative paths this holder wrote, as it reports them.
	// Never proof: the integration pass compares the store against the ACTUAL diff since
	// the checkpoint. This is the CLAIM, and checking it against the declared lanes is
	// what catches the holder that widened its own scope.
	ChangedPaths []string `json:"changed_paths" yaml:"changed_paths"`
	// Validation is the one check the job was given, and how it ended.
	Validation JobResultValidation `json:"validation" yaml:"validation"`
	// Descendants are the jobs this holder forked. One the store does not carry is a
	// branch of the plan nobody is tracking, which job.Verify reports.
	Descendants []string `json:"descendants,omitempty" yaml:"descendants,omitempty"`
	// UnresolvedRisks is what the holder could not settle. Never omitempty: an absent list
	// and an empty one are the same JSON once the key can vanish, so a holder that never
	// considered the question would be indistinguishable from one that found nothing.
	UnresolvedRisks []string `json:"unresolved_risks" yaml:"unresolved_risks"`
}

// JobResultValidation is the evidence for the job's assigned check. OutputRef is what
// makes it evidence rather than an assertion: it names a log magus stored, and the store's
// own record of that run says which command produced it and how it ended.
//
// THERE IS NO `passed` FIELD, and its absence is the point: a holder's verdict on its own
// run reads identical whether the check ran or not. job.Verify derives the outcome from
// the recorded run.
type JobResultValidation struct {
	// Command is the check as the holder ran it, rendered beside the status for a person.
	// The job's own check is what the evidence is bound to, so a command here that
	// disagrees with the ref is a discrepancy a reader sees rather than a rule.
	Command string `json:"command" yaml:"command"`
	// OutputRef is the reference id magus stored that run's captured output under.
	OutputRef string `json:"output_ref" yaml:"output_ref"`
}

// JobAttempt is what an output store recorded for one captured run: which command produced
// it, and whether that command failed. The identity is STRUCTURED because the store holds
// it that way, so comparing project/target/spell binds a ref to a check without either
// side having to spell a command line the same way.
//
// It is FILED ON THE JOB by `magus job exit`, resolved in the holder's own checkout. An
// output store belongs to a cache dir, so a parent waiting from another worktree can never
// resolve the holder's ref itself; carrying the record is what makes evidence survive the
// trip between two checkouts of one repository.
type JobAttempt struct {
	// Found is false for a ref no longer in the store, which is not an error: nobody can
	// reopen it either way, and that is the fact verification turns on.
	Found bool `json:"found" yaml:"found"`
	// Ref is the output reference this record was resolved from, kept so a reader of the
	// stored job can tell which citation it describes.
	Ref string `json:"ref,omitempty" yaml:"ref,omitempty"`
	// Project, Target and Spell are the run's recorded identity. Spell is the
	// `spell::op` filter that selected the definition, empty when the run named none.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Target  string `json:"target,omitempty"  yaml:"target,omitempty"`
	Spell   string `json:"spell,omitempty"   yaml:"spell,omitempty"`
	// Failed is the run's own outcome, which is what verification reads INSTEAD of a
	// holder's claim to have passed.
	Failed bool `json:"failed,omitempty" yaml:"failed,omitempty"`
}

// String renders what the store recorded, in the same spelling a check renders, so a
// rejection puts the two side by side.
func (a JobAttempt) String() string {
	target := a.Target
	if a.Spell != "" {
		target = a.Spell + "::" + target
	}
	return LeaseCheck{Target: target, Project: a.Project}.String()
}
