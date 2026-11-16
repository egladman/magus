package types

// UnverifiedReport is what magus\workspace(dir).unverified(revision) reads in the checkout
// holding dir: the files revision changed since its merge base with Base, and those no clean
// passing run recorded in that checkout read as revision holds them. Facts only: a workspace
// rule decides what they stop.
type UnverifiedReport struct {
	// Revision is the full id read.
	Revision string `json:"revision"`
	// Base is the ref the merge base is taken with, as vcs\base names it (`origin/main`).
	Base string `json:"base"`
	// Changed are the repository-relative paths revision changed since that merge base, sorted.
	Changed []string `json:"changed,omitempty"`
	// Unverified are those of Changed no run read, in Changed's order.
	Unverified []UnverifiedFile `json:"unverified,omitempty"`
	// Runs are the recorded runs whose target claims a file of Unverified, newest first,
	// failed, dirty and revisionless runs included.
	Runs []RunEvidence `json:"runs,omitempty"`
	// Unknown says why the read did not finish, empty when it did. When set, Changed and
	// Unverified are not an answer.
	Unknown string `json:"unknown,omitempty"`
}

// UnverifiedFile is one changed file no clean passing run read.
type UnverifiedFile struct {
	Path string `json:"path"`
	// Commands are at most three `magus run` lines whose target reads the file: targets this
	// checkout ran over it first, then magusfile targets whose declared inputs or outputs
	// claim it. Empty when no target claims it, which no run can fix: declaring the file as
	// a target's input is how it gets checked.
	Commands []string `json:"commands,omitempty"`
}

// RunEvidence is one recorded run of a target that claims a changed file.
type RunEvidence struct {
	// Ref is the run's step ref, which `magus query output` reads.
	Ref     string `json:"ref"`
	Project string `json:"project"`
	// Target is bare, its charms stripped.
	Target string `json:"target"`
	// Revision is the commit the run's inputs were read at, empty when the run recorded none.
	Revision string `json:"revision,omitempty"`
	// Dirty is set when the working tree held uncommitted edits during the run.
	Dirty  bool `json:"dirty,omitempty"`
	Failed bool `json:"failed,omitempty"`
	// Cached is set when the target did not run: a cache hit recorded this attempt over the
	// current revision, replaying the log. It counts as evidence for the target's declared
	// inputs.
	Cached bool `json:"cached,omitempty"`
	// Claims are the files of Changed its target's declared inputs or outputs claim.
	Claims []string `json:"claims,omitempty"`
	// Reads are those of Claims it read as revision holds them; empty for a run that does
	// not count.
	Reads []string `json:"reads,omitempty"`
}
