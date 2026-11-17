package job

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
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

// States is the closed set, in lifecycle order. The vocabulary lives beside the
// constants because four readers quote it: the job decoder, the put merge, the
// published schema, and the error each of them raises. A closed set that drifts
// rejects a client for a value the schema told it to send.
func States() []State {
	return []State{StateDeclared, StateRunning, StateExited, StatePass, StateFail, StateNoReturn}
}

// ValidState reports whether s is one of [States]. An empty state is NOT.
// A job that has not said where it stands carries none, and the callers that
// allow that test for it themselves.
func ValidState(s State) bool { return slices.Contains(States(), s) }

// LeaseCheck is the one check a lease runs, in the parts the output store records a run
// by. The parts are the target, the project it runs in, and whatever
// is forwarded past `--`. The target carries a `spell::` filter when the job
// named one.
//
// A RECORD rather than a command line. A line has to be parsed before it can be compared
// with a stored run, and a parser that reads positions binds `run -o json test .` to a
// target named json. Rendering one as a command is the runner's business.
type LeaseCheck struct {
	// Target is the magus target the check runs, without the `magus run` in front of it.
	// Empty when Script names the check instead.
	Target string `json:"target"             yaml:"target" schema:"target"`
	// Script is a Buzz script the check runs with `magus buzz --record` in place of a
	// target, for a fact no target checks: a passing recorded run of it is the evidence. A
	// check names a target or a script, never both.
	Script string `json:"script,omitempty"   yaml:"script,omitempty"`
	// Project is the project the target runs in, "." when the job names none.
	Project string `json:"project,omitempty"  yaml:"project,omitempty"`
	// Args are forwarded to the tool after `--`, never to magus.
	Args []string `json:"args,omitempty"     yaml:"args,omitempty"`
	// NoDefaultCharms runs the check with --no-default-charms, so only a run made without
	// the workspace's default_charms satisfies it: the charmless `generate` that compares
	// against HEAD, in a workspace whose default is rw.
	NoDefaultCharms bool `json:"no_default_charms,omitzero" yaml:"no_default_charms,omitempty"`
	// Test runs Script's tests with `magus buzz -t` instead of its main: passing tests and
	// a passing main are different facts, so each is satisfied only by its own run. A
	// script check alone carries it.
	Test bool `json:"test,omitzero" yaml:"test,omitempty"`
}

// PrimaryGoalID names the job's singular check when it is projected into its goals. It
// is a stable identity, not a user assertion.
const PrimaryGoalID = "check"

// Goal is one machine-verifiable condition a job must satisfy before it can pass.
// Criteria remains the human-readable objective; goals bind that objective to recorded
// Magus output rather than a holder's boolean attestation. The output must have been
// captured after the job was declared; target execution limits remain the target's run
// policy rather than a second goal timeout.
type Goal struct {
	ID          string   `json:"id"                     yaml:"id"                     schema:"leaseid"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"  yaml:"depends_on,omitempty"  schema:"leaseid"`
	// Kind names WHAT this goal examines and Expect names what must be true of it. Two
	// fields rather than one, because the alternative is a kind per pair: `paths` beside
	// `exists` beside `absent` beside `no-symbol`, which is a vocabulary that grows by
	// multiplication and reads inconsistently the moment it has four members.
	//
	// Both are RESOLVED before a job is stored. A default applied on read is
	// one every reader must know.
	Kind GoalKind `json:"kind" yaml:"kind"`
	// Expect is optional and defaults to the kind's own: passed for check, changed for
	// paths and symbol. The common goal names only its kind and its subject.
	Expect GoalExpect `json:"expect,omitempty" yaml:"expect,omitempty"`
	// Check is the run a GoalKindCheck goal examines. Zero on every other kind.
	Check LeaseCheck `json:"check,omitzero" yaml:"check,omitempty"`
	// Paths are the globs a GoalKindPaths goal examines. Zero on every other kind.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	// Symbols are the names a GoalKindSymbol goal examines, as the knowledge graph
	// resolves them. Zero on every other kind.
	Symbols []string `json:"symbols,omitempty" yaml:"symbols,omitempty"`
	// Signature is the declaration an ExpectSignature goal holds its one symbol to, in the
	// form the symbol index renders it. A fork that declares none records the indexed one,
	// which is how a stub the root wrote becomes the contract. Zero on every other goal.
	Signature string `json:"signature,omitempty" yaml:"signature,omitempty"`
	// Accept are the bases a run of a GoalKindCheck goal's check may rest on (see
	// [Store.Grade]). A check goal names at least one: [Goal.Resolve] fills none in, so
	// one accepting nothing is a fault, never a goal that takes the strongest or the
	// weakest. Zero on every other kind.
	Accept []Basis `json:"accept,omitempty" yaml:"accept,omitempty"`
}

// Resolve fills a goal's kind and expectation from what it declared, so every stored goal
// names both. A goal that named neither is a check, which is what the single Check field
// meant before goals existed; a goal that named a kind and no expectation takes that
// kind's natural one.
//
// Called at the WRITE boundary (Declaration.Apply, job.PutMerge), never on read. That is the whole reason the enums have no empty member.
func (g Goal) Resolve() Goal {
	if g.Kind == "" {
		g.Kind = GoalKindCheck
	}
	if g.Expect == "" {
		g.Expect = g.Kind.defaultExpect()
	}
	return g
}

// GoalKind names WHAT a goal examines, because not every condition a job can be held to is
// a target run.
//
// Each kind names an evidence source magus already holds: the output store, the VCS diff,
// the knowledge graph. A condition magus can observe no evidence for is not declarable
// here on purpose, and there is deliberately no escape hatch for one. A goal accepting an
// unrecorded exit status would be the easiest kind to satisfy falsely, which is the
// attestation the whole mechanism replaces; declare a target and use GoalKindCheck.
type GoalKind string

const (
	// GoalKindCheck examines a recorded run of the goal's check. It is a return code, and
	// what raises it above one is that magus RECORDED it: the output store holds the
	// target, the project, the timestamp and the failure bit, so the run can be reopened
	// and attributed to this job rather than taken on the holder's word.
	GoalKindCheck GoalKind = "check"
	// GoalKindPaths examines files, by glob. It is what "this job must actually produce
	// the migration" looks like when no target can say so.
	GoalKindPaths GoalKind = "paths"
	// GoalKindSymbol examines named symbols in the knowledge graph, which is the
	// granularity below a file: a function added, renamed or deleted is a fact the graph
	// holds even when the file it lives in changed for ten other reasons.
	GoalKindSymbol GoalKind = "symbol"
)

// GoalKinds is the closed set, for the same reason States is one: the validator, the
// published schema and the error each of them raises all quote it, and a vocabulary that
// drifts rejects a client for a value the schema told it to send.
func GoalKinds() []GoalKind {
	return []GoalKind{GoalKindCheck, GoalKindPaths, GoalKindSymbol}
}

// GoalExpect names what must be TRUE of what a goal examines. One vocabulary across every
// kind, so `absent` means the same thing of a file, a symbol and a reference.
type GoalExpect string

const (
	// ExpectPassed is a recorded run that finished without failing. GoalKindCheck only.
	ExpectPassed GoalExpect = "passed"
	// ExpectChanged is "the diff since the job's checkpoint touches this".
	ExpectChanged GoalExpect = "changed"
	// ExpectPresent is "this is here now", whether or not this job is what put it here.
	ExpectPresent GoalExpect = "present"
	// ExpectAbsent is "this is not here now", which is how a deletion or a removal is
	// declared as a condition rather than reported as one.
	ExpectAbsent GoalExpect = "absent"
	// ExpectUnreferenced is "nothing names this any more", and it belongs to symbols. It
	// is the one that answers the remainder a partitioned rename leaks: split the work per
	// project and the callers in no project belong to no job, so every job passes and the
	// rename is unfinished.
	//
	// An EXPECTATION rather than a kind of its own, because the subject it examines is
	// still the symbol. A `refs` kind read the same field as `symbol` and forced every
	// reader to treat the two as one, which is a vocabulary that says it has four members
	// and behaves as though it has three.
	ExpectUnreferenced GoalExpect = "unreferenced"
	// ExpectSignature is "this symbol is declared with this shape": the declaration the
	// goal's Signature names. Go and Buzz compare the parsed shape, so a renamed parameter
	// still holds; other languages compare the text.
	ExpectSignature GoalExpect = "signature"
)

// GoalExpects is the closed set. See GoalKinds.
func GoalExpects() []GoalExpect {
	return []GoalExpect{ExpectPassed, ExpectChanged, ExpectPresent, ExpectAbsent, ExpectUnreferenced, ExpectSignature}
}

// defaultExpect is what a kind means when a goal names no condition, so the common goal of
// each kind declares only its subject. A check is asked whether it passed; files and
// symbols are asked whether this job changed them, which is the question a job is for.
func (k GoalKind) defaultExpect() GoalExpect {
	if k == GoalKindCheck {
		return ExpectPassed
	}
	return ExpectChanged
}

// Subject is what the goal examines, rendered for a message that has to name it.
func (g Goal) Subject() []string {
	switch g.Kind {
	case GoalKindCheck:
		if g.Check.Target == "" && g.Check.Script == "" {
			return nil
		}
		return []string{g.Check.words()}
	case GoalKindPaths:
		return g.Paths
	default:
		return g.Symbols
	}
}

// goalAccepts is the kind-to-condition matrix, and the one place it is written down.
// Anything outside it is refused at declaration rather than graded into a verdict nobody
// can act on: `check` + `absent` has no meaning, and a goal nobody can satisfy reads as a
// job nobody can finish.
var goalAccepts = map[GoalKind][]GoalExpect{
	GoalKindCheck:  {ExpectPassed},
	GoalKindPaths:  {ExpectChanged, ExpectPresent, ExpectAbsent},
	GoalKindSymbol: {ExpectChanged, ExpectPresent, ExpectAbsent, ExpectUnreferenced, ExpectSignature},
}

// EffectiveGoals returns the declared goals plus the primary Check as a goal. Keeping the
// projection at the model boundary means guards that still read Check retain their
// capability semantics while every verifier has one complete collection to grade.
func (j Job) EffectiveGoals() []Goal {
	goals := cloneGoals(j.Goals)
	if j.Check != nil {
		// The lease's own check is run by whoever holds the lease, through the runner that
		// started it, so its run is witnessed; Check carries no accept list of its own.
		primary := Goal{ID: PrimaryGoalID, Check: *j.Check, Accept: []Basis{BasisWitnessed}}.Resolve()
		goals = append([]Goal{primary}, goals...)
	}
	return goals
}

