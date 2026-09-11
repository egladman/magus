package types

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// JobState is where one lease stands. The three terminal values are
// the point of the set: a row that never reaches one is a row nobody closed.
//
// NoReturn is deliberately distinct from Fail. A worker that died, stalled, or was
// killed produced no verdict at all, and folding that into "failed" claims a judgment
// nobody made: the root agent still has to go look. Silence is not a pass, and it is
// not a failure either.
type JobState string

const (
	// StateDeclared is a row written before its worker was spawned.
	StateDeclared JobState = "declared"
	// StateRunning is a holder in flight.
	StateRunning JobState = "running"
	// StateExited is a job its holder returned, with a result filed and nobody waiting on
	// it yet. The POSIX reading exactly: the child is done and its status has not been
	// collected, which is neither "still working" nor any judgment of the work.
	StateExited JobState = "exited"
	// StatePass is a job whose acceptance criteria and assigned check both
	// passed, as judged by the agent that owns it.
	StatePass JobState = "pass"
	// StateFail is a lease that returned and did not meet its criteria.
	StateFail JobState = "fail"
	// StateNoReturn is a lease that never reported: dead, stalled, or cancelled.
	StateNoReturn JobState = "no_return"
)

// JobStates is the closed set, in lifecycle order. The vocabulary lives beside the
// constants because four readers quote it (the row decoder, the put merge, the published
// schema, and the error each of them raises) and a closed set that drifts is one a client
// is rejected by for a value the schema told it to send.
func JobStates() []JobState {
	return []JobState{StateDeclared, StateRunning, StateExited, StatePass, StateFail, StateNoReturn}
}

// ValidJobState reports whether s is one of [JobStates]. An empty state is NOT: a row
// that has not said where it stands carries none, and the callers that allow that test
// for it themselves.
func ValidJobState(s JobState) bool { return slices.Contains(JobStates(), s) }

// JobHolder is who runs a job. One store holds both kinds, so a reader can tell the
// daemon's own housekeeping from work a session was handed without asking a second door.
type JobHolder string

const (
	// HolderDaemon is a job from the daemon's maintenance catalog: the daemon declares it,
	// submits it, and runs it itself.
	HolderDaemon JobHolder = "daemon"
	// HolderSession is work an orchestrator declared for somebody else to hold.
	HolderSession JobHolder = "session"
)

// OrSession reads an empty holder as HolderSession: every row written before the two kinds
// shared a store was a session's, so absent means session rather than unknown.
func (h JobHolder) OrSession() JobHolder {
	if h == "" {
		return HolderSession
	}
	return h
}

// LeaseCheck is the one check a lease runs, in the parts the output store records a run
// by: the target (carrying a `spell::` filter when the row named one), the project it runs
// in, and whatever is forwarded past `--`.
//
// A RECORD rather than the command line it renders to. A line has to be parsed before it
// can be compared with a stored run, and a parser that reads positions binds
// `magus run -o json test .` to a target named json.
type LeaseCheck struct {
	// Target is the magus target the check runs, without the `magus run` in front of it.
	Target string `json:"target"             yaml:"target"`
	// Project is the project the target runs in, "." when the row names none.
	Project string `json:"project,omitempty"  yaml:"project,omitempty"`
	// Args are forwarded to the tool after `--`, never to magus.
	Args []string `json:"args,omitempty"     yaml:"args,omitempty"`
}

// String renders the check as the command that runs it.
func (c LeaseCheck) String() string {
	project := c.Project
	if project == "" {
		project = "."
	}
	line := "magus run " + c.Target + " " + project
	if len(c.Args) > 0 {
		line += " -- " + strings.Join(c.Args, " ")
	}
	return line
}

