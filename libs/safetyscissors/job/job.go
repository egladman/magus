package job

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/internal/apigroup"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

// State is where one lease stands. The three terminal values are
// the point of the set: a job that never reaches one is a job nobody closed.
//
// NoReturn is deliberately distinct from Fail. A worker that died, stalled, or was
// killed produced no verdict at all, and folding that into "failed" claims a judgment
// nobody made: the root agent still has to go look. Silence is not a pass, and it is
// not a failure either.
type State string

const (
	// StateDeclared is a job written before its worker was spawned.
	StateDeclared State = "declared"
	// StateRunning is a holder in flight.
	StateRunning State = "running"
	// StateExited is a job its holder returned, with a result filed and nobody waiting on
	// it yet. The POSIX reading exactly: the child is done and its status has not been
	// collected, which is neither "still working" nor any judgment of the work.
	StateExited State = "exited"
	// StatePass is a job whose acceptance criteria and assigned check both
	// passed, as judged by the agent that owns it.
	StatePass State = "pass"
	// StateFail is a lease that returned and did not meet its criteria.
	StateFail State = "fail"
	// StateNoReturn is a lease that never reported: dead, stalled, or cancelled.
	StateNoReturn State = "no_return"
)

// states is the closed set, in lifecycle order. The vocabulary lives beside the
// constants because four readers quote it: the job decoder, the put merge, the
// published schema, and the error each of them raises. A closed set that drifts
// rejects a client for a value the schema told it to send.
func states() []State {
	return []State{StateDeclared, StateRunning, StateExited, StatePass, StateFail, StateNoReturn}
}

// ValidState reports whether s is one of the job states. An empty state is NOT.
// A job that has not said where it stands carries none, and the callers that
// allow that test for it themselves.
func ValidState(s State) bool { return slices.Contains(states(), s) }

// BaseVerdict says how the base a worker reported at registration compares
// with the Checkpoint its lease was handed. A FACT computed at that moment, never a
// refusal: a diverged worker is registered like any other and told what diverged.
//
// The middle value is why this is not a boolean. A checkpoint is a revision PLUS a
// dirty-patch digest, so two trees can share a revision and hold different uncommitted
// work; that is neither agreement nor the kind of divergence a respawn fixes, and folding
// it into either one sends the worker to the wrong remedy.
type BaseVerdict string

const (
	// BaseMatch is a reported base identical to the checkpoint, digest included.
	BaseMatch BaseVerdict = "match"
	// BaseRevisionMatch is the same revision carrying a different uncommitted patch.
	BaseRevisionMatch BaseVerdict = "revision-match"
	// BaseDiverged is a different revision: the worker is not on the tree it was handed.
	BaseDiverged BaseVerdict = "diverged"
	// BaseUnknown is a registration with nothing to compare against, because the lease was
	// declared without a Checkpoint. Distinct from BaseMatch on the same ground
	// StateNoReturn is distinct from StateFail: claiming agreement nobody observed is a
	// judgment the ledger did not make.
	BaseUnknown BaseVerdict = "unknown"
)

// Terminal reports whether the lease is done, however it ended. It keeps a finished lease
// out of the overlap report below, and it is what `ledger accept` reads to refuse
// re-grading a job somebody already graded.
func (s State) Terminal() bool {
	return s == StatePass || s == StateFail || s == StateNoReturn
}

// Live reports whether the lease can still act on its paths: declared, running, or
// exited. A job with no state is not live, it has not said it is; that is the one rule the
// guard and the sandbox both scope a holder by, so it lives here rather than in either.
//
// EXITED IS LIVE, which reads oddly next to Terminal and is the safe direction. A holder
// that filed its result still holds the lease on its checkout, and a verification that
// rejects sends it back to the same write paths; dropping the job out of live here would
// leave every write after `job exit` graded by nothing at all. What an exited job no
// longer does is claim its paths against another job: it blocks no other fork and is in
// no overlap (see [State.Editing]).
func (s State) Live() bool {
	return s == StateDeclared || s == StateRunning || s == StateExited
}

// Editing reports whether a holder may still be writing the job's paths: declared or
// running. Only an editing job claims its write paths against another job's.
func (s State) Editing() bool {
	return s == StateDeclared || s == StateRunning
}

// PathHolders are the jobs that hold their write paths against everyone else
// now. These are the [State.Editing] ones. An exited job filed its result
// and holds nothing, so an output in its paths is anyone's to regenerate.
// The write judge and the run-output judge both read ownership here. rows is
// not modified.
func PathHolders(rows []Job) []Job {
	return slices.DeleteFunc(slices.Clone(rows), func(r Job) bool { return !r.State.Editing() })
}

// DefaultWeight is the weight of a job that names none, and of a run bound to no job.
const DefaultWeight = 0.5

// ValidWeight reports whether w is in (0,1]. Out of range is refused, never clamped.
func ValidWeight(w float64) bool { return w > 0 && w <= 1 }

// MaxIDLen bounds a lease id: long enough for a branch-shaped ledger name, short
// enough that the id stays a correlation key rather than a payload riding every event
// line.
const MaxIDLen = 128

// ValidID reports whether id may be stamped as a lease and checked out as a branch:
// see validateID.
//
// The narrowness is a security property: a lease id is EXEMPT from the redaction
// internal/trail applies to every other event field, so every channel that stamps one
// passes its candidate through here first. It lives beside [Job] because the ledger and
// the trail both read the id.
func ValidID(id string) bool { return idRule(id) == "" }

// validateID refuses an id that may not name a job, as an [*idError] naming the rule it
// breaks. The id is also the branch a checkout of the job is made on, so it is held to
// what every VCS accepts as one: lowercase letters, digits, '-' and '.', with '/' only
// between segments, no segment empty or starting with '.' or '-', never ':' or '..', at
// most [MaxIDLen] characters.
func validateID(id string) error {
	if rule := idRule(id); rule != "" {
		return &idError{ID: id, Rule: rule}
	}
	return nil
}

// idError is an id validateID refuses, and the rule it breaks.
type idError struct {
	ID   string
	Rule string
}

func (e *idError) Error() string { return fmt.Sprintf("%q is not a job id: %s", e.ID, e.Rule) }

// idRule names the rule id breaks, or returns "" for a valid id.
func idRule(id string) string {
	switch {
	case id == "":
		return "an id is not empty"
	case len(id) > MaxIDLen:
		return fmt.Sprintf("an id is at most %d characters", MaxIDLen)
	case strings.Contains(id, ".."):
		return "an id never contains .."
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.', c == '/':
		case c == ':':
			return "an id never contains ':'"
		default:
			return "an id is lowercase letters, digits, '-', '.' and '/' between segments"
		}
	}
	for seg := range strings.SplitSeq(id, "/") {
		switch {
		case seg == "":
			return "'/' goes between segments: an id neither starts nor ends with it, nor doubles it"
		case seg[0] == '.' || seg[0] == '-':
			return "no segment of an id starts with '.' or '-'"
		}
	}
	return ""
}

// APIVersion is the job shape this module writes and the only one a
// client may send, and the api_version of every line of jobs.jsonl and
// attempts.jsonl (ADR 0044): a line stamped with another is refused. An added
// field does not move it, since a record's [Schema] carries unknown members
// through a rewrite.
const APIVersion apiversion.APIVersion = apigroup.Job