// words is the check in the form [ParseLeaseCheck] reads, `<target> <project> [-- args]`,
// or its script in place of the target and project.
func (c LeaseCheck) words() string {
	line := c.Script
	if line == "" {
		line = c.Target + " " + cmp.Or(c.Project, ".")
	}
	if len(c.Args) > 0 {
		line += " -- " + strings.Join(c.Args, " ")
	}
	return line
}

// SameLeaseCheck reports whether a and b declare the same check. Two nil checks are the
// same, and a nil check is not the same as any declared one. A function over pointers
// rather than a method, because a job's Check is optional and either side may
// be nil.
func SameLeaseCheck(a, b *LeaseCheck) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Target == b.Target && a.Script == b.Script && a.Project == b.Project &&
		a.NoDefaultCharms == b.NoDefaultCharms && a.Test == b.Test && slices.Equal(a.Args, b.Args)
}

// ParseLeaseCheck reads `<target> <project> [-- args]`, the shape a person types and the
// shape a record is written in. The project defaults to ".".
//
// A flag anywhere is REFUSED rather than read as a word: a check is an identity the output
// store can be asked about, and `-o json` in it either names a target nobody ran or
// silently shifts the project.
//
// The one flag read is --no-default-charms, which the record carries as NoDefaultCharms.
func ParseLeaseCheck(s string) (LeaseCheck, error) {
	words := strings.Fields(s)
	var args []string
	if i := slices.Index(words, "--"); i >= 0 {
		words, args = words[:i], words[i+1:]
	}
	var noDefaultCharms bool
	positional := words[:0:0]
	for _, w := range words {
		if w == "--no-default-charms" {
			noDefaultCharms = true
			continue
		}
		positional = append(positional, w)
		if strings.HasPrefix(w, "-") {
			return LeaseCheck{}, &CheckError{Line: s, Reason: fmt.Sprintf("%q carries the flag %s,"+
				" flags belong after `--`, where they reach the tool rather than magus", s, w)}
		}
	}
	words = positional
	switch {
	case len(words) == 0:
		return LeaseCheck{}, &CheckError{Line: s, Reason: "this one names no target"}
	case len(words) > 2:
		return LeaseCheck{}, &CheckError{Line: s, Reason: fmt.Sprintf("%q carries %d words before any `--`", s, len(words))}
	}
	c := LeaseCheck{Target: words[0], Project: ".", Args: args, NoDefaultCharms: noDefaultCharms}
	if len(words) == 2 {
		c.Project = words[1]
	}
	c.Project = path.Clean(c.Project)
	return c, nil
}

// CheckError is a check [ParseLeaseCheck] refuses: the line it was given, and why.
type CheckError struct {
	Line   string
	Reason string
}

func (e *CheckError) Error() string {
	return "a check is `<target> <project> [-- args]` and " + e.Reason
}

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
// see [ValidateID].
//
// The narrowness is a security property: a lease id is EXEMPT from the redaction
// internal/trail applies to every other event field, so every channel that stamps one
// passes its candidate through here first. It lives beside [Job] because the ledger and
// the trail both read the id.
func ValidID(id string) bool { return idRule(id) == "" }

// ValidateID refuses an id that may not name a job, as an [*IDError] naming the rule it
// breaks. The id is also the branch a checkout of the job is made on, so it is held to
// what every VCS accepts as one: lowercase letters, digits, '-' and '.', with '/' only
// between segments, no segment empty or starting with '.' or '-', never ':' or '..', at
// most [MaxIDLen] characters.
func ValidateID(id string) error {
	if rule := idRule(id); rule != "" {
		return &IDError{ID: id, Rule: rule}
	}
	return nil
}

// IDError is an id [ValidateID] refuses, and the rule it breaks.
type IDError struct {
	ID   string
	Rule string
}

func (e *IDError) Error() string { return fmt.Sprintf("%q is not a job id: %s", e.ID, e.Rule) }

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
// client may send. An added field does not move it, since [Schema].Unknown
// carries unknown members through a rewrite. It is untyped so that a
// caller's own version type, as magus's types.APIVersion, takes it as it is.
const APIVersion = apigroup.Job

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
// [LeaseSourceEnv] is a record magus wrote or a flag the caller's wiring passed; the
// environment is the process's claim about itself, so it answers only when nothing else
// does.
type LeaseSource string

const (
	// LeaseSourceFlag is an explicit --lease on the call.
	LeaseSourceFlag LeaseSource = "flag"
	// LeaseSourceAgent is the job the guard recorded for the caller its host named: a
	// subagent a spawn attributed or that ran `magus job exec`, or a session that ran it.
	// Named for the subagent case it began with; a session with no agent id is recorded
	// the same way.
	LeaseSourceAgent LeaseSource = "agent"
	// LeaseSourceMarker is the checkout's record, which the guard writes when a caller
	// whose host names no session or agent runs `magus job exec`, and only such a caller
	// reads.
	LeaseSourceMarker LeaseSource = "marker"
	// LeaseSourceContested is a record answering while a lower source named a different
	// lease. The record's lease is the one graded; the other is ignored.
	LeaseSourceContested LeaseSource = "contested"
	// LeaseSourceEnv is magus.lease in the process's BAGGAGE.
	LeaseSourceEnv LeaseSource = "env"
)