// ParseLeaseCheck reads `<target> <project> [-- args]`, the shape a person types and the
// shape a record is written in. The project defaults to ".".
//
// A flag anywhere is REFUSED rather than read as a word: a check is an identity the output
// store can be asked about, and `-o json` in it either names a target nobody ran or
// silently shifts the project.
func ParseLeaseCheck(s string) (LeaseCheck, error) {
	words := strings.Fields(s)
	var args []string
	if i := slices.Index(words, "--"); i >= 0 {
		words, args = words[:i], words[i+1:]
	}
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			return LeaseCheck{}, fmt.Errorf("a check is `<target> <project> [-- args]` and %q carries the flag %s;"+
				" flags belong after `--`, where they reach the tool rather than magus", s, w)
		}
	}
	switch {
	case len(words) == 0:
		return LeaseCheck{}, errors.New("a check is `<target> <project> [-- args]` and this one names no target")
	case len(words) > 2:
		return LeaseCheck{}, fmt.Errorf("a check is `<target> <project> [-- args]` and %q carries %d words before any `--`", s, len(words))
	}
	c := LeaseCheck{Target: words[0], Project: ".", Args: args}
	if len(words) == 2 {
		c.Project = words[1]
	}
	c.Project = path.Clean(c.Project)
	return c, nil
}

// ParseLeaseRunLine reads a rendered `[magus] run <target> <project> [-- args]` back into a
// record.
//
// compat(until: no client still sends a rendered validation line; observe: a grep of the
// ledger archives under the per-repository state dir for a row carrying `validation` and
// no `check`): the line is what rows declared before the check record existed.
func ParseLeaseRunLine(s string) (LeaseCheck, error) {
	words := strings.Fields(s)
	if len(words) > 0 && words[0] != "run" {
		words = words[1:]
	}
	if len(words) == 0 || words[0] != "run" {
		return LeaseCheck{}, fmt.Errorf("a check line is `magus run <target> <project> [-- args]` and %q is not one", s)
	}
	return ParseLeaseCheck(strings.Join(words[1:], " "))
}

// JobBaseVerdict says how the base a worker reported at registration compares
// with the Checkpoint its lease was handed. A FACT computed at that moment, never a
// refusal: a diverged worker is registered like any other and told what diverged.
//
// The middle value is why this is not a boolean. A checkpoint is a revision PLUS a
// dirty-patch digest, so two trees can share a revision and hold different uncommitted
// work; that is neither agreement nor the kind of divergence a respawn fixes, and folding
// it into either one sends the worker to the wrong remedy.
type JobBaseVerdict string

const (
	// BaseMatch is a reported base identical to the checkpoint, digest included.
	BaseMatch JobBaseVerdict = "match"
	// BaseRevisionMatch is the same revision carrying a different uncommitted patch.
	BaseRevisionMatch JobBaseVerdict = "revision-match"
	// BaseDiverged is a different revision: the worker is not on the tree it was handed.
	BaseDiverged JobBaseVerdict = "diverged"
	// BaseUnknown is a registration with nothing to compare against, because the lease was
	// declared without a Checkpoint. Distinct from BaseMatch on the same ground
	// StateNoReturn is distinct from StateFail: claiming agreement nobody observed is a
	// judgment the ledger did not make.
	BaseUnknown JobBaseVerdict = "unknown"
)

// Terminal reports whether the lease is done, however it ended. It keeps a finished lease
// out of the overlap report below, and it is what `ledger accept` reads to refuse
// re-grading a row somebody already graded.
func (s JobState) Terminal() bool {
	return s == StatePass || s == StateFail || s == StateNoReturn
}

// Live reports whether the lease can still act on its paths: declared, running, or
// exited. A job with no state is not live, it has not said it is; that is the one rule the
// guard and the sandbox both scope a holder by, so it lives here rather than in either.
//
// EXITED IS LIVE, which reads oddly next to Terminal and is the safe direction. A holder
// that filed its result still holds the lease on its checkout, and a verification that
// rejects sends it back to the same lanes; dropping the job out of live here would leave
// every write after `job exit` graded by nothing at all.
func (s JobState) Live() bool {
	return s == StateDeclared || s == StateRunning || s == StateExited
}

// MaxJobIDLen bounds a lease id: long enough for a branch-shaped ledger name, short
// enough that the id stays a correlation key rather than a payload riding every event
// line.
const MaxJobIDLen = 128

// ValidJobID reports whether id may be stamped as a lease: letters, digits
// and the separators -_./: a ledger row or a branch-shaped lease name uses, never empty,
// at most [MaxJobIDLen] characters.
//
// The narrowness is a security property, not a naming preference. A lease id is EXEMPT
// from the redaction internal/trail applies to every other event field, so every channel
// that can stamp one (a lease marker, the BAGGAGE environment channel, a
// producer's own field) has to pass its candidate through here first, or the exemption
// becomes a way to carry a credential onto an event line.
//
// It lives beside [Job] rather than in the package that redacts, because the
// ledger and the trail are two readers of one id: a validator owned by either would
// leave the other free to accept an id the first would refuse.
func ValidJobID(id string) bool {
	if id == "" || len(id) > MaxJobIDLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == '/', c == ':':
		default:
			return false
		}
	}
	return true
}