// ResultAPIVersion is the result shape a holder files, the only one
// the decoder accepts.
const ResultAPIVersion = apigroup.JobResult

// WriteProof is what the fork could prove about a job's write paths against the other
// live jobs bound to the SAME CHECKOUT, or not bound to any yet, at the moment it was
// declared.
//
// Recorded rather than enforced, with one exception (write paths covering a workspace-load
// file, which is refused outright). Two workers overlapping is a call only the
// orchestrator can make: it may have sequenced them, or split one file deliberately. What
// nobody could do before is READ that call back afterwards, so a plan full of overlapping
// write paths and a plan whose write paths were checked looked identical.
type WriteProof string

const (
	// WriteProofAlone is a fork made when no other live job with write paths was bound to
	// the checkout or still waiting to be taken in it. There was nothing to be disjoint
	// FROM, which is not the same claim as disjoint and is why it is its own value.
	WriteProofAlone WriteProof = "alone"
	// WriteProofDisjoint is a job whose write paths intersect no other such job's here.
	WriteProofDisjoint WriteProof = "disjoint"
	// WriteProofOverlapping is one whose write paths intersect another's. The fork stands;
	// the job says so.
	WriteProofOverlapping WriteProof = "overlapping"
)

// LeaseSource is which channel answered the lease a call acts under. Every source but
// [LeaseSourceEnv] is a record the caller wrote or a flag the caller's wiring passed; the
// environment is the process's claim about itself, so it answers only when nothing else
// does.
type LeaseSource string

const (
	// LeaseSourceFlag is an explicit --lease on the call.
	LeaseSourceFlag LeaseSource = "flag"
	// LeaseSourceAgent is the job the guard recorded for the caller its host named: a
	// subagent a spawn attributed or that took the job, or a session that took it.
	// Named for the subagent case it began with; a session with no agent id is recorded
	// the same way.
	LeaseSourceAgent LeaseSource = "agent"
	// LeaseSourceMarker is the checkout's record, which the guard writes when a caller
	// whose host names no session or agent takes the job, and only such a caller
	// reads.
	LeaseSourceMarker LeaseSource = "marker"
	// LeaseSourceContested is a record answering while a lower source named a different
	// lease. The record's lease is the one graded; the other is ignored.
	LeaseSourceContested LeaseSource = "contested"
	// LeaseSourceEnv is the lease member of the process's BAGGAGE.
	LeaseSourceEnv LeaseSource = "env"
)

// Result is the result a holder files for a job.
type Result struct {
	Schema               `yaml:",inline"`
	Findings             []string               `json:"findings,omitempty" yaml:"findings,omitempty"`
	Job                  string                 `json:"job,omitempty" yaml:"job,omitempty"`
	ChangedPaths         []string               `json:"changed_paths" yaml:"changed_paths"`
	Validation           ResultValidation       `json:"validation,omitzero" yaml:"validation,omitempty"`
	VerificationEvidence []VerificationEvidence `json:"verification_evidence,omitempty" yaml:"verification_evidence,omitempty"`
	Descendants          []string               `json:"descendants,omitempty" yaml:"descendants,omitempty"`
	UnresolvedRisks      []string               `json:"unresolved_risks" yaml:"unresolved_risks"`
	// FollowUps is work the holder explicitly leaves for later. Each item
	// stands until the job's Deferred answers it.
	FollowUps []string `json:"follow_ups,omitempty" yaml:"follow_ups,omitempty"`
	// ObservedGeneration is the job's Generation when the result was filed, stamped by the
	// store rather than taken from the holder.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
}

// VerificationEvidence links a postcondition to captured output, or says why its
// run is owed instead. Owed names the reason, and the integration that merges
// the job's branch runs the postcondition.
type VerificationEvidence struct {
	GateID    string `json:"gate_id" yaml:"gate_id"`
	OutputRef string `json:"output_ref" yaml:"output_ref"`
	Owed      string `json:"owed,omitempty" yaml:"owed,omitempty"`
	// Unrun is the cause that kept the postcondition's run from running in this
	// environment. It files a failed attempt: `job wait` fails it.
	Unrun string `json:"unrun,omitempty" yaml:"unrun,omitempty"`
}

// ResultValidation cites the output for a job's primary check, or names in Owed why its
// run is owed to the integration, as [VerificationEvidence] does.
type ResultValidation struct {
	Command   string `json:"command" yaml:"command"`
	OutputRef string `json:"output_ref" yaml:"output_ref"`
	Owed      string `json:"owed,omitempty" yaml:"owed,omitempty"`
	// Unrun is the environment cause that kept the check from running, as
	// on [VerificationEvidence].
	Unrun string `json:"unrun,omitempty" yaml:"unrun,omitempty"`
}

// Status reports the result of verifying a job.
type Status struct {
	Job             string               `json:"job" yaml:"job"`
	Verified        bool                 `json:"verified" yaml:"verified"`
	Violations      []string             `json:"violations,omitempty" yaml:"violations,omitempty"`
	UnresolvedRisks []string             `json:"unresolved_risks,omitempty" yaml:"unresolved_risks,omitempty"`
	Command         string               `json:"command,omitempty" yaml:"command,omitempty"`
	Gates           []VerificationStatus `json:"gates,omitempty" yaml:"gates,omitempty"`
	// Notes are what the grade passed over and says so rather than dropping silently, such
	// as a changed path the caller maintains itself outside the write paths. They never decide
	// Verified.
	Notes []string `json:"notes,omitempty" yaml:"notes,omitempty"`
	// StaleIndexes are the projects whose symbol index was older than their sources when the
	// job was described, so its symbol postconditions were not graded.
	StaleIndexes []string `json:"stale_indexes,omitempty" yaml:"stale_indexes,omitempty"`
	// Footprint is the declaration each changed line of the job's diff since its checkpoint
	// lands in. It decides Verified only for a job claiming declarations (see
	// FootprintUnclaimed); for any other job it is a report. FootprintKnown says the
	// regions were computed, so an empty Footprint means "touched no declaration" rather
	// than "nobody looked", and FootprintReason says why they were not.
	Footprint       []RegionChange `json:"footprint,omitempty" yaml:"footprint,omitempty"`
	FootprintKnown  bool           `json:"footprint_known" yaml:"footprint_known"`
	FootprintReason string         `json:"footprint_reason,omitempty" yaml:"footprint_reason,omitempty"`
	// FootprintUnclaimed are the footprint's locations, as Location strings, in a file the
	// job claims only by declaration (`run.go#executeStages`) that none of those claims
	// names. A file's Preamble is exempt: every job that adds an import lands there. Each
	// is also a violation.
	FootprintUnclaimed []string `json:"footprint_unclaimed,omitempty" yaml:"footprint_unclaimed,omitempty"`
	// Entries are the job's recorded entries, so a footprint line in an entered path reads
	// as the entrant's write rather than the holder's.
	Entries []Entry `json:"entries,omitempty" yaml:"entries,omitempty"`
	// LinesAdded and LinesDeleted sum the Footprint's new and old sides, so they mean
	// something only when FootprintKnown does.
	LinesAdded   int `json:"lines_added" yaml:"lines_added"`
	LinesDeleted int `json:"lines_deleted" yaml:"lines_deleted"`
	// UndeclaredExports are the exported Go symbols the diff added that no symbol postcondition of
	// the job or an ancestor names, and LeftUnreferenced the symbols that lost their last
	// use in the diff and are still defined. Both are reports, never violations, and mean
	// something only when SymbolDeltaKnown does.
	UndeclaredExports []string `json:"undeclared_exports,omitempty" yaml:"undeclared_exports,omitempty"`
	LeftUnreferenced  []string `json:"left_unreferenced,omitempty" yaml:"left_unreferenced,omitempty"`
	SymbolDeltaKnown  bool     `json:"symbol_delta_known" yaml:"symbol_delta_known"`
	// ObservedGeneration is the job's Generation when it was graded. PriorGeneration is the
	// oldest generation among the evidence that was taken against an earlier spec, zero when
	// all of it is current: such evidence never passes a wait.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
	PriorGeneration    int `json:"prior_generation,omitzero"    yaml:"prior_generation,omitempty"`
}