// Result is the result a holder files for a job.
type Result struct {
	Schema               `yaml:",inline"`
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

// VerificationEvidence links a goal to captured Magus output, or says why its
// run is owed instead. Owed names the reason, and the integration that merges
// the job's branch runs the goal.
type VerificationEvidence struct {
	GateID    string `json:"gate_id" yaml:"gate_id"`
	OutputRef string `json:"output_ref" yaml:"output_ref"`
	Owed      string `json:"owed,omitempty" yaml:"owed,omitempty"`
	// Unrun is the cause that kept the goal's run from running in this
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

// OwedNoSeat is the one reason a worker files a check as owed: the machine
// would not seat it, its build budget full or its memory short, and a worker
// waits on neither.
const OwedNoSeat = "no seat"

// OwedRemoteCI is the reason a check is owed when the workspace declares a
// remote CI (ci.remote_gate) that runs it on the pull request.
const OwedRemoteCI = "remote CI runs it"

// Status reports the result of verifying a job.
type Status struct {
	Job             string               `json:"job" yaml:"job"`
	Verified        bool                 `json:"verified" yaml:"verified"`
	Violations      []string             `json:"violations,omitempty" yaml:"violations,omitempty"`
	UnresolvedRisks []string             `json:"unresolved_risks,omitempty" yaml:"unresolved_risks,omitempty"`
	Command         string               `json:"command,omitempty" yaml:"command,omitempty"`
	Gates           []VerificationStatus `json:"gates,omitempty" yaml:"gates,omitempty"`
	// Notes are what the grade passed over and says so rather than dropping silently, such
	// as a changed path magus maintains itself outside the write paths. They never decide
	// Verified.
	Notes []string `json:"notes,omitempty" yaml:"notes,omitempty"`
	// StaleIndexes are the projects whose symbol index was older than their sources when the
	// job was described, so its symbol goals were not graded.
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
	// UndeclaredExports are the exported Go symbols the diff added that no symbol goal of
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

// VerificationStatus reports verification of one goal.
type VerificationStatus struct {
	ID        string `json:"id" yaml:"id"`
	Verified  bool   `json:"verified" yaml:"verified"`
	OutputRef string `json:"output_ref,omitempty" yaml:"output_ref,omitempty"`
	// Scope is the `--` args the run behind OutputRef narrowed its go test with, "" when it
	// ran every package and test its check names.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`
	// Owed is why the goal's run is owed to the integration rather than cited (see
	// [OwedNoSeat]), "" for a goal graded on a run.
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

// GateAttempt is the stored output record for one goal.
type GateAttempt struct {
	GateID  string      `json:"gate_id" yaml:"gate_id"`
	Attempt CapturedRun `json:"attempt" yaml:"attempt"`
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
// The field set mirrors the magus-multi-agent skill's ledger table.
type Job struct {
	// Requires names the features a magus must implement to write
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
	// `magus vcs checkpoint -o name` prints: the revision, plus a dirty-patch digest
	// when the tree was not clean. A string, the form an orchestrator has at
	// spawn time and `magus graph diff --rev` takes.
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
	// the workspace's agents.brief_format. Empty defers to magus.yaml.
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
	// Zero is no cap. Checked only when the job is forked again; magus never
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
	// holder already has finishes; exec and the guard refuse its next magus run.
	Paused int64 `json:"paused,omitzero" yaml:"paused,omitempty"`
	// PausedBy is who paused the job, PauseReason why, and PausedUntil the unix second the
	// pause lapses, zero for none (see [Job.PausedAt]). Set with Paused and cleared with it.
	PausedBy    Origin `json:"paused_by,omitzero"    yaml:"paused_by,omitempty"`
	PauseReason string `json:"pause_reason,omitempty" yaml:"pause_reason,omitempty"`
	PausedUntil int64  `json:"paused_until,omitzero" yaml:"paused_until,omitempty"`
	// Rejection is the last `magus job wait` that refused the live try,
	// which leaves State where it was. Store-computed. The tries are
	// [Attempt] records beside the job.
	Rejection *Rejection `json:"rejection,omitzero" yaml:"rejection,omitempty"`
	// Check is the one check this lease runs. See [LeaseCheck].
	Check *LeaseCheck `json:"check,omitzero" yaml:"check,omitempty"`
	// Validation is Check rendered as the command that runs it, by the store that keeps
	// the row. Nothing in this module writes or reads it: [Declaration.Apply] leaves it as
	// it was, so a store that keeps it renders it from Check on every write.
	Validation string `json:"validation,omitempty" yaml:"validation,omitempty"`
	// Goals are the job's definition of done that magus grades: a check that passed, paths
	// or symbols this job changed, added or removed. Check is graded as one more goal. Named
	// goals rather than gates so the word never collides with the CI gate.
	Goals []Goal `json:"goals,omitempty" yaml:"goals,omitempty"`
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
	// newest last, at most MaxUnattributedWrites and one entry per path. The
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
	// on, in the same `magus vcs checkpoint -o name` form Checkpoint holds. Checkpoint is
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
	// CheckoutRoot is the absolute root of the checkout `magus job exec` took this job in,
	// stamped by the store on that write. Once the directory is gone, nobody can be
	// holding the job, and the store ends it.
	CheckoutRoot string `json:"checkout_root,omitempty" yaml:"checkout_root,omitempty"`
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
	// as no_return on the next read; `magus job prune` ends an untaken one.
	Deadline int64 `json:"deadline" yaml:"deadline,omitempty"`
	// Attempt is the run record behind the live try's result, resolved
	// in the holder's OWN checkout, so a waiter in another checkout
	// reopens it. The result is on the [Attempt].
	Attempt *CapturedRun `json:"attempt,omitzero" yaml:"attempt,omitempty"`
	// GateAttempts are the portable output-store snapshots for every explicit goal.
	// Attempt remains the snapshot for the primary Check.
	GateAttempts []GateAttempt `json:"gate_attempts,omitempty" yaml:"gate_attempts,omitempty"`
	// LastRun is the job's most recent run, nil until one is submitted. The
	// invocation id lands at submit, the cost and outcome when the run ends.
	LastRun *Run `json:"last_run,omitzero" yaml:"last_run,omitempty"`
	// Integration is the latest grade of this job's check goals in another tree, nil until
	// `magus job wait --integration` records one. Store-computed like Result, and kept
	// beside State rather than moving it: a job that passed where it was written and fails
	// once merged is two facts, and one field could hold only the later.
	Integration *Integration `json:"integration,omitzero" yaml:"integration,omitempty"`
	// WaiterEvidence names the goals whose passing run came from the waiter's checkout
	// rather than the holder's, nil unless `magus job wait --evidence` took some. It sits
	// beside GateAttempts, which keep the holder's own attempts untouched: a goal that
	// failed where the work was written and passed once re-run here is two facts. Each
	// Gates entry is one goal, the ref taken for it and its verdict; Checkout and At say
	// where and when.
	//
	// A Integration because the record is the same one --integration keeps: goals graded
	// against runs in another checkout. What differs is only that these verdicts moved the
	// job's state, so the record exists only for a job that passed.
	WaiterEvidence *Integration `json:"waiter_evidence,omitzero" yaml:"waiter_evidence,omitempty"`
}

// Worktree is the checkout `magus job fork --checkout` made or adopted for one job: the
// job's holder takes it there and nowhere else. One the fork made (Made) magus removes once
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
	// Removed is unix seconds when magus removed it, zero while it stands.
	Removed int64 `json:"removed" yaml:"removed,omitempty"`
	// Kept is why magus left it standing after the job ended, "" until then or once it is
	// removed.
	Kept string `json:"kept,omitempty" yaml:"kept,omitempty"`
	// Made is true when the job's fork created the checkout, the one kind magus
	// removes once the job ends. A checkout the fork adopted, or one recorded
	// before Made existed, reads false and stands until its maker removes it.
	Made bool `json:"made" yaml:"made,omitempty"`
}

// Standing reports whether the checkout has not been removed: it binds its job's holder,
// and the guard treats it as its job's own.
func (w *Worktree) Standing() bool { return w != nil && w.Removed == 0 }

// Integration is a job's check goals graded against runs recorded in Checkout, the tree
// an integration branch was merged into, at At (unix seconds).
type Integration struct {
	Checkout string               `json:"checkout" yaml:"checkout"`
	At       int64                `json:"at" yaml:"at"`
	Verified bool                 `json:"verified" yaml:"verified"`
	Gates    []VerificationStatus `json:"gates,omitempty" yaml:"gates,omitempty"`
	// ObservedGeneration is the job's Generation when the grade was taken.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
}

// Rejection is a `magus job wait` that refused the live attempt, At unix seconds.
type Rejection struct {
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

// Declaration is the typed INPUT for one lease: the fields a caller DECLARES,
// and nothing the store computes.
//
// It is a DECLARATION and not a merge: an omitted field is cleared rather than
// kept. magus\workspace().putJob does the opposite (see job.PutMerge).
//
// JSON only, so no yaml tags. The members it does not declare are kept in
// Schema.Unknown by its own MarshalJSON and UnmarshalJSON, which plain encoding/json
// calls; libs/codec's UnmarshalStrict refuses them instead.
type Declaration struct {
	// Schema is the job shape this record is written in. The version is
	// required, and a reader refuses one it does not know.
	Schema
	// ID is the lease's identity within the plan, the key a second register replaces on,
	// and the only required field besides the version.
	ID string `json:"id" schema:"leaseid"`
	// Parent is the lease this one was spawned under, empty for a lease the root declared.
	Parent string `json:"parent,omitempty" schema:"leaseid"`
	// Agent is the agent section; nil is ordinary work. See [Job.Agent].
	Agent *Agent `json:"agent,omitzero"`
	// Checkpoint is the working state this lease starts from, as `magus vcs checkpoint -o
	// name` prints it.
	Checkpoint string `json:"checkpoint,omitempty"`
	// Paths are what this lease may write and read. See [Paths].
	Paths Paths `json:"paths,omitzero"`
	// DependsOn names the lease ids that must land before this one.
	DependsOn []string `json:"depends_on,omitempty" schema:"leaseid"`
	// Integrates names the branches this job merges; see [Job.Integrates].
	Integrates []string `json:"integrates,omitempty"`
	// Breaks are the flags, JSON keys, exported symbols or stored formats the root approved
	// this job breaking; empty means it breaks nothing on its own authority. See [Job.Breaks].
	Breaks []string `json:"breaks,omitempty"`
	// Weight is the job's lean on the machine budget, in (0,1]: omitted means 0.5, and an
	// explicit 0 is out of range. See [Job.Weight].
	Weight *float64 `json:"weight,omitzero"`
	// BriefFormat is the format a brief to this job's worker takes; empty defers to
	// magus.yaml's agents.brief_format. See [Job.BriefFormat].
	BriefFormat BriefFormat `json:"brief_format,omitempty"`
	// Delivery and ParentHead: see [Job.Delivery].
	Delivery   string `json:"delivery,omitempty"`
	ParentHead string `json:"parent_head,omitempty"`
	// Follows names the job this one continues. See [Job.Follows].
	Follows string `json:"follows,omitempty" schema:"leaseid"`
	// Checkout is an absolute directory, outside the workspace, where the fork makes a new
	// checkout of the repository for this job at its checkpoint, or at the forking
	// checkout's revision when it names none.
	// The job owns it: see [Worktree]. Empty works wherever the job is taken.
	Checkout string `json:"checkout,omitempty" schema:"abspath"`
	// Check is the one check this lease runs, and acceptance binds a worker's evidence
	// to it.
	Check *LeaseCheck `json:"check,omitzero"`
	// State is the job's lifecycle position, empty for a job that has not said
	// where it stands. no_return is a lease that never reported, which is not
	// a failure.
	State State `json:"state,omitempty"`
	// ReadOnly marks a lease that gathers evidence and writes nothing, so empty write
	// paths are correct rather than missing.
	ReadOnly bool `json:"read_only,omitzero"`
	// Goals are what done means, graded by `magus job wait`. See [Job.Goals].
	Goals []Goal `json:"goals,omitempty"`
	// Timeout bounds the lease, as a Go duration. The store stamps Job.Deadline from it when
	// it writes the job, so a declaration carries a length and never an
	// instant. Empty is no bound; there is no default here (a workspace may
	// set jobs.default_timeout).
	Timeout string `json:"timeout,omitempty" schema:"duration"`
	// MaxAttempts caps how many times the job may be forked, its first fork included; zero
	// is no cap. See [Job.MaxAttempts].
	MaxAttempts int `json:"max_attempts,omitzero"`
	// MaxClaims caps how many proved claims one attempt takes; zero takes the default. See
	// [Job.MaxClaims].
	MaxClaims int `json:"max_claims,omitzero"`
	// Deviations accept permissible problems on the record's own authority.
	Deviations []Deviation `json:"deviations,omitempty"`
	// Enter turns the record into an entry rather than a declaration: it acknowledges one
	// write into this path, inside the write paths of the live job ID names, by somebody
	// other than its holder. A record carrying it declares nothing else.
	Enter string `json:"enter,omitempty"`
	// Claim turns the record into a proved claim of the job ID names: each entry adds
	// one write path with the proof the store re-proves before it writes. A record carrying
	// it declares nothing else.
	Claim []ClaimRequest `json:"claim,omitempty"`
	// Deferred and LeftUnmerged turn the record into a settlement of the
	// job ID names, ended or not. It declares nothing else.
	Deferred     []Deferred `json:"deferred,omitempty"`
	LeftUnmerged string     `json:"left_unmerged,omitempty"`
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

// ClaimRequest is one write path a claim record asks for, and Because the proof:
// `refs:<symbol>`, `test:<file>`, `output:<project>:<target>` or `released`. See
// [ParseClaimEvidence].
//
// Callers, given instead of Path and Because, names a symbol: `magus job apply` expands
// the request into one per declaration holding an occurrence of it, path
// `<file>#<declaration>` because `refs:<symbol>`, and the store proves each of those as
// it would one written by hand.
type ClaimRequest struct {
	Path    string `json:"path,omitempty" schema:"writepath"`
	Because string `json:"because,omitempty" schema:"nonblank"`
	Callers string `json:"callers,omitempty" schema:"nonblank"`
}

// ClaimKind is what a proved claim rests on.
type ClaimKind string

const (
	// ClaimRefs is a path that references a declaration the job's diff changed inside
	// its write paths.
	ClaimRefs ClaimKind = "refs"
	// ClaimTest is a test beside a file the job changed that references what it defines.
	ClaimTest ClaimKind = "test"
	// ClaimOutput is a declared output of a target whose sources the job changed.
	ClaimOutput ClaimKind = "output"
	// ClaimReleased is a path the job's own holder released in this attempt.
	ClaimReleased ClaimKind = "released"
	// ClaimDeadHolder is a write path of the live job it names whose holder is dead: its
	// session ended, or it made no judged call past the silence bound with no background
	// command running.
	ClaimDeadHolder ClaimKind = "dead"
)

// ClaimEvidence is a parsed `because`: the kind and what it names, empty for released.
type ClaimEvidence struct {
	Kind    ClaimKind `json:"kind"              yaml:"kind"`
	Subject string    `json:"subject,omitempty" yaml:"subject,omitempty"`
}

// String is the evidence as a `because` spells it.
func (e ClaimEvidence) String() string {
	if e.Subject == "" {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s:%s", string(e.Kind), e.Subject)
}

// ParseClaimEvidence reads a `because`. An output subject is `<project>:<target>`.
func ParseClaimEvidence(because string) (ClaimEvidence, error) {
	kind, subject, _ := strings.Cut(strings.TrimSpace(because), ":")
	e := ClaimEvidence{Kind: ClaimKind(kind), Subject: strings.TrimSpace(subject)}
	refuse := func(reason string) (ClaimEvidence, error) {
		return e, &ClaimEvidenceError{Because: because, Reason: reason}
	}
	switch {
	case !e.Kind.Valid():
		return refuse("names no proof, so use one of refs:<symbol>, test:<file>, output:<project>:<target>, dead:<job> or released")
	case e.Kind == ClaimReleased && e.Subject != "":
		return refuse("names a subject, and released names nothing after it")
	case e.Kind != ClaimReleased && e.Subject == "":
		return refuse(fmt.Sprintf("names no %s subject", e.Kind))
	case e.Kind == ClaimOutput && !strings.Contains(e.Subject, ":"):
		return refuse("names no target, so spell it output:<project>:<target>")
	}
	return e, nil
}

// ClaimEvidenceError is a `because` [ParseClaimEvidence] refuses, and why.
type ClaimEvidenceError struct {
	Because string
	Reason  string
}

func (e *ClaimEvidenceError) Error() string { return strconv.Quote(e.Because) + " " + e.Reason }

// Claim is one write path a holder added with a proof. Proof is the sentence the
// store found it true by, At unix seconds and By who wrote it.
type Claim struct {
	Path     string        `json:"path"     yaml:"path"`
	Evidence ClaimEvidence `json:"evidence" yaml:"evidence"`
	Proof    string        `json:"proof"    yaml:"proof"`
	At       int64         `json:"at"       yaml:"at"`
	By       Origin        `json:"by"       yaml:"by"`
}

// pathOutsideWorkspace says why a declared path can name no workspace-relative path, or
// "": the guard matches declarations against paths relative to the workspace root, never
// expanding a ~ or reading an absolute path in one.
func pathOutsideWorkspace(p string) string {
	file, _ := SplitClaim(p)
	switch clean := path.Clean(file); {
	case strings.HasPrefix(file, "~"):
		return "begins with ~"
	case path.IsAbs(clean):
		return "is absolute"
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "climbs out of the workspace with .."
	}
	return ""
}

// Faults is every fault the record shows on its own, without a workspace: each field's
// shape, the check, the deviations and the goals. It never stops at the first. A
// deviation may permit only a code in permissible, the caller's own.
func (d Declaration) Faults(permissible ...Code) []Fault {
	var ps []Fault
	invalid := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultRecord, Field: field, Message: message, Fix: fix})
	}
	if rule := idRule(strings.TrimSpace(d.ID)); rule != "" {
		invalid("id", fmt.Sprintf("%q is not a lease id: %s", d.ID, rule), "name a branch-shaped id such as fix-parser or area/fix-parser")
	}
	if d.Enter != "" {
		rest := d
		rest.Schema, rest.ID, rest.Enter = Schema{}, "", ""
		if !reflect.ValueOf(rest).IsZero() {
			invalid("enter", "a record carrying enter enters a job and declares nothing, so it carries only api_version, id and enter",
				"drop every other field, or drop enter")
		}
		return ps
	}
	if len(d.Claim) > 0 {
		return append(ps, d.claimFaults()...)
	}
	if d.Settles() {
		return append(ps, d.settlementFaults()...)
	}
	if p := strings.TrimSpace(d.Parent); p != "" && !ValidID(p) {
		invalid("parent", fmt.Sprintf("parent %q is not a lease id", d.Parent), "name the id of the job this one was forked under")
	}
	if d.Agent != nil {
		if err := d.Agent.Validate(); err != nil {
			invalid("agent", err.Error(), `write worker {"type": "resumed"}, or {"type": "new", "reason": "<why>"}`)
		}
	}
	for i, dep := range d.DependsOn {
		if d := strings.TrimSpace(dep); d != "" && !ValidID(d) {
			invalid(fmt.Sprintf("depends_on[%d]", i), fmt.Sprintf("depends_on[%d] %q is not a lease id", i, dep),
				"name the id of a job that must land first")
		}
	}
	for i, branch := range d.Integrates {
		at := fmt.Sprintf("integrates[%d]", i)
		switch b := strings.TrimSpace(branch); {
		case !validIntegratedBranch(b):
			invalid(at, fmt.Sprintf("%s %q is not a branch name a merge can take", at, branch),
				"name a local branch, such as the one a finished job committed on")
		case slices.Contains(d.Integrates[:i], branch):
			invalid(at, fmt.Sprintf("%s %q is named twice", at, branch), "drop the repeat")
		}
	}
	if len(d.Integrates) > 0 && d.ReadOnly {
		invalid("read_only", "an integration job merges branches, so it writes", "drop read_only, or drop integrates")
	}
	if d.State != "" && !ValidState(d.State) {
		invalid("state", fmt.Sprintf("state %q is not a job state", d.State), "use one of "+StateVocabulary())
	}
	if _, err := ParseTimeout(d.Timeout); err != nil {
		invalid("timeout", fmt.Sprintf("timeout %q is not a positive duration", d.Timeout), "name one such as 30m or 2h, or leave it out")
	}
	if d.BriefFormat != "" && !d.BriefFormat.Valid() {
		invalid("brief_format", fmt.Sprintf("brief_format %q is not a brief format", d.BriefFormat),
			fmt.Sprintf("use one of %s, or leave it out for magus.yaml's agents.brief_format", strings.Join(d.BriefFormat.Values(), ", ")))
	}
	if d.Delivery != "" && !slices.Contains(Deliveries(), d.Delivery) {
		invalid("delivery", fmt.Sprintf("delivery %q is not a delivery", d.Delivery),
			"use one of "+strings.Join(Deliveries(), ", "))
	}
	if d.ParentHead != "" && d.Delivery != DeliveryStacked {
		invalid("parent_head", "parent_head is the base of a stacked job's pull request, and this job is not stacked",
			"drop parent_head, or set delivery to "+DeliveryStacked)
	}
	if d.Weight != nil && !ValidWeight(*d.Weight) {
		invalid("weight", fmt.Sprintf("weight %v is outside (0,1]", *d.Weight),
			fmt.Sprintf("use a weight above 0 and at most 1, or leave it out for %v", DefaultWeight))
	}
	if d.MaxAttempts < 0 {
		invalid("max_attempts", fmt.Sprintf("max_attempts is %d, and it is a count of forks", d.MaxAttempts), "use a positive count, or 0 for no cap")
	}
	if d.MaxClaims < 0 {
		invalid("max_claims", fmt.Sprintf("max_claims is %d, and it is a count of claims", d.MaxClaims), "use a positive count, or 0 for the default")
	}
	if d.Checkout != "" && !filepath.IsAbs(d.Checkout) {
		invalid("checkout", fmt.Sprintf("checkout %q is relative, and a fork names the checkout it makes by an absolute path", d.Checkout),
			"name an absolute directory outside the workspace")
	}
	for _, field := range []struct {
		name  string
		paths []string
	}{{"paths.write", d.Paths.Write}, {"paths.deny", d.Paths.Deny}, {"paths.read", d.Paths.Read}} {
		for i, p := range field.paths {
			if why := pathOutsideWorkspace(p); why != "" {
				at := fmt.Sprintf("%s[%d]", field.name, i)
				invalid(at, fmt.Sprintf("%s %q %s, so it matches no workspace path and bounds nothing", at, p, why),
					"name it relative to the workspace root")
			}
		}
	}
	for i, p := range d.Paths.Soft {
		if !slices.Contains(d.Paths.Write, p) {
			at := fmt.Sprintf("paths.soft[%d]", i)
			invalid(at, fmt.Sprintf("%s %q is not in paths.write, and a soft path is a write path that overlaps nothing", at, p),
				"name an entry of paths.write, or drop it")
		}
	}
	_, declared, checkFaults := d.check()
	ps = append(ps, checkFaults...)
	for i, b := range d.Breaks {
		if strings.TrimSpace(b) == "" {
			at := fmt.Sprintf("breaks[%d]", i)
			invalid(at, at+" is blank", "name the flag, key, symbol or format the job may break, or drop the entry")
		}
	}
	for i, w := range d.Deviations {
		at := fmt.Sprintf("deviations[%d]", i)
		if !slices.Contains(permissible, w.Code) {
			invalid(at+".code", fmt.Sprintf("%s permits %q, which is not a permissible code", at, w.Code),
				fmt.Sprintf("permit only %s, and fix the record for anything else", orList(permissible)))
		}
		if strings.TrimSpace(w.Subject) == "" {
			invalid(at+".subject", at+" names no subject", "name the write path, `target project` or output path the problem names")
		}
		if strings.TrimSpace(w.Reason) == "" {
			invalid(at+".reason", at+" gives no reason", "say why the problem is accepted, such as \"the root regenerates it\"")
		}
	}
	return append(ps, goalFaults(d.Goals, declared)...)
}

// claimFaults is every fault of a claim record: it carries only schema_version, id
// and claim, and each entry names one literal workspace path and a proof that parses.
func (d Declaration) claimFaults() []Fault {
	var ps []Fault
	invalid := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultRecord, Field: field, Message: message, Fix: fix})
	}
	rest := d
	rest.Schema, rest.ID, rest.Claim = Schema{}, "", nil
	if !reflect.ValueOf(rest).IsZero() {
		invalid("claim", "a record carrying claim claims a job and declares nothing, so it carries only api_version, id and claim",
			"drop every other field, or drop claim")
	}
	for i, w := range d.Claim {
		at := fmt.Sprintf("claim[%d]", i)
		if strings.TrimSpace(w.Callers) != "" {
			if w.Path != "" || w.Because != "" {
				invalid(at+".callers", fmt.Sprintf("%s names callers of %s and also a path or a proof, and callers expands into both", at, w.Callers),
					"drop path and because, or drop callers")
			}
			continue
		}
		switch p := strings.TrimSpace(w.Path); {
		case p == "":
			invalid(at+".path", at+" names no path", "name the file to add, relative to the workspace root")
		case pathOutsideWorkspace(p) != "":
			invalid(at+".path", fmt.Sprintf("%s %q %s", at, w.Path, pathOutsideWorkspace(p)), "name it relative to the workspace root")
		case isGlobMeta(p) || strings.Contains(p, "#"):
			invalid(at+".path", fmt.Sprintf("%s %q is a pattern or a claim, and a proof covers one file", at, w.Path), "name one file")
		}
		if _, err := ParseClaimEvidence(w.Because); err != nil {
			invalid(at+".because", fmt.Sprintf("%s: %s", at, err), "name the proof the path rests on")
		}
	}
	return ps
}

// Settles reports whether the record answers a job's deferred items or states
// why its work stays unmerged, rather than declaring a spec.
func (d Declaration) Settles() bool {
	return len(d.Deferred) > 0 || d.LeftUnmerged != ""
}

func (d Declaration) settlementFaults() []Fault {
	var ps []Fault
	invalid := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultRecord, Field: field, Message: message, Fix: fix})
	}
	rest := d
	rest.Schema, rest.ID, rest.Deferred, rest.LeftUnmerged = Schema{}, "", nil, ""
	if !reflect.ValueOf(rest).IsZero() {
		invalid("deferred", "a record carrying deferred or left_unmerged settles a job and declares nothing, so it carries only api_version, id, deferred and left_unmerged",
			"drop every other field, or drop deferred and left_unmerged")
	}
	if d.LeftUnmerged != "" && strings.TrimSpace(d.LeftUnmerged) == "" {
		invalid("left_unmerged", "left_unmerged is blank", "state why the work stays off the merge branch")
	}
	for i, d := range d.Deferred {
		at := fmt.Sprintf("deferred[%d]", i)
		row, reason := strings.TrimSpace(d.Job), strings.TrimSpace(d.Reason)
		switch {
		case strings.TrimSpace(d.Item) == "":
			invalid(at+".item", at+" names no item", "copy the item's text from the job's result")
		case (row == "") == (reason == ""):
			invalid(at, at+" answers with neither or both of job and reason", "name the declared job that carries it, or the reason it is dismissed")
		case row != "" && !ValidID(row):
			invalid(at+".job", fmt.Sprintf("%s job %q is not a lease id", at, d.Job), "name the declared job that carries it")
		}
	}
	return ps
}