// JobSchemaVersion is the version of the row shape this magus writes and accepts. It
// is stamped on every stored row and required on every row a client sends, so a decoder
// meeting a shape it does not know says which versions it supports instead of rejecting
// one field at a time.
//
// BUMP IT ON EVERY CHANGE TO THE FIELD SET, an added optional field included. This used to
// say an added optional field does not need one; measured 2026-09-11, that is exactly how
// two live rows silently lost write_paths, read_paths, deny_paths, model and check to an
// older binary's non-strict decoder, because those fields were added without moving this
// constant, so the rows still read schema_version 1 and the loss was invisible. A reader
// that quietly ignores a field it does not know is correct only until something else reads
// its rewrite; the version is what tells such a reader to stop instead of proceeding.
// TestJobSchemaVersionCoversEveryField pins the field set this version describes against a
// golden list, so a field added without a bump fails a test instead of failing a store.
const JobSchemaVersion = 4

// JobActor identifies the session that wrote a row: the same pair the trail records
// for an agent's actions, so a row and the actions that followed it join on one identity.
//
// Both halves are often empty, and that is a fact rather than a gap: a person writing a
// row from a terminal carries no session id and no host.
type JobActor struct {
	// Session is the acting session's id, as the host or the W3C trace channel names it.
	Session string `json:"session,omitempty" yaml:"session,omitempty"`
	// Host is the agent host that produced the write, as its own wrapper named itself.
	Host string `json:"host,omitempty" yaml:"host,omitempty"`
}