// Clean reports whether the holder's result left no risk unresolved.
func (s Status) Clean() bool { return len(s.UnresolvedRisks) == 0 }

// VerificationStatus reports verification of one postcondition.
type VerificationStatus struct {
	ID        string `json:"id" yaml:"id"`
	Verified  bool   `json:"verified" yaml:"verified"`
	OutputRef string `json:"output_ref,omitempty" yaml:"output_ref,omitempty"`
	// Scope is the `--` args the run behind OutputRef narrowed its go test with, "" when it
	// ran every package and test its check names.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`
	// Owed is the caller's reason the postcondition's run is owed to the integration rather than
	// cited, "" for a postcondition graded on a run.
	Owed       string   `json:"owed,omitempty" yaml:"owed,omitempty"`
	Violations []string `json:"violations,omitempty" yaml:"violations,omitempty"`
}

// CapturedRun is the stored record for one captured run.
type CapturedRun struct {
	Found       bool   `json:"found" yaml:"found"`
	Ref         string `json:"ref,omitempty" yaml:"ref,omitempty"`
	TimestampMs int64  `json:"timestamp_ms" yaml:"timestamp_ms,omitempty"`
	Project     string `json:"project,omitempty" yaml:"project,omitempty"`
	Target      string `json:"target,omitempty" yaml:"target,omitempty"`
	Spell       string `json:"spell,omitempty" yaml:"spell,omitempty"`
	Failed      bool   `json:"failed" yaml:"failed,omitempty"`
	// AttemptID names this execution alone, where Ref may name its step, which a later run
	// of the same step takes over.
	AttemptID string `json:"attempt_id,omitempty" yaml:"attempt_id,omitempty"`
	// DurationMs is how long the run took; TimestampMs is when it ENDED, so the run started
	// at TimestampMs-DurationMs.
	DurationMs int64 `json:"duration_ms" yaml:"duration_ms,omitempty"`
	// Revision is the VCS revision the run read its inputs at, empty when unknown. Dirty says
	// the tree also held uncommitted changes then, so Revision alone does not name what it read.
	Revision string `json:"revision,omitempty" yaml:"revision,omitempty"`
	Dirty    bool   `json:"dirty" yaml:"dirty,omitempty"`
	// Lease is the job lease the run acted under, empty for a run under none, such as a
	// check the orchestrator runs itself.
	Lease string `json:"lease,omitempty" yaml:"lease,omitempty"`
	// ExtraArgs are the args the run forwarded after `--`. Nil when it forwarded none or the
	// record predates the field, so only a non-nil value is compared with a check's args.
	ExtraArgs []string `json:"extra_args,omitempty" yaml:"extra_args,omitempty"`
	// ObservedGeneration is the job's Generation when the store recorded the run against it.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
}

// PostconditionAttempt is the stored output record for one postcondition.
type PostconditionAttempt struct {
	PostconditionID string      `json:"postcondition_id" yaml:"postcondition_id"`
	Attempt         CapturedRun `json:"attempt" yaml:"attempt"`
}

// Paths is a job's declared boundary. Write and Deny bound what its lease
// writes, and are empty on a read-only lease by design. Read widens what it
// reads, each entry to its project's dependencies. Read is its own list
// because a worker that must read a shared library must not be handed the
// right to write it.
type Paths struct {
	// Write entries of the form `<file>#<declaration>` claim one
	// declaration of the file (see SplitClaim).
	Write []string `json:"write,omitempty" yaml:"write,omitempty" schema:"writepath"`
	Read  []string `json:"read,omitempty" yaml:"read,omitempty" schema:"writepath"`
	Deny  []string `json:"deny,omitempty" yaml:"deny,omitempty" schema:"writepath"`
	// Soft are the Write entries that overlap nothing until the diff
	// touches them. The store then drops them from Soft.
	Soft []string `json:"soft,omitempty" yaml:"soft,omitempty" schema:"writepath"`
}