// goalFaults is every fault of a record's goals: each goal's own shape, then what
// only the set shows. primary says the record declares a check, the goal named
// PrimaryGoalID a depends_on may name.
func goalFaults(goals []Goal, primary bool) []Fault {
	var ps []Fault
	bad := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultGoal, Field: field, Message: message, Fix: fix})
	}
	index := make(map[string]int, len(goals))
	if primary {
		index[PrimaryGoalID] = -1
	}
	for i, gate := range goals {
		at := fmt.Sprintf("goals[%d]", i)
		ps = append(ps, gate.faults(at)...)
		if _, dup := index[gate.ID]; dup {
			bad(at+".id", fmt.Sprintf("goals carries duplicate id %q", gate.ID), "give each goal its own id")
			continue
		}
		index[gate.ID] = i
	}
	ps = append(ps, oneSignaturePerSymbol(goals)...)
	for i, gate := range goals {
		for j, dep := range gate.DependsOn {
			at := fmt.Sprintf("goals[%d].depends_on[%d]", i, j)
			switch _, known := index[dep]; {
			case dep == gate.ID:
				bad(at, fmt.Sprintf("goal %q cannot depend on itself", gate.ID), "drop the entry")
			case !known:
				bad(at, fmt.Sprintf("goal %q depends_on unknown goal %q", gate.ID, dep), "name a goal this record declares")
			}
		}
	}
	visiting := make(map[string]bool, len(goals))
	visited := make(map[string]bool, len(goals))
	var visit func(string)
	visit = func(id string) {
		i, known := index[id]
		switch {
		case !known || i < 0 || visited[id]:
			return
		case visiting[id]:
			visited[id] = true
			bad(fmt.Sprintf("goals[%d].depends_on", i), fmt.Sprintf("goal dependencies contain a cycle at %q", id),
				"drop one depends_on entry on the cycle")
			return
		}
		visiting[id] = true
		for _, dep := range goals[i].DependsOn {
			if dep != id {
				visit(dep)
			}
		}
		visiting[id] = false
		visited[id] = true
	}
	for _, gate := range goals {
		visit(gate.ID)
	}
	return ps
}