// Job is one row of an orchestrating agent's lease ledger: what that
// agent DECLARED about a piece of work it handed out, recorded so a human can see the
// plan the agents are running.
//
// FACTS ONLY, NEVER ENFORCEMENT, and the division is precise rather than a blanket "magus
// does nothing with these". WritePaths and DenyPaths are
// what an orchestrator said it intended, not a boundary this store checks: nothing here
// blocks a write, gates a run, or refuses a call. The AGENT GUARD is what consults these
// facts to grade a write, and it lives outside this package and READS this store; a guard
// verdict is its own, not the ledger's. BaseVerdict is the shape that division takes on a
// row: registration computes it, records it, and hands it back, and it refuses nothing;
// the caller and the orchestrator decide what a divergence is worth.
//
// Why enforcement lives outside rather than here: a store that quietly started refusing
// would make the ledger something agents route around instead of something they keep
// honestly, and a ledger nobody keeps honestly grades nothing.
//
// The field set mirrors the ledger table in the magus-multi-agent skill one-for-one, so a
// row an agent writes down and a row it records here cannot describe the same lease
// differently.
//
// Registered in cmd/magus-utils/boundary_types.go as a RuntimeObject, so magus\ledger.put
// and magus\ledger.list return one. VCSCheckpoint stays unregistered: Checkpoint is a
// plain string here, the form an orchestrator has at spawn time.
type Job struct {
	// SchemaVersion is the shape this row was written in, stamped by the store on every
	// write and never taken from a client. See JobSchemaVersion.
	SchemaVersion int `json:"schema_version" yaml:"schema_version"`
	// ID is the lease's identity within the plan, and the key Update upserts on. The
	// console joins its drawer rows to agent activity by this value, so an
	// orchestrator should use the same id it puts in the worker's prompt.
	ID string `json:"id" yaml:"id"`
	// Parent is the id of the lease this one was handed out under, empty for a lease the
	// root spawned. Depth is read off this chain rather than stored, so a mis-stamped depth
	// cannot disagree with the tree.
	Parent string `json:"parent,omitempty" yaml:"parent,omitempty"`
	// Goal is the lease's goal and its observable acceptance criteria, as one block of
	// text. Not split into two fields: the skill requires criteria to be observable and
	// a separate empty Criteria field would read as "none required" rather than as
	// "the author did not write any".
	Goal string `json:"goal,omitempty" yaml:"goal,omitempty"`
	// Checkpoint is the working state this lease was handed, in the form
	// `magus vcs checkpoint -o name` prints: the revision, plus a dirty-patch digest
	// when the tree was not clean. A string rather than an embedded VCSCheckpoint
	// because that is the form an orchestrator has at spawn time and the form a later
	// reader feeds back to `magus graph diff --rev`.
	Checkpoint string `json:"checkpoint,omitempty" yaml:"checkpoint,omitempty"`
	// WritePaths and DenyPaths are the declared write boundary. Empty on a
	// read-only lease BY DESIGN (see ReadOnly), which is why neither is required.
	WritePaths []string `json:"write_paths,omitempty" yaml:"write_paths,omitempty"`
	DenyPaths  []string `json:"deny_paths,omitempty" yaml:"deny_paths,omitempty"`
	// ReadPaths is the declared READ lane: the paths whose projects this lease may read,
	// widened to those projects' own dependencies when the guard resolves it. Empty
	// means WritePaths stands in, because a worker leased to edit a project is a
	// worker that was pointed at that project.
	//
	// A separate field rather than a wider WritePaths, and the separation is the
	// point: a worker that has to READ a shared library must not be handed the right
	// to WRITE it, and one list cannot say both. It is also the only way to widen the
	// boundary, which is deliberate. An environment variable that switched the rule
	// off would be set once, in a wrapper, by the first worker it inconvenienced, and
	// nothing afterwards would say the lane had stopped being checked.
	ReadPaths []string `json:"read_paths,omitempty" yaml:"read_paths,omitempty"`
	// DependsOn are the ids of leases that must land before this one, so a reader can
	// see the ordering the orchestrator committed to.
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// Model is the model or effort tier the work was matched to (principal, standard,
	// economy in the skill's table). A free string: hosts name their models differently
	// and a closed set here would force a lie for the ones that do not fit.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
	// Check is the one check this lease runs. See [LeaseCheck].
	Check *LeaseCheck `json:"check,omitempty" yaml:"check,omitempty"`
	// Validation is Check rendered as the command that runs it.
	//
	// compat(until: no reader still reads the rendered line; observe: grep for
	// `.Validation` outside internal/ledger, which is cmd/magus/guard_gate.go's gate
	// ownership rule and the console's plan view today): the store writes it from Check
	// on every put so the two cannot disagree, and a client that sends it instead of
	// Check is still understood.
	Validation string `json:"validation,omitempty" yaml:"validation,omitempty"`
	// State is the row's lifecycle position. See JobState for why no_return is
	// its own value.
	State JobState `json:"state,omitempty" yaml:"state,omitempty"`
	// Holder is who runs this job. See [JobHolder]; empty reads as HolderSession.
	Holder JobHolder `json:"holder,omitempty" yaml:"holder,omitempty"`
	// ReadOnly marks the abbreviated row the skill describes: a lease that gathers
	// evidence and writes nothing has no write set, so empty WritePaths and
	// DenyPaths are correct rather than missing. Without this flag a reader
	// cannot tell an abbreviated row from one whose author forgot the boundary.
	ReadOnly bool `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	// Releases are the paths this lease gave up, each with the content digest the path
	// carried at that moment. Store-computed and output-only, like the timestamps: a
	// worker announces a release by shrinking WritePaths, and the digest is what the
	// next agent needs to tell whether it inherited the file the releaser left.
	Releases []JobRelease `json:"releases,omitempty" yaml:"releases,omitempty"`
	// Unattributed are paths this lease owns that somebody outside it wrote, newest last,
	// at most MaxUnattributedWrites of them and one row per path.
	//
	// Store-computed and output-only like Releases, and recorded by the AGENT GUARD, which is
	// the only thing positioned to notice: it already grades every write against these declared
	// boundaries and already tells the writer to coordinate. It threw the observation away
	// afterwards, so the lease on the other side (the one whose file moved) was the one
	// party never told.
	Unattributed []JobUnattributedWrite `json:"unattributed,omitempty" yaml:"unattributed,omitempty"`
	// ReportedBase is the checkpoint token the lease's WORKER reported it actually landed
	// on, in the same `magus vcs checkpoint -o name` form Checkpoint holds. Checkpoint is
	// what the orchestrator handed out; this is what the worker found. Two fields rather
	// than one overwritten in place, because a single value could never disagree with
	// itself and the disagreement is the fact worth recording.
	ReportedBase string `json:"reported_base,omitempty" yaml:"reported_base,omitempty"`
	// BaseVerdict compares the two, computed by the store at the moment the worker
	// registered and kept as the fact it was then. Empty until a lease registers, which is
	// why there is no vocabulary member for "never registered": an absent verdict is not
	// a judgment, and inventing one would be the mistake StateNoReturn exists to avoid.
	BaseVerdict JobBaseVerdict `json:"base_verdict,omitempty" yaml:"base_verdict,omitempty"`
	// RegisteredBy is the session that CREATED this row, stamped by the store on the
	// first write and carried unchanged afterwards. It is the row's provenance, and the
	// store names it when it refuses a mutation from somebody else's worker.
	//
	// Distinct from Registered below, which is when a WORKER reported the base it landed
	// on: one says who declared the work, the other when somebody turned up to do it.
	RegisteredBy JobActor `json:"registered_by,omitempty" yaml:"registered_by,omitempty"`
	// Registered is unix seconds, stamped by the store on the write that recorded
	// ReportedBase, off the same clock read as Updated. No write door accepts it from a
	// caller, for the reason Created and Updated do not: a client-supplied timestamp is a
	// fact about the client's clock.
	Registered int64 `json:"registered,omitempty" yaml:"registered,omitempty"`
	// Created and Updated are unix seconds, stamped by the store on write and
	// output-only to callers: a client-supplied timestamp is a fact about the client's
	// clock, not about when the row was recorded.
	//
	// Updated is the row's heartbeat. A lease that re-puts its row on every state change
	// keeps it moving; a row nobody touches goes stale, and a reader may then judge the
	// lease possibly dead. That judgment is the READER'S: nothing here transitions a row
	// on its own, and silence has no verdict in it.
	Created int64 `json:"created" yaml:"created"`
	Updated int64 `json:"updated" yaml:"updated"`
	// Result is what the holder filed when it exited, and Attempt is the run record behind
	// that result's output ref, resolved in the holder's OWN checkout.
	//
	// Both are on the job because an output store belongs to a cache dir: a parent waiting
	// from another worktree cannot resolve the holder's ref, so a result that travelled as
	// a file read as evidence from nowhere every time it crossed a checkout. Carrying the
	// run record with the result is what makes the evidence portable, and it is the
	// store's record rather than anything the holder asserts.
	Result  *JobResult  `json:"result,omitempty"  yaml:"result,omitempty"`
	Attempt *JobAttempt `json:"attempt,omitempty" yaml:"attempt,omitempty"`
	// LastRun is the job's most recent completed run, nil until one finishes.
	//
	// The SIZE of what the job maintains is deliberately not stored beside it: a trail, a
	// run log and a cache all grow without the row being written, so a stored figure goes
	// stale in silence. It is measured when the job is listed.
	LastRun *JobRun `json:"last_run,omitempty" yaml:"last_run,omitempty"`
}

// JobRun is one completed run of a job: what it cost, whether it worked, and what it
// reclaimed.
type JobRun struct {
	// Invocation is the run's invocation id, and it can only be recorded at SUBMIT: the
	// completion callback carries argv, duration and error and no invocation, so a run
	// recorded only at the end could never name the log it produced.
	Invocation string `json:"invocation,omitempty" yaml:"invocation,omitempty"`
	// Ended is unix milliseconds and DurationMs is the measured duration, so the run's
	// start is their difference.
	Ended      int64 `json:"ended,omitempty"       yaml:"ended,omitempty"`
	DurationMs int64 `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
	// OK is false when the run errored, and Error is its text. Never omitempty: a run that
	// failed and a run nobody recorded must not read the same.
	OK    bool   `json:"ok"              yaml:"ok"`
	Error string `json:"error,omitempty" yaml:"error,omitempty"`
	// ItemsRemoved and BytesReclaimed are the run's own delta, reported only by the jobs
	// that measure one. Zero also means "this job does not count", so neither is evidence
	// that a rotate found nothing to drop.
	ItemsRemoved   int64 `json:"items_removed,omitempty"   yaml:"items_removed,omitempty"`
	BytesReclaimed int64 `json:"bytes_reclaimed,omitempty" yaml:"bytes_reclaimed,omitempty"`
}