// Job is one lease in an orchestrating agent's ledger. It holds what that
// agent DECLARED about a piece of work it handed out.
//
// FACTS ONLY, NEVER ENFORCEMENT. Paths.Write and Paths.Deny are intent, not a
// boundary this store checks. The agent guard reads them to grade a write.
type Job struct {
	// Requires names the features a build must implement to write
	// this job. Store-computed; the record keeps it as an annotation.
	Requires []string `json:"requires,omitempty" yaml:"requires,omitempty"`
	// Unknown names the record members this build does not declare,
	// as spec.<key>, status.<key> or <key>. A rewrite keeps them.
	Unknown []string `json:"unknown,omitempty" yaml:"unknown,omitempty"`
	// ID is the lease's identity within the plan, and the key Update upserts on. The
	// console joins its drawer entries to agent activity by this value. An
	// orchestrator should use the same id it puts in the worker's prompt.
	ID string `json:"id" yaml:"id"`
	// Parent is the id of the lease this one was handed out under, empty for a lease the
	// root spawned. Depth is read off this chain rather than stored, so a mis-stamped depth
	// cannot disagree with the tree.
	Parent string `json:"parent,omitempty" yaml:"parent,omitempty"`
	// Agent is the agent section: the requested model, the instructions and
	// the worker. Nil is ordinary work, and nothing graded reads it.
	Agent *Agent `json:"agent,omitzero" yaml:"agent,omitempty"`
	// Checkpoint is the working state this lease was handed, in the form
	// the caller's checkpoint token: the revision, plus a dirty-patch digest
	// when the tree was not clean. A string, the form an orchestrator has at
	// spawn time.
	Checkpoint string `json:"checkpoint,omitempty" yaml:"checkpoint,omitempty"`
	// Paths are the declared write and read boundary. See [Paths].
	Paths Paths `json:"paths,omitzero" yaml:"paths,omitempty"`
	// DependsOn are the ids of leases that must land before this one, so a reader can
	// see the ordering the orchestrator committed to.
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// Integrates names the finished branches an integration job merges into its checkout,
	// with `git merge --no-ff`. Naming any makes this an integration job. Its
	// write paths are the files those branches changed. A live lease on one is
	// a note, not a refusal.
	Integrates []string `json:"integrates,omitempty" yaml:"integrates,omitempty"`
	// Breaks are the flags, JSON keys, exported symbols or stored formats the root approved
	// this job breaking. Empty means the job breaks nothing on its own authority: a break
	// outside the list is a finding for the root, not a decision for the worker.
	Breaks []string `json:"breaks,omitempty" yaml:"breaks,omitempty"`
	// ModelTier is the tier the store assigned for the requested model, and
	// why. Store-computed like Releases. Nil means the workspace declared no
	// model tiers when the job was written.
	ModelTier *tier.Assignment `json:"model_tier,omitzero" yaml:"model_tier,omitempty"`
	// BriefFormat is the format a brief to this job's worker takes, ahead of
	// the workspace's own default. Empty defers to it.
	BriefFormat BriefFormat `json:"brief_format,omitempty" yaml:"brief_format,omitempty"`
	// Delivery is how the person chose to deliver this job's work, one of
	// Deliveries; empty means nobody chose. Workspace policy judges it.
	Delivery string `json:"delivery,omitempty" yaml:"delivery,omitempty"`
	// ParentHead is the branch a stacked job's pull request is based on.
	ParentHead string `json:"parent_head,omitempty" yaml:"parent_head,omitempty"`
	// Follows is the job this one continues: follow-up work on what that job's worker wrote.
	// Agent.Worker says whether that warm worker takes it (see the guard's
	// spawn-warm-worker).
	Follows string `json:"follows,omitempty" yaml:"follows,omitempty"`
	// ReworkRounds counts work sent to this job's holder after it handed back.
	// Widenings counts writes that added paths after a holder took the job.
	// Both are store-computed.
	ReworkRounds int `json:"rework_rounds" yaml:"rework_rounds,omitempty"`
	Widenings    int `json:"widenings"  yaml:"widenings,omitempty"`
	// Claims are the write paths this attempt's holder added with a proof the store
	// re-proved, oldest first. Store-computed like ModelTier; a fork of the next attempt
	// starts it over.
	Claims []Claim `json:"claims,omitempty" yaml:"claims,omitempty"`
	// Generation counts changes to the declared spec, as Kubernetes'
	// metadata.generation does. Store-computed. A result, attempt or grade
	// taken against an earlier Generation reads as stale; zero is unknown.
	Generation int `json:"generation" yaml:"generation,omitempty"`
	// MaxAttempts caps Attempts, as a Kubernetes Job's backoffLimit does.
	// Zero is no cap. Checked only when the job is forked again; the store never
	// retries a job itself.
	MaxAttempts int `json:"max_attempts" yaml:"max_attempts,omitempty"`
	// MaxClaims caps how many proved claims one attempt takes, chosen by whoever declares the
	// job; zero takes the default. A fork raising it past jobs.max_claims is refused.
	MaxClaims int `json:"max_claims" yaml:"max_claims,omitempty"`
	// Deferred answers the items the job's result left for later.
	Deferred []Deferred `json:"deferred,omitempty" yaml:"deferred,omitempty"`
	// LeftUnmerged is the stated reason the job's work stays off the merge
	// branch. It exempts the job's work and its deferred items.
	LeftUnmerged string `json:"left_unmerged,omitempty" yaml:"left_unmerged,omitempty"`
	// Landed says a landing probe ended the job because its work reads the same
	// on the base or in an integration. EndReason says where, for a person.
	Landed bool `json:"landed" yaml:"landed,omitempty"`
	// Weight is a lean, never a guarantee: a run bound to the job carries it on its
	// budget.Claim. In (0,1]; zero reads as DefaultWeight.
	Weight float64 `json:"weight,omitzero" yaml:"weight,omitempty"`
	// Paused is the unix second a person paused the job, zero while it is not. A claim the
	// holder already has finishes; the caller refuses its next run.
	Paused int64 `json:"paused,omitzero" yaml:"paused,omitempty"`
	// PausedBy is who paused the job, PauseReason why, and PausedUntil the unix second the
	// pause lapses, zero for none (see [Job.PausedAt]). Set with Paused and cleared with it.
	PausedBy    Origin `json:"paused_by,omitzero"    yaml:"paused_by,omitempty"`
	PauseReason string `json:"pause_reason,omitempty" yaml:"pause_reason,omitempty"`
	PausedUntil int64  `json:"paused_until,omitzero" yaml:"paused_until,omitempty"`
	// Rejection is the last grade that refused the live try,
	// which leaves State where it was. Store-computed. The tries are
	// [Attempt] records beside the job.
	Rejection *Rejection `json:"rejection,omitzero" yaml:"rejection,omitempty"`
	// Check is the one check this lease runs, as the store's [CheckSpec] kept it; the zero
	// Check is none. See [WithCheckSpec].
	Check Check `json:"check,omitzero"`
	// Validation is Check rendered as the command that runs it, by the store that keeps
	// the row. Nothing in this module writes or reads it: [Declaration.Apply] leaves it as
	// it was, so a store that keeps it renders it from Check on every write.
	Validation string `json:"validation,omitempty" yaml:"validation,omitempty"`
	// Postconditions are the job's definition of done that the store grades: a check that passed, paths
	// or symbols this job changed, added or removed. Check is graded as one more postcondition. Named
	// postconditions rather than gates so the word never collides with the CI gate.
	Postconditions []Postcondition `json:"postconditions,omitempty" yaml:"postconditions,omitempty"`
	// State is the job's lifecycle position. See State for why no_return is
	// its own value.
	State State `json:"state,omitempty" yaml:"state,omitempty"`
	// ReadOnly marks the abbreviated job the skill describes: it gathers
	// evidence and writes nothing, so empty WritePaths and DenyPaths are
	// correct rather than missing.
	ReadOnly bool `json:"read_only" yaml:"read_only,omitempty"`
	// Releases are the paths this lease gave up, each with the content digest the path
	// carried at that moment. Store-computed and output-only, like the timestamps: a
	// worker announces a release by shrinking WritePaths, and the digest is what the
	// next agent needs to tell whether it inherited the file the releaser left.
	Releases []Release `json:"releases,omitempty" yaml:"releases,omitempty"`
	// Unattributed are paths this lease owns that somebody outside it wrote,
	// newest last, at most maxUnattributedWrites and one entry per path. The
	// AGENT GUARD records them. Store-computed.
	Unattributed []UnattributedWrite `json:"unattributed,omitempty" yaml:"unattributed,omitempty"`
	// Entries are the acknowledged writes into this job's write paths by
	// somebody other than its holder, oldest first, at most two per job.
	// Store-computed like Releases.
	Entries []Entry `json:"entries,omitempty" yaml:"entries,omitempty"`
	// ReleaseRequests are refused forks asking the holder to release write
	// paths. Store-computed, and here so a hook reads them without a fold.
	ReleaseRequests []ReleaseRequest `json:"release_requests,omitempty" yaml:"release_requests,omitempty" buzz:"-"`
	// WriteProof is what the fork could prove about this job's write paths against the
	// other live jobs bound to the checkout it was declared in. Store-computed and
	// output-only like Releases: it is a fact about the plan at one instant, and a caller
	// that could assert it could assert the proof it stands for. See [WriteProof].
	WriteProof WriteProof `json:"write_proof,omitempty" yaml:"write_proof,omitempty"`
	// Deviations are the permissible problems the declaration accepted, copied from it.
	Deviations []Deviation `json:"deviations,omitempty" yaml:"deviations,omitempty"`
	// Validated is when the store last validated the whole record, in unix
	// seconds; zero on a job never validated. Store-stamped like Created.
	Validated int64 `json:"validated" yaml:"validated,omitempty"`
	// ReportedBase is the checkpoint token the lease's WORKER reported it actually landed
	// on, in the same checkpoint form Checkpoint holds. Checkpoint is
	// what the orchestrator handed out; this is what the worker found. Two fields rather
	// than one overwritten in place, because a single value could never disagree with
	// itself and the disagreement is the fact worth recording.
	ReportedBase string `json:"reported_base,omitempty" yaml:"reported_base,omitempty"`
	// BaseVerdict compares the two, computed by the store at the moment the worker
	// registered and kept as the fact it was then. Empty until a lease registers, which is
	// why there is no vocabulary member for "never registered": an absent verdict is not
	// a judgment, and inventing one would be the mistake StateNoReturn exists to avoid.
	BaseVerdict BaseVerdict `json:"base_verdict,omitempty" yaml:"base_verdict,omitempty"`
	// RegisteredBy is where the write that CREATED this job came from.
	// The store stamps it on the first write. Distinct from Registered, when a
	// WORKER reported its base.
	RegisteredBy Origin `json:"registered_by,omitzero" yaml:"registered_by,omitempty"`
	// Authority is the revision a leased call is judged from (ADR 0045).
	// Store-computed at fork from who forked. Empty refuses leased work.
	Authority string `json:"authority,omitempty" yaml:"authority,omitempty"`
	// Registered is unix seconds, stamped by the store on the write that recorded
	// ReportedBase, off the same clock read as Updated. No write door accepts it from a
	// caller, for the reason Created and Updated do not: a client-supplied timestamp is a
	// fact about the client's clock.
	Registered int64 `json:"registered" yaml:"registered,omitempty"`
	// HolderStopped is unix seconds, set when the holder's turn ended
	// with the job open, and cleared by its next hook call. It ends nothing.
	HolderStopped int64 `json:"holder_stopped" yaml:"holder_stopped,omitempty"`
	// CheckoutRoot is the absolute root of the checkout the holder took this job in,
	// stamped by the store on that write. Once the directory is gone, nobody can be
	// holding the job, and the store ends it.
	CheckoutRoot string `json:"checkout_root,omitempty" yaml:"checkout_root,omitempty"`
	// ForkedIn is the absolute root of the checkout its fork ran in. The
	// store stamps it, "" when it names no root.
	ForkedIn string `json:"forked_in,omitempty" yaml:"forked_in,omitempty"`
	// Worktree is the checkout a fork made or adopted for this job (see [Declaration.Checkout]), nil
	// when the job works wherever it is taken. Store-computed like CheckoutRoot: only the
	// fork that made it and the removal at the job's end write it.
	Worktree *Worktree `json:"worktree,omitzero" yaml:"worktree,omitempty"`
	// EndReason is why the job ended as no_return: the sweep's reason, or which
	// end a holder's or a person's exit was. Store-computed; see
	// job.Store.List for the sweep's rules.
	EndReason string `json:"end_reason,omitempty" yaml:"end_reason,omitempty"`
	// Created and Updated are unix seconds, stamped by the store. Updated is
	// the job's heartbeat, moved only by the job's own writes. Guard notes
	// leave it alone.
	Created int64 `json:"created" yaml:"created"`
	Updated int64 `json:"updated" yaml:"updated"`
	// Deadline is unix seconds past which the guard denies this lease's writes,
	// zero for no bound. A fork's timeout stamps it. A taken job past it ends
	// as no_return on the next read; a prune ends an untaken one.
	Deadline int64 `json:"deadline" yaml:"deadline,omitempty"`
	// Attempt is the run record behind the live try's result, resolved
	// in the holder's OWN checkout, so a waiter in another checkout
	// reopens it. The result is on the [Attempt].
	Attempt *CapturedRun `json:"attempt,omitzero" yaml:"attempt,omitempty"`
	// PostconditionAttempts are the portable output-store snapshots for every explicit postcondition.
	// Attempt remains the snapshot for the primary Check.
	PostconditionAttempts []PostconditionAttempt `json:"postcondition_attempts,omitempty" yaml:"postcondition_attempts,omitempty"`
	// LastRun is the job's most recent run, nil until one is submitted. The
	// invocation id lands at submit, the cost and outcome when the run ends.
	LastRun *Run `json:"last_run,omitzero" yaml:"last_run,omitempty"`
	// Integration is the latest grade of this job's check postconditions in another tree, nil until
	// a grade in an integration records one. Store-computed like Result, and kept
	// beside State rather than moving it: a job that passed where it was written and fails
	// once merged is two facts, and one field could hold only the later.
	Integration *Integration `json:"integration,omitzero" yaml:"integration,omitempty"`
	// WaiterEvidence names the postconditions whose passing run came from the waiter's checkout
	// rather than the holder's, nil unless the waiter took some. It sits
	// beside PostconditionAttempts, which keep the holder's own attempts untouched: a postcondition that
	// failed where the work was written and passed once re-run here is two facts. Each
	// Gates entry is one postcondition, the ref taken for it and its verdict; Checkout and At say
	// where and when.
	//
	// A Integration because the record is the same one --integration keeps: postconditions graded
	// against runs in another checkout. What differs is only that these verdicts moved the
	// job's state, so the record exists only for a job that passed.
	WaiterEvidence *Integration `json:"waiter_evidence,omitzero" yaml:"waiter_evidence,omitempty"`
}