// oneSignaturePerSymbol refuses two signature goals on one symbol: the graph answers one
// declaration per name, so two shapes for it make a goal nobody could meet.
func oneSignaturePerSymbol(goals []Goal) []Fault {
	var ps []Fault
	held := map[string]string{}
	for i, gate := range goals {
		if gate.Resolve().Expect != ExpectSignature {
			continue
		}
		for j, name := range gate.Symbols {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if other, ok := held[name]; ok {
				ps = append(ps, Fault{
					Kind:    FaultGoal,
					Field:   fmt.Sprintf("goals[%d].symbols[%d]", i, j),
					Message: fmt.Sprintf("goals %q and %q both expect a signature of %q, and a symbol has one", other, gate.ID, name),
					Fix:     "keep one signature goal per symbol",
				})
				continue
			}
			held[name] = gate.ID
		}
	}
	return ps
}

// Faults is every fault g shows on its own: what keeps it from being matched to an
// output record without interpreting a shell command.
func (g Goal) Faults() []Fault { return g.faults("") }

// faults is every fault g shows on its own, each Field under field (`goals[1]`).
func (g Goal) faults(field string) []Fault {
	var ps []Fault
	bad := func(sub, message, fix string) {
		at := sub
		if field != "" {
			at = fmt.Sprintf("%s.%s", field, sub)
		}
		ps = append(ps, Fault{Kind: FaultGoal, Field: at, Message: message, Fix: fix})
	}
	if rule := idRule(strings.TrimSpace(g.ID)); rule != "" {
		bad("id", fmt.Sprintf("id %q is not a goal id: %s", g.ID, rule), "use a lowercase, branch-shaped id")
	}
	// Validates what the job WILL BE, not what arrived. A declaration off the
	// wire has not been through Resolve yet.
	g = g.Resolve()
	switch accepted, known := goalAccepts[g.Kind]; {
	case !known:
		bad("kind", fmt.Sprintf("goal %q names kind %q", g.ID, g.Kind), "use one of "+quoteJoin(GoalKinds(), ", "))
	case !slices.Contains(accepted, g.Expect):
		bad("expect", fmt.Sprintf("goal %q is a %s goal expecting %q", g.ID, g.Kind, g.Expect),
			fmt.Sprintf("a %s goal expects %s", g.Kind, quoteJoin(accepted, " or ")))
	}
	// Exactly one subject, named by the kind. A goal carrying two is one whose author
	// changed their mind, and grading the one the kind happens to read would silently
	// ignore the other.
	if g.Kind != GoalKindCheck && (g.Check.Target != "" || g.Check.Script != "") {
		bad("check", fmt.Sprintf("goal %q is a %s goal and also carries a check", g.ID, g.Kind), "a goal examines one subject: drop the check")
	}
	if g.Kind != GoalKindPaths && len(trimmedNonEmpty(g.Paths)) > 0 {
		bad("paths", fmt.Sprintf("goal %q is a %s goal and also carries paths", g.ID, g.Kind), "a goal examines one subject: drop the paths")
	}
	if g.Kind != GoalKindSymbol && len(trimmedNonEmpty(g.Symbols)) > 0 {
		bad("symbols", fmt.Sprintf("goal %q is a %s goal and also carries symbols", g.ID, g.Kind), "a goal examines one subject: drop the symbols")
	}
	// An unknown kind has no subject field, and its own problem above says so.
	subject := map[GoalKind]string{GoalKindCheck: "check", GoalKindPaths: "paths", GoalKindSymbol: "symbols"}[g.Kind]
	if subject != "" && len(trimmedNonEmpty(g.Subject())) == 0 {
		bad(subject, fmt.Sprintf("goal %q is a %s goal and names nothing to examine, so nothing could ever satisfy it", g.ID, g.Kind),
			"name what the goal examines")
	}
	switch {
	case g.Expect != ExpectSignature && strings.TrimSpace(g.Signature) != "":
		bad("signature", fmt.Sprintf("goal %q expects %q and carries a signature, which only a %q goal reads", g.ID, g.Expect, ExpectSignature),
			"drop the signature")
	case g.Expect == ExpectSignature && len(trimmedNonEmpty(g.Symbols)) != 1:
		bad("symbols", fmt.Sprintf("goal %q expects the signature of %d symbols, and a signature is one declaration", g.ID, len(trimmedNonEmpty(g.Symbols))),
			"name one symbol per goal")
	}
	if g.Kind != GoalKindCheck {
		if len(g.Accept) > 0 {
			bad("accept", fmt.Sprintf("goal %q is a %s goal and carries accept, which only a check goal reads", g.ID, g.Kind), "drop accept")
		}
		return ps
	}
	acceptFaults(g, bad)
	if err := g.Check.validScript(); err != nil {
		bad("check.script", err.Error(), "name a target or a script, and give a script no project or charms")
	} else if g.Check.Script == "" {
		if _, err := ParseLeaseCheck(fmt.Sprintf("%s %s", g.Check.Target, g.Check.Project)); err != nil {
			bad("check.target", err.Error(), "name one target and the project it runs in")
		}
	}
	return ps
}

// acceptFaults reports every fault of a check goal's accept list through bad.
func acceptFaults(g Goal, bad func(sub, message, fix string)) {
	fix := "accept one or more of " + quoteJoin(Bases(), ", ")
	if len(g.Accept) == 0 {
		bad("accept", fmt.Sprintf("goal %q is a check goal accepting no basis, so no run could grade it", g.ID), fix)
		return
	}
	seen := map[Basis]bool{}
	for i, b := range g.Accept {
		switch {
		case b == "" || !b.Valid():
			bad(fmt.Sprintf("accept[%d]", i), fmt.Sprintf("goal %q accepts %q, which is not a basis", g.ID, b), fix)
		case seen[b]:
			bad(fmt.Sprintf("accept[%d]", i), fmt.Sprintf("goal %q accepts %q twice", g.ID, b), "name each basis once")
		}
		seen[b] = true
	}
}