// Digests that are not a content hash. A digest is `sha256:<hex>` of the file's bytes
// when the path held one; these say why it could not be, so a reader is never handed a
// hash-shaped value that is not a hash. Named for the FIELD they land in
// (JobRelease.Digest) rather than for releases, which they do not classify.
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

// JobRelease is one path a lease stopped owning, and the version of it the
// next agent inherits.
//
// The skill has workers release a contested path as soon as they finish EDITING it
// rather than at exit, so a waiter can start against it during validation. Digest is
// what makes that safe to act on: it identifies the file the releaser left behind, and
// a mismatch at verification time means the waiter built on a tree the releaser never
// saw.
type JobRelease struct {
	Path   string `json:"path"   yaml:"path"`
	Digest string `json:"digest" yaml:"digest"`
	// ReleasedAt is unix seconds, stamped by the store on the put that dropped the path.
	ReleasedAt int64 `json:"released_at" yaml:"released_at"`
}

// JobUnattributedWrite is one path a lease owns that somebody outside it wrote, and
// the content that writer left behind.
//
// The inverse of JobRelease: a release is a worker saying "I am done with this, here is
// what I left", and this is magus saying "somebody who is not you changed this, here is
// what is there now", so a lease that read the file earlier can find out by ASKING.
//
// UNATTRIBUTED is the honest word: magus knows only that the writer named no live lease,
// so a person editing in their own checkout and an agent that forgot to export its id are
// indistinguishable here. Naming a human would be a claim magus cannot support.
type JobUnattributedWrite struct {
	Path string `json:"path"   yaml:"path"`
	// Digest is the content AFTER the write, on the same three-marker vocabulary as
	// JobRelease.Digest: a hash, or DigestAbsent / DigestDir / DigestUnreadable.
	Digest string `json:"digest" yaml:"digest"`
	// At is unix seconds, stamped by the store.
	At int64 `json:"at" yaml:"at"`
}