// Worktree is the checkout a fork made or adopted for one job: the job's holder takes
// it there and nowhere else. One the fork made (Made) the caller removes once
// the job ends unless removing it would lose work.
type Worktree struct {
	// Root is the checkout's top level, absolute.
	Root string `json:"root" yaml:"root"`
	// Source is the checkout the fork made it from, which removes it: an hg share or a jj
	// workspace is recorded only there.
	Source string `json:"source" yaml:"source"`
	// Base is the full revision it was made at.
	Base string `json:"base" yaml:"base"`
	// Created is unix seconds.
	Created int64 `json:"created" yaml:"created"`
	// Binary is the runner executable the fork placed in the checkout, the same file the
	// forking checkout runs, "" when the job runs whatever its PATH finds.
	Binary string `json:"binary,omitempty" yaml:"binary,omitempty"`
	// BinaryDigest pins Binary's bytes ("sha256:<hex>"), "" for none, so a reader can
	// refuse a Binary that no longer hashes to it.
	BinaryDigest string `json:"binary_digest,omitempty" yaml:"binary_digest,omitempty"`
	// Removed is unix seconds when the caller removed it, zero while it stands.
	Removed int64 `json:"removed" yaml:"removed,omitempty"`
	// Kept is why the caller left it standing after the job ended, "" until then or once it is
	// removed.
	Kept string `json:"kept,omitempty" yaml:"kept,omitempty"`
	// Made is true when the job's fork created the checkout, the one kind the caller
	// removes once the job ends. A checkout the fork adopted, or one recorded
	// before Made existed, reads false and stands until its maker removes it.
	Made bool `json:"made" yaml:"made,omitempty"`
}

// Standing reports whether the checkout has not been removed: it binds its job's holder,
// and the guard treats it as its job's own.
func (w *Worktree) Standing() bool { return w != nil && w.Removed == 0 }

// Integration is a job's check postconditions graded against runs recorded in Checkout, the tree
// an integration branch was merged into, at At (unix seconds).
type Integration struct {
	Checkout string               `json:"checkout" yaml:"checkout"`
	At       int64                `json:"at" yaml:"at"`
	Verified bool                 `json:"verified" yaml:"verified"`
	Gates    []VerificationStatus `json:"gates,omitempty" yaml:"gates,omitempty"`
	// ObservedGeneration is the job's Generation when the grade was taken.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
}