// validScript refuses a check naming both a script and a target, a script with a
// project, since a recorded script run belongs to the workspace, and test on a target.
func (c LeaseCheck) validScript() error {
	switch {
	case c.Script == "" && c.Test:
		return fmt.Errorf("check target %q sets test, which runs a script's tests and so belongs to a script check", c.Target)
	case c.Script == "":
		return nil
	case c.Target != "":
		return fmt.Errorf("check names target %q and script %q, a check names one", c.Target, c.Script)
	case c.Project != "" && c.Project != ".":
		return fmt.Errorf("check script %q names project %q, a recorded script run belongs to the workspace", c.Script, c.Project)
	case c.NoDefaultCharms:
		return fmt.Errorf("check script %q sets no_default_charms, a script runs under no charms", c.Script)
	default:
		return nil
	}
}

// ParseTimeout reads a declared timeout: empty is no bound and parses to zero, anything
// else must be a positive Go duration.
func ParseTimeout(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, &TimeoutError{Timeout: s}
	}
	return d, nil
}

// TimeoutError is a declared timeout [ParseTimeout] refuses.
type TimeoutError struct{ Timeout string }

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timeout %q is not a positive duration such as 30m or 2h", e.Timeout)
}

// Overdue reports whether a live job has passed its deadline at now, in unix
// seconds. A terminal job has no writes left to bound, so it is never overdue.
func (j Job) Overdue(now int64) bool {
	return j.Deadline > 0 && j.State.Live() && now >= j.Deadline
}

// EndReasonSessionEnded opens the end_reason of a job the store ended because
// the agent session holding it ended. It is the one ending `magus job exec`
// takes back. A host may resume a session it reported over, and the resumed
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

// check is the job's declared check and whether it declares one. A check that does not
// parse is refused HERE, at the door.
func (d Declaration) check() (LeaseCheck, bool, []Fault) {
	invalid := func(field string, err error, fix string) []Fault {
		return []Fault{{Kind: FaultRecord, Field: field, Message: err.Error(), Fix: fix}}
	}
	switch {
	case d.Check != nil && d.Check.Script != "":
		if err := d.Check.validScript(); err != nil {
			return LeaseCheck{}, false, invalid("check.script", err, "name a target or a script, and give a script no project or charms")
		}
		return LeaseCheck{Script: d.Check.Script, Args: d.Check.Args, Test: d.Check.Test}, true, nil
	case d.Check != nil:
		if err := d.Check.validScript(); err != nil {
			return LeaseCheck{}, false, invalid("check.test", err, "drop test, or name a script")
		}
		parsed, err := ParseLeaseCheck(fmt.Sprintf("%s %s", d.Check.Target, d.Check.Project))
		if err != nil {
			return LeaseCheck{}, false, invalid("check.target", err, "name one target and the project it runs in")
		}
		parsed.Args = d.Check.Args
		parsed.NoDefaultCharms = d.Check.NoDefaultCharms
		return parsed, true, nil
	}
	return LeaseCheck{}, false, nil
}

// The deliveries a person picks for a job's work.
const (
	// DeliveryFold folds the work into the parent's pull request.
	DeliveryFold = "fold"
	// DeliverySeparate opens its own pull request against the base branch.
	DeliverySeparate = "separate"
	// DeliveryStacked opens a pull request based on the parent's head.
	DeliveryStacked = "stacked"
)

// Deliveries is the closed set [Job.Delivery] takes.
func Deliveries() []string {
	return []string{DeliveryFold, DeliverySeparate, DeliveryStacked}
}

// StateVocabulary is the closed set of job states, as an error quotes it. Shared by
// Declaration.Validate and job.PutMerge, the two places a caller-supplied state is
// checked against the set.
func StateVocabulary() string {
	states := States()
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// Apply writes this declaration onto a job. Store-computed fields are untouched, and so
// is Validation: a job that already carries releases or a registration keeps them.
func (d Declaration) Apply(j *Job) {
	j.Parent = strings.TrimSpace(d.Parent)
	j.Checkpoint = strings.TrimSpace(d.Checkpoint)
	j.Paths.Write = trimmedNonEmpty(d.Paths.Write)
	j.Paths.Deny = trimmedNonEmpty(d.Paths.Deny)
	j.Paths.Soft = trimmedNonEmpty(d.Paths.Soft)
	j.Paths.Read = trimmedNonEmpty(d.Paths.Read)
	j.DependsOn = trimmedNonEmpty(d.DependsOn)
	j.Integrates = trimmedNonEmpty(d.Integrates)
	j.Breaks = trimmedNonEmpty(d.Breaks)
	j.Agent = d.Agent.Trimmed()
	j.Weight = 0
	if d.Weight != nil {
		j.Weight = *d.Weight
	}
	j.BriefFormat = d.BriefFormat
	j.Delivery = d.Delivery
	j.ParentHead = strings.TrimSpace(d.ParentHead)
	j.Follows = strings.TrimSpace(d.Follows)
	// Faults refused an unparsable check before the job reached a store.
	check, declared, _ := d.check()
	j.Check = nil
	if declared {
		j.Check = &check
	}
	j.Goals = cloneGoals(d.Goals)
	j.State = d.State
	j.ReadOnly = d.ReadOnly
	j.MaxAttempts = d.MaxAttempts
	j.MaxClaims = d.MaxClaims
	j.Deviations = slices.Clone(d.Deviations)
}

// ApplySpec writes this declaration's spec onto a job, re-applying a declaration to a
// row that exists. It is Apply, but the job's state is kept, and so is its checkpoint
// when the declaration names none.
func (d Declaration) ApplySpec(j *Job) {
	state, checkpoint := j.State, j.Checkpoint
	d.Apply(j)
	j.State = state
	if j.Checkpoint == "" {
		j.Checkpoint = checkpoint
	}
}

func cloneGoals(in []Goal) []Goal {
	if in == nil {
		return nil
	}
	out := make([]Goal, len(in))
	for i, goal := range in {
		// RESOLVED on the way through, which is what makes this the write boundary the
		// enums' no-empty-member rule depends on: every path that stores goals (Apply,
		// job.PutMerge) clones them through here.
		out[i] = goal.Resolve()
		out[i].Check.Args = slices.Clone(goal.Check.Args)
		out[i].Paths = slices.Clone(goal.Paths)
		out[i].Symbols = slices.Clone(goal.Symbols)
		out[i].DependsOn = slices.Clone(goal.DependsOn)
		out[i].Accept = slices.Clone(goal.Accept)
	}
	return out
}

// trimmedNonEmpty trims every element of in and drops the ones left empty.
func trimmedNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
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
	// TakenBy is the job magus granted the path to because this job's holder
	// was dead, and DeadBecause why magus judged it dead. Both are empty on any
	// other release.
	TakenBy     string `json:"taken_by,omitempty"     yaml:"taken_by,omitempty"`
	DeadBecause string `json:"dead_because,omitempty" yaml:"dead_because,omitempty"`
	// Narrowed marks a file the store narrowed to the holder's claims.
	// Its entry stays in paths.write; a re-fork ends the narrowing.
	Narrowed bool `json:"narrowed" yaml:"narrowed,omitempty"`
}

// PausedAt reports whether a person's pause holds the job at now, unix seconds.
// Past PausedUntil it has lapsed: the holder runs again, and the sweep and
// `magus job prune` may end the job.
func (j Job) PausedAt(now int64) bool {
	return j.Paused != 0 && (j.PausedUntil == 0 || now < j.PausedUntil)
}

// UnattributedWrite is one path a lease owns that somebody outside it wrote, and
// the content that writer left behind.
//
// The inverse of Release: a release is a worker saying "I am done with this, here is
// what I left", and this is magus saying "somebody who is not you changed this, here is
// what is there now", so a lease that read the file earlier can find out by ASKING.
//
// UNATTRIBUTED is the honest word: magus knows only that the writer named no live lease,
// so a person editing in their own checkout and an agent that forgot to export its id are
// indistinguishable here. Naming a human would be a claim magus cannot support.
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

// Overlap is two leases whose declared WritePaths intersect. A FACT the
// reader is handed, never a verdict: two leases may share a path because their author
// meant them to run in sequence, or because nobody noticed. Nothing here blocks,
// gates, or reorders anything.
type Overlap struct {
	// JobA and JobB are the lease ids, in ledger order: JobA was recorded first.
	JobA string `json:"job_a" yaml:"job_a"`
	JobB string `json:"job_b" yaml:"job_b"`
	// PathsA and PathsB are the intersecting declarations from each side, deduped and
	// kept apart. They are rarely the same string ("internal/ledger" and
	// "internal/ledger/store.go" intersect), so one merged list left a reader unable to
	// tell which lease claimed which, which is the only thing they can act on.
	PathsA []string `json:"paths_a" yaml:"paths_a"`
	PathsB []string `json:"paths_b" yaml:"paths_b"`
	// Claims compares what the two sides DECLARED below the file, when either claims a
	// declaration (`run.go#executeStages`): ClaimsDisjoint for different declarations of the
	// files they share, ClaimsShared when a claim covers one the other side holds. Empty
	// when neither side claims below the file.
	Claims string `json:"claims,omitempty" yaml:"claims,omitempty"`
	// Footprint compares what the two jobs have actually changed, declaration by
	// declaration. Nil when no reader computed it: it needs each job's checkout and a VCS,
	// and deriving an overlap needs neither.
	Footprint *OverlapFootprint `json:"footprint,omitzero" yaml:"footprint,omitempty"`
}

// The verdicts a Overlap's Claims reaches.
const (
	ClaimsDisjoint = "disjoint"
	ClaimsShared   = "shared"
)

// The verdicts a OverlapFootprint reaches.
const (
	FootprintDisjoint = "disjoint"
	FootprintShared   = "shared"
	FootprintUnknown  = "unknown"
)