// JobOverlap is two leases whose declared WritePaths intersect. A FACT the
// reader is handed, never a verdict: two leases may share a path because their author
// meant them to run in sequence, or because nobody noticed. Nothing here blocks,
// gates, or reorders anything.
type JobOverlap struct {
	// JobA and JobB are the lease ids, in ledger order: JobA was recorded first.
	JobA string `json:"job_a" yaml:"job_a"`
	JobB string `json:"job_b" yaml:"job_b"`
	// PathsA and PathsB are the intersecting declarations from each side, deduped and
	// kept apart. They are rarely the same string ("internal/ledger" and
	// "internal/ledger/store.go" intersect), so one merged list left a reader unable to
	// tell which lease claimed which, which is the only thing they can act on.
	PathsA []string `json:"paths_a" yaml:"paths_a"`
	PathsB []string `json:"paths_b" yaml:"paths_b"`
}

// JobList is what a reader of the ledger is served: the recorded rows, plus
// the overlaps derived from them. A constructor rather than a literal at each read
// door, because the MCP tool and the console's route must not be able to disagree
// about whether an overlap exists: the same reason types.NewFileReport exists.
type JobList struct {
	Jobs     []Job        `json:"jobs"               yaml:"jobs"`
	Overlaps []JobOverlap `json:"overlaps,omitempty" yaml:"overlaps,omitempty"`
}

// NewJobList wraps the rows and derives the overlaps. Derived on READ and
// never stored: an overlap is a relation between two rows, so storing it on either
// one would mean a row that stopped being true when its neighbor changed.
//
// The registration facts take the opposite route and are NOT derived here. ReportedBase,
// BaseVerdict and Registered describe one row against the checkpoint that row was handed,
// so they belong on the row, are computed once when the worker registers, and reach every
// reader of this list (magus_job's list op, the console's /api/v1/jobs) by riding
// the leases. Deriving a second copy at read time would be a duplicate to keep true, which
// is exactly what the overlap rule above avoids in the other direction.
//
// The single door onto a report, which is why the empty case is normalized HERE: an
// unwritten ledger serves "jobs":[] rather than null, and the MCP tool and the HTTP
// route would otherwise each have to decide that for themselves.
func NewJobList(jobs []Job) JobList {
	if jobs == nil {
		jobs = []Job{}
	}
	return JobList{Jobs: jobs, Overlaps: jobOverlaps(jobs)}
}