// Rejection is a grade that refused the live attempt, At unix seconds.
type Rejection struct {
	// Submission numbers the results within an attempt from one.
	Submission         int                  `json:"submission,omitempty" yaml:"submission,omitempty"`
	Unmet              []VerificationStatus `json:"unmet,omitempty"               yaml:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"          yaml:"violations,omitempty"`
	At                 int64                `json:"at"                            yaml:"at"`
	ObservedGeneration int                  `json:"observed_generation,omitzero"  yaml:"observed_generation,omitempty"`
}

// Denial is one guard denial of a job's holder, Ts unix seconds. VerdictRef reopens the
// guard's verdict.
type Denial struct {
	Rule       string `json:"rule"                  yaml:"rule"`
	Reason     string `json:"reason,omitempty"      yaml:"reason,omitempty"`
	Action     string `json:"action,omitempty"      yaml:"action,omitempty"`
	VerdictRef string `json:"verdict_ref,omitempty" yaml:"verdict_ref,omitempty"`
	Ts         int64  `json:"ts"                    yaml:"ts"`
}

// Deferred answers one item of a job's result. Item is its exact text.
// Exactly one of Job, the declared job carrying it, and Reason, why it is
// dismissed, is set.
type Deferred struct {
	Item   string `json:"item"             yaml:"item"`
	Job    string `json:"job,omitempty"    yaml:"job,omitempty" schema:"leaseid"`
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// Unmerged is one fact against a job: work off the merge branch, or a
// deferred item nothing answers. Head is empty when no commit could be read.
type Unmerged struct {
	Job  string `json:"job"            yaml:"job"`
	Head string `json:"head,omitempty" yaml:"head,omitempty"`
	// Item is the deferred item's text, empty for a fact about the work.
	Item   string `json:"item,omitempty" yaml:"item,omitempty"`
	Why    string `json:"why"            yaml:"why"`
	Remedy string `json:"remedy"         yaml:"remedy"`
}

// UnmergedReport is what the checkout's branch does not hold. Unknown says
// why the branch could not be read: Jobs then holds deferred items only, and
// a job absent from it is not thereby merged. Risks are unanswered unresolved
// risks: listed for reading, and no work the branch lacks.
type UnmergedReport struct {
	Branch  string     `json:"branch,omitempty"  yaml:"branch,omitempty"`
	Jobs    []Unmerged `json:"jobs,omitempty"    yaml:"jobs,omitempty"`
	Risks   []Unmerged `json:"risks,omitempty"   yaml:"risks,omitempty"`
	Unknown string     `json:"unknown,omitempty" yaml:"unknown,omitempty"`
}

// ForkFacts is what a fork leaves waiting: finished branches off the base
// and commits since the last full test. It holds facts only, and repo policy
// decides what they stop. BranchesUnknown and TestUnknown say why a side went
// unread.
type ForkFacts struct {
	// FinishedBranches names each as `<branch> (job <job>, <n> commit(s))`.
	FinishedBranches []string `json:"finished_branches,omitempty" yaml:"finished_branches,omitempty"`
	BranchesUnknown  string   `json:"branches_unknown,omitempty"  yaml:"branches_unknown,omitempty"`
	// IntegrateRecord is the JSON job record an integration fork of them takes.
	IntegrateRecord  string `json:"integrate_record,omitempty"   yaml:"integrate_record,omitempty"`
	CommitsSinceTest int    `json:"commits_since_test"           yaml:"commits_since_test"`
	LastTestRef      string `json:"last_test_ref,omitempty"      yaml:"last_test_ref,omitempty"`
	LastTestRevision string `json:"last_test_revision,omitempty" yaml:"last_test_revision,omitempty"`
	TestUnknown      string `json:"test_unknown,omitempty"       yaml:"test_unknown,omitempty"`
}

// Lines renders the report for a person, one line per fact, none when there
// is nothing to say.
func (r UnmergedReport) Lines() []string {
	var out []string
	if r.Unknown != "" {
		out = append(out, "not merged: unknown, "+r.Unknown)
	}
	if len(r.Jobs) > 0 {
		out = append(out, fmt.Sprintf("not merged onto %s: %d", cmp.Or(r.Branch, "the branch"), len(r.Jobs)))
	}
	for _, u := range r.Jobs {
		switch {
		case u.Item != "":
			out = append(out, fmt.Sprintf("  %s: %q: %s; %s", u.Job, u.Item, u.Why, u.Remedy))
		case u.Head != "":
			out = append(out, fmt.Sprintf("  %s at %s: %s; %s", u.Job, u.Head, u.Why, u.Remedy))
		default:
			out = append(out, fmt.Sprintf("  %s: %s; %s", u.Job, u.Why, u.Remedy))
		}
	}
	return out
}

// Overdue reports whether a live job has passed its deadline at now, in unix
// seconds. A terminal job has no writes left to bound, so it is never overdue.
func (j Job) Overdue(now int64) bool {
	return j.Deadline > 0 && j.State.Live() && now >= j.Deadline
}

// EndReasonSessionEnded opens the end_reason of a job the store ended because
// the agent session holding it ended. It is the one ending a take of the job
// reverses. A host may resume a session it reported over, and the resumed
// session's next exec of the job rebinds it.
const EndReasonSessionEnded = "its holder's session ended"

// EndedWithSession reports whether the job is no_return only because its
// holder's session ended (see [EndReasonSessionEnded]).
func (j Job) EndedWithSession() bool {
	return j.State == StateNoReturn && strings.HasPrefix(j.EndReason, EndReasonSessionEnded)
}

// SpecChangedSince reports whether a record stamped with generation observed was taken
// against an earlier declared spec than the job's. A zero on either side is unknown,
// never a change.
func (j Job) SpecChangedSince(observed int) bool {
	return observed != 0 && j.Generation != 0 && observed != j.Generation
}

// IdleSince is when job's jobs.stale_after clock started, in unix seconds.
// A declared job nobody took is QUEUED while a dependency is still editing, so
// waiting is true. Otherwise the clock is the later of its last update and its
// dependencies'. A dependency no job declares holds nothing back.
func IdleSince(rows []Job, row Job) (since int64, waiting bool) {
	since = row.Updated
	if row.State != StateDeclared || row.Registered != 0 {
		return since, false
	}
	for _, dep := range row.DependsOn {
		i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == dep })
		switch {
		case i < 0:
		case rows[i].State.Editing():
			return 0, true
		default:
			since = max(since, rows[i].Updated)
		}
	}
	return since, false
}

// StaleAt reports whether job has been idle at least after as of now, in
// unix seconds, with its clock read by [IdleSince]. A queued job is never
// stale, and a zero or negative after is never.
func StaleAt(rows []Job, row Job, now int64, after time.Duration) bool {
	since, waiting := IdleSince(rows, row)
	return !waiting && after > 0 && now-since >= int64(after/time.Second)
}

// quoteJoin renders a closed set for an error that has to list what it would accept.
func quoteJoin[T ~string](items []T, sep string) string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = strconv.Quote(string(item))
	}
	return strings.Join(out, sep)
}