// OverlapFootprint is whether two overlapping jobs' diffs touch the same declaration.
// Like the overlap it hangs off, a report: two jobs sharing a function may have been
// planned that way.
type OverlapFootprint struct {
	// Verdict is FootprintDisjoint, FootprintShared or FootprintUnknown.
	Verdict string `json:"verdict" yaml:"verdict"`
	// Shared is the Collisions of the two footprints, each Location as its String, on either
	// side of either diff, sorted. A changed line above a file's first declaration, or in a
	// file with no diff driver, locates as the whole file and so collides with every region
	// of that file.
	Shared []string `json:"shared,omitempty" yaml:"shared,omitempty"`
	// Reason says why the verdict is FootprintUnknown.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// List is what a reader of the ledger is served: the recorded jobs, plus
// the overlaps derived from them. It has a constructor rather than a literal
// at each read door. The MCP tool and the console's route must not disagree
// about whether an overlap exists, as with types.NewFileReport.
type List struct {
	Jobs []Job `json:"jobs" yaml:"jobs"`
	// Attempts are the tries of Jobs the store holds, each read under its
	// job's deadline (see [Attempt.Bound]).
	Attempts []Attempt `json:"attempts,omitempty" yaml:"attempts,omitempty"`
	Overlaps []Overlap `json:"overlaps,omitempty" yaml:"overlaps,omitempty"`
	// Overdue, Orphans and Stale are live job ids a reader should look at,
	// derived at one instant by [List.Flag]. The store's read has ended
	// most orphans and untaken stale jobs (see job.Store.List).
	Overdue []string `json:"overdue,omitempty" yaml:"overdue,omitempty"`
	Orphans []string `json:"orphans,omitempty" yaml:"orphans,omitempty"`
	Stale   []string `json:"stale,omitempty"   yaml:"stale,omitempty"`
	// Blocked are the live jobs that claim no paths yet because a dependency
	// has not passed, each naming the first such dependency. Derived with
	// Overlaps.
	Blocked []Block `json:"blocked,omitempty" yaml:"blocked,omitempty"`
	// ReadOnly are the jobs requiring a feature this magus lacks. It lists and
	// honors them and refuses every write to them.
	ReadOnly []ReadOnly `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	// Unmerged is filled by `magus ls jobs` alone. It reads the version
	// control per job, and every other door lists on a hot path.
	Unmerged *UnmergedReport `json:"unmerged,omitzero" yaml:"unmerged,omitempty"`
}

// ReadOnly names a job this magus will not write and the features it lacks.
type ReadOnly struct {
	Job   string   `json:"job"   yaml:"job"`
	Lacks []string `json:"lacks" yaml:"lacks"`
}

// Block is why a live job owns none of its write paths: On, a job it depends on, is in
// State rather than pass. State is empty when no job declares On.
type Block struct {
	Job   string `json:"job"             yaml:"job"`
	On    string `json:"on"              yaml:"on"`
	State State  `json:"state,omitempty" yaml:"state,omitempty"`
}

// BlockedOn reports the first job row depends on that has not reached pass.
// A blocked job claims none of its write paths, so the overlap report and the
// guard ask this before treating a job as an owner.
//
// A dependency no job declares blocks too.
func BlockedOn(rows []Job, row Job) (Block, bool) {
	for _, dep := range row.DependsOn {
		i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == dep })
		if i < 0 {
			return Block{Job: row.ID, On: dep}, true
		}
		if rows[i].State != StatePass {
			return Block{Job: row.ID, On: dep, State: rows[i].State}, true
		}
	}
	return Block{}, false
}

// String renders the reason as "blocked on <dep> which is <state>".
func (b Block) String() string {
	state := string(b.State)
	if state == "" {
		state = "undeclared"
	}
	return fmt.Sprintf("blocked on %s which is %s", b.On, state)
}

// Flag fills Overdue, Orphans and Stale as of now, in unix seconds. Overdue is past its
// deadline; an orphan is live while an ancestor has ended (pass, fail or no_return), so
// nobody is left to wait on it; stale was not updated within staleAfter, read by
// [StaleAt], and a zero staleAfter flags nothing.
func (l List) Flag(now int64, staleAfter time.Duration) List {
	l.Overdue, l.Orphans, l.Stale = nil, nil, nil
	for _, row := range l.Jobs {
		if !row.State.Live() {
			continue
		}
		if row.Overdue(now) {
			l.Overdue = append(l.Overdue, row.ID)
		}
		if _, ended := EndedAncestor(l.Jobs, row.ID); ended {
			l.Orphans = append(l.Orphans, row.ID)
		}
		if StaleAt(l.Jobs, row, now, staleAfter) {
			l.Stale = append(l.Stale, row.ID)
		}
	}
	return l
}

// EndedAncestor returns the nearest ancestor of id in a terminal state. A job
// with no state has not said it ended, so it does not count.
func EndedAncestor(rows []Job, id string) (Job, bool) {
	for _, a := range Ancestors(rows, id) {
		if a.State.Terminal() {
			return a, true
		}
	}
	return Job{}, false
}

// Ancestors walks id's parent chain nearest first. It stops at a parent the
// jobs do not carry and at a cycle, since either means the plan is damaged.
//
// A scan per step rather than an index: chains are a few jobs deep, and every
// list flags each live job through this. A map of the whole store per call
// made one list quadratic in allocations. The chain walked so far is the
// cycle check.
func Ancestors(rows []Job, id string) []Job {
	find := func(id string) (Job, bool) {
		i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == id })
		if i < 0 {
			return Job{}, false
		}
		return rows[i], true
	}
	var out []Job
	seen := func(id string) bool { return slices.ContainsFunc(out, func(a Job) bool { return a.ID == id }) }
	cur, ok := find(id)
	for ok && cur.Parent != "" && cur.Parent != id && !seen(cur.Parent) {
		if cur, ok = find(cur.Parent); ok {
			out = append(out, cur)
		}
	}
	return out
}

// Descendants are the jobs below id by parent chain, in store order.
func Descendants(rows []Job, id string) []Job {
	var out []Job
	for _, r := range rows {
		if r.ID != id && slices.ContainsFunc(Ancestors(rows, r.ID), func(a Job) bool { return a.ID == id }) {
			out = append(out, r)
		}
	}
	return out
}

// NewList wraps the jobs and derives the overlaps, on READ and never stored.
//
// ReportedBase, BaseVerdict and Registered are NOT derived here: they live on
// the job and are computed once, when the worker registers.
//
// This is the single door onto a report, so the empty case is normalized HERE.
// An unwritten ledger serves "jobs":[] rather than null.
func NewList(jobs []Job) List {
	if jobs == nil {
		jobs = []Job{}
	}
	var blocked []Block
	var readOnly []ReadOnly
	for _, row := range jobs {
		if b, ok := BlockedOn(jobs, row); ok && row.State.Live() {
			blocked = append(blocked, b)
		}
		if lacks := row.Unmet(); lacks != nil {
			readOnly = append(readOnly, ReadOnly{Job: row.ID, Lacks: lacks})
		}
	}
	return List{Jobs: jobs, Overlaps: jobOverlaps(jobs), Blocked: blocked, ReadOnly: readOnly}
}

// jobOverlaps reports every pair of jobs whose declared write paths
// intersect, in ledger order.
//
// Only a [State.Editing] job is in a pair. A released, exited or finished job is not
// competing for a path (that is the whole shape of the skill's early-release rule,
// where a worker shrinks its write paths so a waiter can start), and reporting one
// would make the report noisiest exactly when the plan is winding down. A job blocked on
// a dependency is not in any pair either: it claims nothing until that dependency passes.
func jobOverlaps(jobs []Job) []Overlap {
	claims := func(j Job) bool {
		_, blocked := BlockedOn(jobs, j)
		return j.State.Editing() && len(j.HardPaths()) > 0 && !blocked
	}
	var out []Overlap
	for i, a := range jobs {
		if !claims(a) {
			continue
		}
		for _, b := range jobs[i+1:] {
			if !claims(b) {
				continue
			}
			if pa, pb := intersectingPaths(a.HardPaths(), b.HardPaths()); len(pa) > 0 {
				out = append(out, Overlap{JobA: a.ID, JobB: b.ID, PathsA: pa, PathsB: pb, Claims: claimsVerdict(pa, pb)})
			}
		}
	}
	return out
}

// claimsVerdict is ClaimsDisjoint when the only ground two sides share is a file each
// claims different declarations of, ClaimsShared when a declaration claim meets a claim
// that covers it, and "" when neither side claims below the file.
func claimsVerdict(pathsA, pathsB []string) string {
	claimed := func(p string) bool { _, d := SplitClaim(p); return d != "" }
	if !slices.ContainsFunc(pathsA, claimed) && !slices.ContainsFunc(pathsB, claimed) {
		return ""
	}
	for _, pa := range pathsA {
		for _, pb := range pathsB {
			if PathsIntersect(pa, pb) {
				return ClaimsShared
			}
		}
	}
	return ClaimsDisjoint
}

// intersectingPaths collects the declared paths that cover common ground at the file
// level, each side kept in its own list and deduped. Two claims on different declarations
// of one file are collected: they are an integration order, and the pair says so through
// its Claims verdict rather than by disappearing.
func intersectingPaths(a, b []string) (pathsA, pathsB []string) {
	for _, pa := range a {
		for _, pb := range b {
			if !pathsShareGround(pa, pb) {
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
//
// Two claims on one file naming different declarations (`run.go#A`, `run.go#B`) do not
// intersect; a claim with a declaration intersects any entry covering its file without one.
// Declarations are compared as written, so `run.go#A` and `run.go#func A() {` read as
// disjoint although both name one function: the footprint is what catches that pair.
func PathsIntersect(a, b string) bool {
	if !pathsShareGround(a, b) {
		return false
	}
	_, declA := SplitClaim(a)
	_, declB := SplitClaim(b)
	return declA == "" || declB == "" || declA == declB
}

// pathsShareGround is PathsIntersect at the file level, declarations ignored.
func pathsShareGround(a, b string) bool {
	// An entry that names nothing claims nothing. It cleans to ".". The
	// whole-tree rule below would read that as a claim on everything.
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	if lit, glob, ok := literalAgainstGlob(a, b); ok {
		return globReaches(glob, lit)
	}
	a, b = LiteralPrefix(a), LiteralPrefix(b)
	if a == "" || b == "" {
		// A pattern with no literal prefix ("**/*.go") claims the whole tree.
		return true
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// literalAgainstGlob returns the cleaned literal and the cleaned glob, and true, when
// exactly one of a and b holds a glob metacharacter and the literal is not the root. Any
// other pair reports false: two literals and two globs keep the prefix rule, since deciding
// whether two globs share a path is a solver and a missed collision costs more than a pair
// read and dismissed.
func literalAgainstGlob(a, b string) (lit, glob string, ok bool) {
	a, _ = SplitClaim(a)
	b, _ = SplitClaim(b)
	a, b = path.Clean(a), path.Clean(b)
	switch aMeta, bMeta := isGlobMeta(a), isGlobMeta(b); {
	case aMeta && !bMeta && b != ".":
		return b, a, true
	case bMeta && !aMeta && a != ".":
		return a, b, true
	}
	return "", "", false
}

// globReaches reports whether glob can match lit, an ancestor of lit, or a path beneath
// lit, segment by segment, so "internal/guard/*_test.go" reaches "internal/guard" and not
// "internal/guard/guard.go". A `**` segment can absorb anything below it and so answers
// true, as does a glob that cannot be split on "/" (see [unsplittableGlob]): the answer
// errs toward overlapping.
func globReaches(glob, lit string) bool {
	if unsplittableGlob(glob) {
		return true
	}
	litSegs := strings.Split(lit, "/")
	for i, seg := range strings.Split(glob, "/") {
		if strings.Contains(seg, "**") || i >= len(litSegs) {
			return true
		}
		if ok, err := doublestar.Match(seg, litSegs[i]); err == nil && !ok {
			return false
		}
	}
	return true
}

// unsplittableGlob reports whether glob cannot be read one "/" segment at a time: a "/"
// sits inside a {...} alternation, or the braces do not balance, so no split is sure to
// keep an alternation whole.
func unsplittableGlob(glob string) bool {
	depth, slash := 0, false
	for _, r := range glob {
		switch {
		case r == '{':
			depth++
		case r == '}':
			depth--
		case r == '/' && depth > 0:
			slash = true
		}
	}
	return slash || depth != 0
}

// LiteralPrefix is the part of a declared path that names actual directories: the
// cleaned path itself when it holds no glob metacharacter, and everything above the
// first segment that does when it holds one.
//
// Exported because the focus rule asks the same question of the same
// declarations. A wildcard cannot say which project a write path lands in.
// Two packages deriving that prefix by their own rules would disagree the
// day the rules drifted. A claimed declaration (see SplitClaim) is
// not part of the path and is dropped.
func LiteralPrefix(p string) string {
	p, _ = SplitClaim(p)
	p = path.Clean(p)
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

// PathWithin reports whether the declared path inner names no file outer does not, so a
// holder replacing outer with inner only narrows. A literal is within a glob that matches
// it, a literal names itself and everything beneath it, `F#d` is within `F` and within a
// glob matching F, and a glob is within a literal directory holding its literal prefix or
// within `<prefix>/**` and `<prefix>/**/<last>` when its prefix is under that one. Against
// `<prefix>/**/*<suffix>`, a last segment glob is within it when every name it matches
// ends in the literal suffix: `internal/job/*_test.go` is within `internal/**/*.go`.
//
// It errs toward false: two globs of any other shape are never within one another, since
// proving one glob's matches a subset of another's is a solver, and a refused narrowing
// costs the holder one widening where a wrong yes hands it a file nobody granted.
func PathWithin(inner, outer string) bool {
	innerFile, innerDecl := SplitClaim(inner)
	outerFile, outerDecl := SplitClaim(outer)
	if innerFile == "" || outerFile == "" {
		return false
	}
	innerFile, outerFile = path.Clean(innerFile), path.Clean(outerFile)
	innerGlob := isGlobMeta(innerFile)
	switch {
	case outerDecl != "":
		return innerDecl == outerDecl && innerFile == outerFile && !innerGlob
	case innerFile == outerFile, outerFile == ".":
		return true
	case !isGlobMeta(outerFile):
		return underDir(LiteralPrefix(innerFile), outerFile)
	case !innerGlob:
		ok, err := doublestar.Match(outerFile, innerFile)
		return err == nil && ok
	}
	prefix, last, ok := starStarShape(outerFile)
	if !ok || !underDir(LiteralPrefix(path.Dir(innerFile)), prefix) {
		return false
	}
	base := path.Base(innerFile)
	if last == "" || base == last {
		return true
	}
	if isGlobMeta(base) {
		return segmentWithin(base, last)
	}
	matched, err := doublestar.Match(last, base)
	return err == nil && matched
}

// segmentWithin reports whether every name the glob segment inner matches is one the
// segment outer matches, for an outer of the form `*<suffix>` with a literal suffix. A
// match for inner always ends in the literal run after inner's last metacharacter, so
// that run ending in the suffix is enough, and an inner ending in a metacharacter, or
// `**`, can end in anything.
func segmentWithin(inner, outer string) bool {
	if inner == "**" || !strings.HasPrefix(outer, "*") || isGlobMeta(outer[1:]) {
		return false
	}
	suffix := outer[1:]
	if suffix == "" {
		return true
	}
	tail := inner[strings.LastIndexAny(inner, "*?[]{}")+1:]
	return tail != "" && strings.HasSuffix(tail, suffix)
}

// underDir reports whether p is dir or beneath it; every path is under the root, "".
func underDir(p, dir string) bool {
	return dir == "" || p == dir || strings.HasPrefix(p, dir+"/")
}

// starStarShape splits a glob of the form `<prefix>/**` or `<prefix>/**/<last>`, with a
// literal prefix and a last segment holding no `**`, and reports false for any other shape.
func starStarShape(glob string) (prefix, last string, ok bool) {
	if unsplittableGlob(glob) {
		return "", "", false
	}
	prefix = LiteralPrefix(glob)
	rest := strings.TrimPrefix(strings.TrimPrefix(glob, prefix), "/")
	segs := strings.Split(rest, "/")
	switch {
	case segs[0] != "**":
		return "", "", false
	case len(segs) == 1:
		return prefix, "", true
	case len(segs) == 2 && !strings.Contains(segs[1], "**"):
		return prefix, segs[1], true
	}
	return "", "", false
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

// Features are the Requires entries this build implements, sorted. A
// job requiring another is read and listed, and only writing it is
// refused.
func Features() []string {
	return []string{"claim-declarations", "dead-job-end", "job-generations", "signature-goals"}
}

// Unmet returns the entries of Requires that [Features] does not name,
// in job order, or nil when this build can write the job.
func (j Job) Unmet() []string {
	var out []string
	for _, r := range j.Requires {
		if !slices.Contains(Features(), r) {
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

// Clone returns a deep copy: no slice, map or pointer it holds is shared with u, so a
// value handed out of a store is safe to keep. Nil stays nil, so a job that stored null
// does not come back as []. Its goals come back resolved (see [Goal.Resolve]).
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
	c.Goals = cloneGoals(j.Goals)
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
	if j.Check != nil {
		check := *j.Check
		check.Args = slices.Clone(j.Check.Args)
		c.Check = &check
	}
	if j.Attempt != nil {
		attempt := *j.Attempt
		attempt.ExtraArgs = slices.Clone(j.Attempt.ExtraArgs)
		c.Attempt = &attempt
	}
	c.GateAttempts = slices.Clone(j.GateAttempts)
	for i := range c.GateAttempts {
		c.GateAttempts[i].Attempt.ExtraArgs = slices.Clone(c.GateAttempts[i].Attempt.ExtraArgs)
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
		r := *j.Rejection
		r.Unmet = cloneVerificationStatuses(j.Rejection.Unmet)
		r.Violations = slices.Clone(j.Rejection.Violations)
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

// ParsePauseUntil reads a pause's --until as the unix second it lapses: a duration from now
// or an RFC3339 instant. Empty is zero, a pause with no bound.
func ParsePauseUntil(raw string, now time.Time) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, &PauseUntilError{Until: raw, Reason: "is not ahead of now, as a positive duration (2h) or a later instant is"}
		}
		return now.Add(d).Unix(), nil
	}
	if at, err := time.Parse(time.RFC3339, raw); err == nil {
		return at.Unix(), nil
	}
	return 0, &PauseUntilError{Until: raw, Reason: "is neither a duration such as 2h nor an RFC3339 instant such as 2006-01-02T15:04:05Z"}
}

// PauseUntilError is a pause bound [ParsePauseUntil] refuses, and why.
type PauseUntilError struct {
	Until  string
	Reason string
}

func (e *PauseUntilError) Error() string { return fmt.Sprintf("--until %q %s", e.Until, e.Reason) }

// HoldsNoBuildSeat reports whether the worker for u can never take a build
// seat. It writes nothing and declares no check, so it runs no target.
func (j Job) HoldsNoBuildSeat() bool {
	if !j.ReadOnly || len(j.Paths.Write) > 0 {
		return false
	}
	return !slices.ContainsFunc(j.EffectiveGoals(), func(g Goal) bool { return g.Resolve().Kind == GoalKindCheck })
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
	// Unknown holds every member the record's type does not declare, re-emitted unchanged
	// when the record is written back. The record's MarshalJSON and UnmarshalJSON keep it,
	// under encoding/json and encoding/json/v2 alike.
	Unknown map[string]codec.RawMessage `json:"-" yaml:"-" buzz:"-" codec:"unknown"`
}

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
	s.Unknown = maps.Clone(s.Unknown)
	return s
}

// MarshalJSON writes the result with the members it kept in Unknown.
func (r Result) MarshalJSON() ([]byte, error) {
	type plain Result
	return codec.MarshalWithUnknown(plain(r), r.Unknown)
}

// UnmarshalJSON reads the result, keeping the members it does not declare in Unknown.
func (r *Result) UnmarshalJSON(data []byte) error {
	type plain Result
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*r = Result(p)
	r.Unknown = unknown
	return nil
}

// MarshalJSON writes the declaration with the members it kept in Unknown.
func (d Declaration) MarshalJSON() ([]byte, error) {
	type plain Declaration
	return codec.MarshalWithUnknown(plain(d), d.Unknown)
}

// UnmarshalJSON reads the declaration, keeping the members it does not declare in
// Unknown. libs/codec's UnmarshalStrict refuses them instead.
func (d *Declaration) UnmarshalJSON(data []byte) error {
	type plain Declaration
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*d = Declaration(p)
	d.Unknown = unknown
	return nil
}

// isGlobMeta reports whether s holds a glob metacharacter. It copies magus's
// types.IsGlobMeta, which the module may not import.
func isGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[{") }

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