// jobOverlaps reports every pair of jobs whose declared write paths
// intersect, in ledger order.
//
// A job in a terminal state is not in any pair. A released or finished job is not
// competing for a path (that is the whole shape of the skill's early-release rule,
// where a worker shrinks its write paths so a waiter can start), and reporting one
// would make the surface noisiest exactly when the plan is winding down.
func jobOverlaps(jobs []Job) []JobOverlap {
	var out []JobOverlap
	for i, a := range jobs {
		if a.State.Terminal() || len(a.WritePaths) == 0 {
			continue
		}
		for _, b := range jobs[i+1:] {
			if b.State.Terminal() || len(b.WritePaths) == 0 {
				continue
			}
			if pa, pb := intersectingPaths(a.WritePaths, b.WritePaths); len(pa) > 0 {
				out = append(out, JobOverlap{JobA: a.ID, JobB: b.ID, PathsA: pa, PathsB: pb})
			}
		}
	}
	return out
}

// intersectingPaths collects the declared paths that cover common ground, each side
// kept in its own list and deduped.
func intersectingPaths(a, b []string) (pathsA, pathsB []string) {
	for _, pa := range a {
		for _, pb := range b {
			if !PathsIntersect(pa, pb) {
				continue
			}
			if !slices.Contains(pathsA, pa) {
				pathsA = append(pathsA, pa)
			}
			if !slices.Contains(pathsB, pb) {
				pathsB = append(pathsB, pb)
			}
		}
	}
	return pathsA, pathsB
}

// PathsIntersect decides whether two DECLARED paths cover common ground: containment on
// the cleaned paths, with a glob truncated to the literal prefix it can be judged by.
//
// THE definition, rather than a copy of one. The overlap report below asks it of two
// leases; the worker brief asks it of a lease's write paths against a project's declared
// output globs, which is the same question about a different pair of declarations. A
// second implementation would differ on the day the truncation rule changed.
//
// Truncating is deliberate over-reporting. "console/src/**/*.ts" and
// "console/src/**/*.css" cover no common file, and this reports them anyway because
// both reduce to "console/src"; deciding whether two arbitrary globs can ever match
// one path is a solver, and a missed collision costs a reader far more than a pair
// they look at and dismiss.
func PathsIntersect(a, b string) bool {
	// An entry that names nothing claims nothing. It cleans to ".", which the whole-tree
	// rule below would then read as a claim on everything, pairing a row that holds one
	// stray blank with every other lease in the plan.
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	a, b = LiteralPrefix(a), LiteralPrefix(b)
	if a == "" || b == "" {
		// A pattern with no literal prefix ("**/*.go") claims the whole tree.
		return true
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// LiteralPrefix is the part of a declared path that names actual directories: the
// cleaned path itself when it holds no glob metacharacter, and everything above the
// first segment that does when it holds one.
//
// Exported because the focus rule asks the same question of the same declarations:
// which project a lease's write_paths land in cannot be answered by a wildcard, and
// two packages deriving that prefix by their own rules would disagree about a
// declaration on the day the rules drifted.
func LiteralPrefix(p string) string {
	p = path.Clean(strings.TrimSpace(p))
	if p == "." || p == "/" {
		return ""
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if strings.ContainsAny(s, "*?[{") {
			return strings.Join(segs[:i], "/")
		}
	}
	return p
}

// Clone returns a deep copy: the slice fields are the only shared state, so copying
// them is what makes a value handed out of a store safe to keep. slices.Clone
// preserves nil, so a row that stored null does not come back as [].
func (u Job) Clone() Job {
	c := u
	c.WritePaths = slices.Clone(u.WritePaths)
	c.DenyPaths = slices.Clone(u.DenyPaths)
	c.ReadPaths = slices.Clone(u.ReadPaths)
	c.DependsOn = slices.Clone(u.DependsOn)
	c.Releases = slices.Clone(u.Releases)
	c.Unattributed = slices.Clone(u.Unattributed)
	if u.Result != nil {
		result := *u.Result
		result.ChangedPaths = slices.Clone(u.Result.ChangedPaths)
		result.Descendants = slices.Clone(u.Result.Descendants)
		result.UnresolvedRisks = slices.Clone(u.Result.UnresolvedRisks)
		c.Result = &result
	}
	if u.Attempt != nil {
		attempt := *u.Attempt
		c.Attempt = &attempt
	}
	if u.LastRun != nil {
		run := *u.LastRun
		c.LastRun = &run
	}
	return c
}