// Run is one completed run of a job: what it cost, whether it worked, and what it
// reclaimed.
type Run struct {
	// Invocation is the run's invocation id, and it can only be recorded at SUBMIT: the
	// completion callback carries argv, duration and error and no invocation, so a run
	// recorded only at the end could never name the log it produced.
	Invocation string `json:"invocation,omitempty" yaml:"invocation,omitempty"`
	// Ended is unix milliseconds and DurationMs is the measured duration, so the run's
	// start is their difference.
	Ended      int64 `json:"ended"       yaml:"ended,omitempty"`
	DurationMs int64 `json:"duration_ms" yaml:"duration_ms,omitempty"`
	// OK is false when the run errored, and Error is its text. Never omitempty: a run that
	// failed and a run nobody recorded must not read the same.
	OK    bool   `json:"ok"              yaml:"ok"`
	Error string `json:"error,omitempty" yaml:"error,omitempty"`
	// ItemsRemoved and BytesReclaimed are the run's own delta, reported only by the jobs
	// that measure one. Zero also means "this job does not count", so neither is evidence
	// that a rotate found nothing to drop.
	ItemsRemoved   int64 `json:"items_removed"   yaml:"items_removed,omitempty"`
	BytesReclaimed int64 `json:"bytes_reclaimed" yaml:"bytes_reclaimed,omitempty"`
}

// Digests that are not a content hash. A digest is `sha256:<hex>` of the file's bytes
// when the path held one; these say why it could not be, so a reader is never handed a
// hash-shaped value that is not a hash. Named for the FIELD they land in
// (Release.Digest) rather than for releases, which they do not classify.
const (
	// DigestAbsent is a path with nothing on disk when it was released: a file the
	// lease deleted, or a declared glob, which is a pattern rather than a path.
	DigestAbsent = "absent"
	// DigestDir is a directory. A tree has no single content digest, and hashing one
	// on every put would walk it, so the next agent is told to go look instead.
	DigestDir = "dir"
	// DigestUnreadable is a path that IS there and could not be hashed: unreadable,
	// not a regular file, or too large to hash under the store's lock. Distinct from
	// DigestAbsent because "the releaser deleted it" and "something is there nobody
	// could read" send the next agent to different places.
	DigestUnreadable = "unreadable"
)

// Release is one path a lease stopped owning, and the version of it the
// next agent inherits.
//
// The skill has workers release a contested path as soon as they finish EDITING it
// rather than at exit, so a waiter can start against it during validation. Digest is
// what makes that safe to act on: it identifies the file the releaser left behind, and
// a mismatch at verification time means the waiter built on a tree the releaser never
// saw.
type Release struct {
	Path   string `json:"path"   yaml:"path"`
	Digest string `json:"digest" yaml:"digest"`
	// ReleasedAt is unix seconds, stamped by the store on the put that dropped the path.
	ReleasedAt int64 `json:"released_at" yaml:"released_at"`
	// Revoked is a path taken from a job a holder had taken, by a writer other than that
	// holder: the orchestrator revoking it rather than the worker giving it up.
	Revoked bool `json:"revoked" yaml:"revoked,omitempty"`
	// TakenBy is the job the store granted the path to because this job's holder
	// was dead, and DeadBecause why the caller judged it dead. Both are empty on any
	// other release.
	TakenBy     string `json:"taken_by,omitempty"     yaml:"taken_by,omitempty"`
	DeadBecause string `json:"dead_because,omitempty" yaml:"dead_because,omitempty"`
	// Narrowed marks a file the store narrowed to the holder's claims.
	// Its entry stays in paths.write; a re-fork ends the narrowing.
	Narrowed bool `json:"narrowed" yaml:"narrowed,omitempty"`
}

// PausedAt reports whether a person's pause holds the job at now, unix seconds.
// Past PausedUntil it has lapsed: the holder runs again, and the sweep and
// a prune may end the job.
func (j Job) PausedAt(now int64) bool {
	return j.Paused != 0 && (j.PausedUntil == 0 || now < j.PausedUntil)
}

// UnattributedWrite is one path a lease owns that somebody outside it wrote, and
// the content that writer left behind.
//
// The inverse of Release: a release is a worker saying "I am done with this, here is
// what I left", and this is the caller saying "somebody who is not you changed this, here is
// what is there now", so a lease that read the file earlier can find out by ASKING.
//
// UNATTRIBUTED is the honest word: the caller knows only that the writer named no live lease,
// so a person editing in their own checkout and an agent that forgot to export its id are
// indistinguishable here. Naming a human would be a claim nobody here can support.
type UnattributedWrite struct {
	Path string `json:"path"   yaml:"path"`
	// Digest is the content AFTER the write, on the same three-marker vocabulary as
	// Release.Digest: a hash, or DigestAbsent / DigestDir / DigestUnreadable.
	Digest string `json:"digest" yaml:"digest"`
	// At is unix seconds, stamped by the store.
	At int64 `json:"at" yaml:"at"`
}

// Entry is a party other than a job's holder acknowledging, on the record, that it
// will write one path inside the job's write paths. The guard lets exactly one such
// write through and stamps Consumed.
type Entry struct {
	Path string `json:"path" yaml:"path"`
	// By is where the acknowledging call came from, stamped by the store.
	By Origin `json:"by" yaml:"by"`
	// At and Consumed are unix seconds; Consumed is zero until the write lands.
	At       int64 `json:"at" yaml:"at"`
	Consumed int64 `json:"consumed" yaml:"consumed,omitempty"`
}

// ReleaseRequest is a refused fork asking a job's holder to release File.
type ReleaseRequest struct {
	// ID is the attention request filed beside it (see sessions.RequestID).
	ID   string `json:"id" yaml:"id"`
	File string `json:"file" yaml:"file"`
	// Wanted are the fork's claims on File.
	Wanted []string `json:"wanted,omitempty" yaml:"wanted,omitempty"`
	// By is the lease the fork was made under, empty for an unbound fork.
	By      string `json:"by,omitempty" yaml:"by,omitempty"`
	Message string `json:"message" yaml:"message"`
	// At is unix seconds, stamped by the store.
	At int64 `json:"at" yaml:"at"`
}

// Integrating reports whether the job names branches to merge.
func (j Job) Integrating() bool { return len(j.Integrates) > 0 }

// validIntegratedBranch reports whether b can name a branch an integration job merges: no
// leading dash, which git would read as an option, and nothing git refuses in a ref name.
func validIntegratedBranch(b string) bool {
	return b != "" && !strings.HasPrefix(b, "-") && !strings.HasSuffix(b, ".lock") && !strings.HasSuffix(b, "/") &&
		!strings.Contains(b, "..") && !strings.Contains(b, "@{") &&
		!strings.ContainsFunc(b, func(r rune) bool { return r <= ' ' || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r) })
}

// HardPaths is WritePaths without the soft entries: the paths the job owns
// against other jobs. A reader asking who owns a path reads this. A reader
// asking what this job may write reads WritePaths.
func (j Job) HardPaths() []string {
	if len(j.Paths.Soft) == 0 {
		return j.Paths.Write
	}
	return slices.DeleteFunc(slices.Clone(j.Paths.Write), func(p string) bool { return slices.Contains(j.Paths.Soft, p) })
}

// features are the Requires entries this build implements, sorted. A
// job requiring another is read and listed, and only writing it is
// refused.
func features() []string {
	return []string{"claim-declarations", "dead-job-end", "job-generations", "signature-postconditions"}
}

// Unmet returns the entries of Requires that this build does not implement,
// in job order, or nil when this build can write the job.
func (j Job) Unmet() []string {
	var out []string
	for _, r := range j.Requires {
		if !slices.Contains(features(), r) {
			out = append(out, r)
		}
	}
	return out
}

// ServerRun reports a job the server declared and runs itself, from its
// maintenance catalog: no agent section, registered by the server.
func (j Job) ServerRun() bool {
	return j.Agent == nil && j.RegisteredBy.EntryPoint == EntryPointServer
}

// Instructions is the agent section's prose, empty for ordinary work.
func (j Job) Instructions() string {
	if j.Agent == nil {
		return ""
	}
	return j.Agent.Instructions
}

// HandedOutAfresh is j, an ended row declared again, with nothing of the holder that took
// it last: a checkout left on it would refuse every new taker, and its grade would stand in
// that checkout. A fork is graded on this row, so the write and the grade share it.
func (j Job) HandedOutAfresh() Job {
	j.ReportedBase, j.BaseVerdict, j.Registered, j.CheckoutRoot = "", "", 0, ""
	j.ReleaseRequests = nil
	return j
}

// Clone returns a deep copy: no slice, map or pointer it holds is shared with u, so a
// value handed out of a store is safe to keep. Nil stays nil, so a job that stored null
// does not come back as []. Its postconditions come back resolved (see [Postcondition.Resolve]).
func (j Job) Clone() Job {
	c := j
	c.Requires = slices.Clone(j.Requires)
	c.Unknown = slices.Clone(j.Unknown)
	c.Paths.Write = slices.Clone(j.Paths.Write)
	c.Paths.Deny = slices.Clone(j.Paths.Deny)
	c.Paths.Soft = slices.Clone(j.Paths.Soft)
	c.Paths.Read = slices.Clone(j.Paths.Read)
	c.DependsOn = slices.Clone(j.DependsOn)
	c.Integrates = slices.Clone(j.Integrates)
	c.Breaks = slices.Clone(j.Breaks)
	c.Postconditions = clonePostconditions(j.Postconditions)
	c.Releases = slices.Clone(j.Releases)
	c.Claims = slices.Clone(j.Claims)
	c.Unattributed = slices.Clone(j.Unattributed)
	c.Entries = slices.Clone(j.Entries)
	c.ReleaseRequests = slices.Clone(j.ReleaseRequests)
	for i, r := range c.ReleaseRequests {
		c.ReleaseRequests[i].Wanted = slices.Clone(r.Wanted)
	}
	c.Deviations = slices.Clone(j.Deviations)
	c.Deferred = slices.Clone(j.Deferred)
	c.Agent = j.Agent.Clone()
	if j.ModelTier != nil {
		assigned := *j.ModelTier
		assigned.Shared = slices.Clone(j.ModelTier.Shared)
		c.ModelTier = &assigned
	}
	c.Check = j.Check
	if j.Attempt != nil {
		attempt := *j.Attempt
		attempt.ExtraArgs = slices.Clone(j.Attempt.ExtraArgs)
		c.Attempt = &attempt
	}
	c.PostconditionAttempts = slices.Clone(j.PostconditionAttempts)
	for i := range c.PostconditionAttempts {
		c.PostconditionAttempts[i].Attempt.ExtraArgs = slices.Clone(c.PostconditionAttempts[i].Attempt.ExtraArgs)
	}
	if j.LastRun != nil {
		run := *j.LastRun
		c.LastRun = &run
	}
	c.Integration = cloneIntegration(j.Integration)
	c.WaiterEvidence = cloneIntegration(j.WaiterEvidence)
	if j.Worktree != nil {
		wt := *j.Worktree
		c.Worktree = &wt
	}
	if j.Rejection != nil {
		r := j.Rejection.clone()
		c.Rejection = &r
	}
	return c
}

// cloneVerificationStatuses is a deep copy of gates, nil for nil.
func cloneVerificationStatuses(gates []VerificationStatus) []VerificationStatus {
	out := slices.Clone(gates)
	for i := range out {
		out[i].Violations = slices.Clone(out[i].Violations)
	}
	return out
}

// cloneIntegration is a deep copy of g, nil for nil.
func cloneIntegration(g *Integration) *Integration {
	if g == nil {
		return nil
	}
	c := *g
	c.Gates = cloneVerificationStatuses(g.Gates)
	return &c
}

// HoldsNoBuildSeat reports whether the worker for u can never take a build
// seat. It writes nothing and declares no check, so it runs no target.
func (j Job) HoldsNoBuildSeat() bool {
	if !j.ReadOnly || len(j.Paths.Write) > 0 {
		return false
	}
	return !slices.ContainsFunc(j.EffectivePostconditions(), func(g Postcondition) bool { return g.Resolve().Kind == PostconditionKindCheck })
}

// Schema is the envelope a job record embeds. A reader ignores the members it does not
// know and refuses only what it cannot act on; a writer carries those members back out.
//
// Embedded, never named: json inlines it, so api_version stays a top-level member. It
// has no JSON methods of its own, since an embedded method would be promoted to the
// record and decode the envelope alone; [Result] and [Declaration] carry them instead.
type Schema struct {
	// Version is the format this record is written in. A reader refuses
	// any other, so no reader acts on a record it cannot read.
	Version apiversion.APIVersion `json:"api_version" yaml:"api_version" buzz:"apiVersion"`
	// Requires names the features a reader must implement to act on this record at all.
	// A reader lacking one treats the record as read-only; the records around it stay
	// usable. Set only for content an older reader would act on wrongly, never for an
	// added field it can ignore.
	Requires []string `json:"requires,omitempty" yaml:"requires,omitempty"`
	// unknown holds every member the record's type does not declare, re-emitted unchanged
	// when the record is written back. The record's MarshalJSON and UnmarshalJSON keep it,
	// under encoding/json and encoding/json/v2 alike; [Schema.Unknown] names them.
	unknown map[string]codec.RawMessage `codec:"unknown"`
}

// Unknown names the members the record's type does not declare, sorted: what a newer
// writer stored, which this build carries back out unchanged. Nil when there are none.
func (s Schema) Unknown() []string { return slices.Sorted(maps.Keys(s.unknown)) }

// Unmet returns the entries of Requires that known does not name, in record order, or
// nil when this reader can act on the record.
func (s Schema) Unmet(known []string) []string {
	var out []string
	for _, r := range s.Requires {
		if !slices.Contains(known, r) {
			out = append(out, r)
		}
	}
	return out
}

func (s Schema) clone() Schema {
	s.Requires = slices.Clone(s.Requires)
	s.unknown = maps.Clone(s.unknown)
	return s
}

// MarshalJSON writes the result with the members it does not declare that it kept.
func (r Result) MarshalJSON() ([]byte, error) {
	type plain Result
	return codec.MarshalWithUnknown(plain(r), r.unknown)
}

// UnmarshalJSON reads the result, keeping the members it does not declare.
func (r *Result) UnmarshalJSON(data []byte) error {
	type plain Result
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*r = Result(p)
	r.unknown = unknown
	return nil
}

// orList joins codes for a sentence: "a", "a or b", "a, b or c".
func orList(codes []Code) string {
	names := make([]string, len(codes))
	for i, c := range codes {
		names[i] = string(c)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
