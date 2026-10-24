package types

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// errInvalidJob opens every error a job record's validation returns.

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
// server's own housekeeping from work a session was handed without asking a second door.
type JobHolder string

const (
	// HolderServer is a job from the server's maintenance catalog: the server declares it,
	// submits it, and runs it itself.
	HolderServer JobHolder = "server"
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
	// Empty when Script names the check instead.
	Target string `json:"target"             yaml:"target" schema:"target"`
	// Script is a Buzz script the check runs with `magus buzz --record` in place of a
	// target, for a fact no target checks: a passing recorded run of it is the evidence. A
	// check names a target or a script, never both.
	Script string `json:"script,omitempty"   yaml:"script,omitempty"`
	// Project is the project the target runs in, "." when the row names none.
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
	// Both are RESOLVED before a row is stored: Resolve fills a goal that named neither,
	// so a reader of a stored row never applies a default. A default applied on read is a
	// default every reader has to know about, and the readers here are the verifier, the
	// guard, the observer and two schemas.
	Kind GoalKind `json:"kind" yaml:"kind"`
	// Expect is optional and defaults to the kind's own: passed for check, changed for
	// paths and symbol. The common goal names only its kind and its subject.
	Expect GoalExpect `json:"expect,omitempty" yaml:"expect,omitempty"`
	// Check is the run a GoalKindCheck goal examines. Zero on every other kind.
	Check LeaseCheck `json:"check,omitempty" yaml:"check,omitempty"`
	// Paths are the globs a GoalKindPaths goal examines. Zero on every other kind.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	// Symbols are the names a GoalKindSymbol goal examines, as the knowledge graph
	// resolves them. Zero on every other kind.
	Symbols []string `json:"symbols,omitempty" yaml:"symbols,omitempty"`
	// Signature is the declaration an ExpectSignature goal holds its one symbol to, in the
	// form the symbol index renders it. A fork that declares none records the indexed one,
	// which is how a stub the root wrote becomes the contract. Zero on every other goal.
	Signature string `json:"signature,omitempty" yaml:"signature,omitempty"`
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
		g.Expect = g.Kind.DefaultExpect()
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

// GoalKinds is the closed set, for the same reason JobStates is one: the validator, the
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

// DefaultExpect is what a kind means when a goal names no condition, so the common goal of
// each kind declares only its subject. A check is asked whether it passed; files and
// symbols are asked whether this job changed them, which is the question a job is for.
func (k GoalKind) DefaultExpect() GoalExpect {
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
		return []string{g.Check.String()}
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
func (u Job) EffectiveGoals() []Goal {
	goals := cloneGoals(u.Goals)
	if u.Check != nil {
		primary := Goal{ID: PrimaryGoalID, Check: *u.Check}.Resolve()
		goals = append([]Goal{primary}, goals...)
	}
	return goals
}

// String renders the check as the command a person types and a row stores. Running it
// satisfies the check: the charms it executes under, default_charms included, are the
// charms the check is resolved to (internal/job.ranCheck).
func (c LeaseCheck) String() string {
	if c.Script != "" {
		line := "magus buzz --record " + c.Script
		if c.Test {
			line = "magus buzz -t " + c.Script
		}
		if len(c.Args) > 0 {
			line += " -- " + strings.Join(c.Args, " ")
		}
		return line
	}
	project := c.Project
	if project == "" {
		project = "."
	}
	line := fmt.Sprintf("magus run %s %s", c.Target, project)
	if c.NoDefaultCharms {
		line += " --no-default-charms"
	}
	if len(c.Args) > 0 {
		line += " -- " + strings.Join(c.Args, " ")
	}
	return line
}

// SameLeaseCheck reports whether a and b declare the same check. Two nil checks are the
// same, and a nil check is not the same as any declared one. A function over pointers
// rather than a method, because a row's Check is optional and either side may be nil.
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
			return LeaseCheck{}, fmt.Errorf("a check is `<target> <project> [-- args]` and %q carries the flag %s,"+
				" flags belong after `--`, where they reach the tool rather than magus", s, w)
		}
	}
	words = positional
	switch {
	case len(words) == 0:
		return LeaseCheck{}, errors.New("a check is `<target> <project> [-- args]` and this one names no target")
	case len(words) > 2:
		return LeaseCheck{}, fmt.Errorf("a check is `<target> <project> [-- args]` and %q carries %d words before any `--`", s, len(words))
	}
	c := LeaseCheck{Target: words[0], Project: ".", Args: args, NoDefaultCharms: noDefaultCharms}
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
	if len(words) > 0 && words[0] != "run" && words[0] != "buzz" {
		words = words[1:]
	}
	if len(words) >= 3 && words[0] == "buzz" && words[1] == "--record" {
		c := LeaseCheck{Script: words[2]}
		if rest := words[3:]; len(rest) > 0 {
			if rest[0] != "--" {
				return LeaseCheck{}, fmt.Errorf("a script check line is `magus buzz --record <script> [-- args]` and %q is not one", s)
			}
			c.Args = rest[1:]
		}
		return c, nil
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
// rejects sends it back to the same write paths; dropping the job out of live here would
// leave every write after `job exit` graded by nothing at all. What an exited job no
// longer does is claim its paths against another job: it blocks no other fork and is in
// no overlap (see [JobState.Editing]).
func (s JobState) Live() bool {
	return s == StateDeclared || s == StateRunning || s == StateExited
}

// Editing reports whether a holder may still be writing the job's paths: declared or
// running. Only an editing job claims its write paths against another job's.
func (s JobState) Editing() bool {
	return s == StateDeclared || s == StateRunning
}

// PathHolders are the rows that hold their write paths against everyone else now: the
// [JobState.Editing] ones. An exited row filed its result and holds nothing, so an output
// in its paths is anyone's to regenerate. The write judge and the run-output judge both
// read ownership here. rows is not modified.
func PathHolders(rows []Job) []Job {
	return slices.DeleteFunc(slices.Clone(rows), func(r Job) bool { return !r.State.Editing() })
}

// DefaultJobWeight is the weight of a job that names none, and of a run bound to no job.
const DefaultJobWeight = 0.5

// ValidJobWeight reports whether w is in (0,1]. Out of range is refused, never clamped.
func ValidJobWeight(w float64) bool { return w > 0 && w <= 1 }

// MaxJobIDLen bounds a lease id: long enough for a branch-shaped ledger name, short
// enough that the id stays a correlation key rather than a payload riding every event
// line.
const MaxJobIDLen = 128

// ValidJobID reports whether id may be stamped as a lease and checked out as a branch:
// see [JobIDRule].
//
// The narrowness is a security property: a lease id is EXEMPT from the redaction
// internal/trail applies to every other event field, so every channel that stamps one
// passes its candidate through here first. It lives beside [Job] because the ledger and
// the trail both read the id.
func ValidJobID(id string) bool { return JobIDRule(id) == "" }

// JobIDRule names the rule id breaks, or returns "" for a valid id. The id is also the
// branch ForkWorktree creates, so it is held to what every driver accepts as one:
// lowercase letters, digits, '-' and '.', with '/' only between segments, no segment
// empty or starting with '.' or '-', never ':' or '..', at most [MaxJobIDLen] characters.
func JobIDRule(id string) string {
	switch {
	case id == "":
		return "an id is not empty"
	case len(id) > MaxJobIDLen:
		return fmt.Sprintf("an id is at most %d characters", MaxJobIDLen)
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

// JobSchemaVersion is the version of the row shape this magus writes: stamped on every row
// it writes, and the newest a client may send. [JobSchema] is its ledger.
//
// AN ADDED FIELD DOES NOT MOVE IT. A reader ignores a member it does not know and a writer
// carries that member back out ([Schema].Unknown), so a row a newer magus wrote survives an
// older one's rewrite whole. The 2026-09-11 loss of write_paths and four other fields was a
// writer dropping what it did not declare, which the bag closes; a bump would only have
// refused the rows. A removed or renamed name goes into JobSchema.Reserved and is never
// declared again, and a change of meaning is a new name.
//
// It moves only for a change an older reader would act on WRONGLY, and that version names
// the feature in JobSchema.Requires, which the store stamps on each row whose content needs
// it. 13 is generations and attempts: an older binary re-forking a row would leave
// Generation behind the spec, so a stale result reads as current, and would ignore
// MaxAttempts. 12 is the `signature` expectation on a symbol goal: an older binary would
// grade it as `changed`. 11 is the `<path>#<declaration>` claim grammar in write_paths
// (see SplitClaim): an older binary matches `run.go#RunCI` against no file and would stop
// grading the claim. 10 is the field set that ends dead jobs.
// TestSchemaLedgersEvolveAdditively fails a bump whose diff is only additive.
const JobSchemaVersion = 13

// JobResultSchemaVersion is the version of the result shape a holder files, the newest the
// decoder accepts. [JobResultSchema] is its ledger, under JobSchemaVersion's rules.
const JobResultSchemaVersion = 2

// JobWriteProof is what the fork could prove about a job's write paths against the other
// live jobs bound to the SAME CHECKOUT, or not bound to any yet, at the moment it was
// declared.
//
// Recorded rather than enforced, with one exception (write paths covering a workspace-load
// file, which is refused outright). Two workers overlapping is a call only the
// orchestrator can make: it may have sequenced them, or split one file deliberately. What
// nobody could do before is READ that call back afterwards, so a plan full of overlapping
// write paths and a plan whose write paths were checked looked identical.
type JobWriteProof string

const (
	// WriteProofAlone is a fork made when no other live job with write paths was bound to
	// the checkout or still waiting to be taken in it. There was nothing to be disjoint
	// FROM, which is not the same claim as disjoint and is why it is its own value.
	WriteProofAlone JobWriteProof = "alone"
	// WriteProofDisjoint is a job whose write paths intersect no other such job's here.
	WriteProofDisjoint JobWriteProof = "disjoint"
	// WriteProofOverlapping is one whose write paths intersect another's. The fork stands;
	// the row says so.
	WriteProofOverlapping JobWriteProof = "overlapping"
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

// JobResult is the result a holder files for a job.
type JobResult struct {
	Schema          `yaml:",inline"`
	Job             string              `json:"job,omitempty" yaml:"job,omitempty"`
	ChangedPaths    []string            `json:"changed_paths" yaml:"changed_paths"`
	Validation      JobResultValidation `json:"validation,omitempty" yaml:"validation,omitempty"`
	GateEvidence    []GateEvidence      `json:"gate_evidence,omitempty" yaml:"gate_evidence,omitempty"`
	Descendants     []string            `json:"descendants,omitempty" yaml:"descendants,omitempty"`
	UnresolvedRisks []string            `json:"unresolved_risks" yaml:"unresolved_risks"`
	// ObservedGeneration is the job's Generation when the result was filed, stamped by the
	// store rather than taken from the holder.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
}

// GateEvidence links a goal to captured Magus output.
type GateEvidence struct {
	GateID    string `json:"gate_id" yaml:"gate_id"`
	OutputRef string `json:"output_ref" yaml:"output_ref"`
}

// JobResultValidation cites the output for a job's primary check.
type JobResultValidation struct {
	Command   string `json:"command" yaml:"command"`
	OutputRef string `json:"output_ref" yaml:"output_ref"`
}

// JobStatus reports the result of verifying a job.
type JobStatus struct {
	Job             string       `json:"job" yaml:"job"`
	Verified        bool         `json:"verified" yaml:"verified"`
	Violations      []string     `json:"violations,omitempty" yaml:"violations,omitempty"`
	UnresolvedRisks []string     `json:"unresolved_risks,omitempty" yaml:"unresolved_risks,omitempty"`
	Command         string       `json:"command,omitempty" yaml:"command,omitempty"`
	Gates           []GateStatus `json:"gates,omitempty" yaml:"gates,omitempty"`
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
	Entries []JobEntry `json:"entries,omitempty" yaml:"entries,omitempty"`
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
func (s JobStatus) Clean() bool { return len(s.UnresolvedRisks) == 0 }

// GateStatus reports verification of one goal.
type GateStatus struct {
	ID         string   `json:"id" yaml:"id"`
	Verified   bool     `json:"verified" yaml:"verified"`
	OutputRef  string   `json:"output_ref,omitempty" yaml:"output_ref,omitempty"`
	Violations []string `json:"violations,omitempty" yaml:"violations,omitempty"`
}

// JobAttempt is the stored record for one captured run.
type JobAttempt struct {
	Found       bool   `json:"found" yaml:"found"`
	Ref         string `json:"ref,omitempty" yaml:"ref,omitempty"`
	TimestampMs int64  `json:"timestamp_ms,omitempty" yaml:"timestamp_ms,omitempty"`
	Project     string `json:"project,omitempty" yaml:"project,omitempty"`
	Target      string `json:"target,omitempty" yaml:"target,omitempty"`
	Spell       string `json:"spell,omitempty" yaml:"spell,omitempty"`
	Failed      bool   `json:"failed,omitempty" yaml:"failed,omitempty"`
	// AttemptID names this execution alone, where Ref may name its step, which a later run
	// of the same step takes over.
	AttemptID string `json:"attempt_id,omitempty" yaml:"attempt_id,omitempty"`
	// DurationMs is how long the run took; TimestampMs is when it ENDED, so the run started
	// at TimestampMs-DurationMs.
	DurationMs int64 `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
	// Revision is the VCS revision the run read its inputs at, empty when unknown. Dirty says
	// the tree also held uncommitted changes then, so Revision alone does not name what it read.
	Revision string `json:"revision,omitempty" yaml:"revision,omitempty"`
	Dirty    bool   `json:"dirty,omitempty" yaml:"dirty,omitempty"`
	// Lease is the job lease the run acted under, empty for a run under none, such as a
	// check the orchestrator runs itself.
	Lease string `json:"lease,omitempty" yaml:"lease,omitempty"`
	// ExtraArgs are the args the run forwarded after `--`. Nil when it forwarded none or the
	// record predates the field, so only a non-nil value is compared with a check's args.
	ExtraArgs []string `json:"extra_args,omitempty" yaml:"extra_args,omitempty"`
	// ObservedGeneration is the job's Generation when the store recorded the run against it.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
}

// JobGateAttempt is the stored output record for one goal.
type JobGateAttempt struct {
	GateID  string     `json:"gate_id" yaml:"gate_id"`
	Attempt JobAttempt `json:"attempt" yaml:"attempt"`
}

// String renders the recorded command.
func (a JobAttempt) String() string {
	target := a.Target
	if a.Spell != "" {
		target = fmt.Sprintf("%s::%s", a.Spell, target)
	}
	return LeaseCheck{Target: target, Project: a.Project, Args: a.ExtraArgs}.String()
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
	// Schema is the shape this row was written in, stamped by the store on every write and
	// never taken from a client, with every member this build does not declare. See
	// JobSchemaVersion.
	Schema `yaml:",inline"`
	// ID is the lease's identity within the plan, and the key Update upserts on. The
	// console joins its drawer rows to agent activity by this value, so an
	// orchestrator should use the same id it puts in the worker's prompt.
	ID string `json:"id" yaml:"id"`
	// Parent is the id of the lease this one was handed out under, empty for a lease the
	// root spawned. Depth is read off this chain rather than stored, so a mis-stamped depth
	// cannot disagree with the tree.
	Parent string `json:"parent,omitempty" yaml:"parent,omitempty"`
	// Criteria is what this lease is for and what done means, as one block of prose. Not
	// split into two fields: the skill requires criteria to be observable, and a separate
	// empty field would read as "none required" rather than as "the author did not write
	// any".
	//
	// PROSE, graded by a reader. The machine-checkable half is Goals, and the two are
	// deliberately separate: a condition magus can verify belongs in a goal, where it is a
	// contract, rather than in a sentence here that nothing reads.
	Criteria string `json:"criteria,omitempty" yaml:"criteria,omitempty"`
	// Checkpoint is the working state this lease was handed, in the form
	// `magus vcs checkpoint -o name` prints: the revision, plus a dirty-patch digest
	// when the tree was not clean. A string rather than an embedded VCSCheckpoint
	// because that is the form an orchestrator has at spawn time and the form a later
	// reader feeds back to `magus graph diff --rev`.
	Checkpoint string `json:"checkpoint,omitempty" yaml:"checkpoint,omitempty"`
	// WritePaths and DenyPaths are the declared write boundary. Empty on a
	// read-only lease BY DESIGN (see ReadOnly), which is why neither is required. A write
	// path `<file>#<declaration>` claims one declaration of the file (see SplitClaim).
	WritePaths []string `json:"write_paths,omitempty" yaml:"write_paths,omitempty"`
	DenyPaths  []string `json:"deny_paths,omitempty" yaml:"deny_paths,omitempty"`
	// SoftPaths are the WritePaths entries that overlap nothing until the holder's diff
	// touches them; the store then drops them from here. Always a subset of WritePaths.
	SoftPaths []string `json:"soft_paths,omitempty" yaml:"soft_paths,omitempty"`
	// ReadPaths widens what this lease may read beyond its WritePaths: the paths whose
	// projects it may also read, each widened to those projects' own dependencies when the
	// guard resolves it. A lease always reads what it writes.
	//
	// A separate field rather than a wider WritePaths, and the separation is the
	// point: a worker that has to READ a shared library must not be handed the right
	// to WRITE it, and one list cannot say both. It is also the only way to widen the
	// boundary, which is deliberate. An environment variable that switched the rule
	// off would be set once, in a wrapper, by the first worker it inconvenienced, and
	// nothing afterwards would say the boundary had stopped being checked.
	ReadPaths []string `json:"read_paths,omitempty" yaml:"read_paths,omitempty"`
	// DependsOn are the ids of leases that must land before this one, so a reader can
	// see the ordering the orchestrator committed to.
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// Integrates names the finished branches an integration job merges into its checkout,
	// with `git merge --no-ff`. A row naming any is an integration job: its write paths are
	// the files those branches changed since its checkpoint, computed at fork, and a live
	// lease on one of them is a note rather than a refusal, since the merge settles it.
	Integrates []string `json:"integrates,omitempty" yaml:"integrates,omitempty"`
	// Breaks are the flags, JSON keys, exported symbols or stored formats the root approved
	// this job breaking. Empty means the job breaks nothing on its own authority: a break
	// outside the list is a finding for the root, not a decision for the worker.
	Breaks []string `json:"breaks,omitempty" yaml:"breaks,omitempty"`
	// Model is the model or effort tier the work was matched to (principal, standard,
	// economy in the skill's table). A free string: hosts name their models differently
	// and a closed set here would force a lie for the ones that do not fit.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
	// ModelTier is the model tier the store assigned when the row was created or its Model
	// changed, with the source, reason and site that chose it. Store-computed and
	// output-only like Releases: a caller's value is replaced by the stored one, and nil
	// means the workspace declared no model tiers when the row was written.
	ModelTier *JobModelTier `json:"model_tier,omitempty" yaml:"model_tier,omitempty"`
	// BriefFormat is the format a brief to this row's worker takes, over the workspace's
	// agents.brief_format. Empty defers to magus.yaml.
	BriefFormat BriefFormat `json:"brief_format,omitempty" yaml:"brief_format,omitempty"`
	// Follows is the job this one continues: follow-up work on what that job's worker wrote.
	// FreshReason says why that worker's context is wrong for this job, which lets a fresh
	// spawn pass while that worker is still warm (see the guard's spawn-warm-worker).
	Follows     string `json:"follows,omitempty"      yaml:"follows,omitempty"`
	FreshReason string `json:"fresh_reason,omitempty" yaml:"fresh_reason,omitempty"`
	// ReworkRounds counts the times the guard saw this row's holder sent more work after it
	// handed back. Widenings counts the writes that added write paths after a holder took
	// the row. Both are store-computed like ModelTier: a caller's value is replaced by the
	// stored one.
	ReworkRounds int `json:"rework_rounds,omitempty" yaml:"rework_rounds,omitempty"`
	Widenings    int `json:"widenings,omitempty"  yaml:"widenings,omitempty"`
	// Claims are the write paths this attempt's holder added with a proof the store
	// re-proved, oldest first. Store-computed like ModelTier; a fork of the next attempt
	// starts it over.
	Claims []JobClaim `json:"claims,omitempty" yaml:"claims,omitempty"`
	// Generation counts changes to the declared spec, as Kubernetes' metadata.generation
	// does: 1 when the row is declared, one more on each fork or apply that changes a
	// declared field. Store-computed, and a store-written field never moves it. Each result,
	// attempt and grade records the Generation it observed, so one taken against an earlier
	// spec reads as stale rather than current; zero there is unknown.
	Generation int `json:"generation,omitempty" yaml:"generation,omitempty"`
	// MaxAttempts caps Attempts, as a Kubernetes Job's backoffLimit does; zero is no cap. It
	// is checked only when the row is forked again: magus never retries a job on its own.
	MaxAttempts int `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
	// MaxClaims caps how many proved claims one attempt takes, chosen by whoever declares the
	// job; zero takes the default. A fork raising it past jobs.max_claims is refused.
	MaxClaims int `json:"max_claims,omitempty" yaml:"max_claims,omitempty"`
	// Weight is a lean, never a guarantee: a run bound to the job carries it on its
	// MachineClaim and polls the budget weight/DefaultJobWeight times as often. In (0,1];
	// zero reads as DefaultJobWeight.
	Weight float64 `json:"weight,omitzero" yaml:"weight,omitempty"`
	// Paused is the unix second a person paused the job, zero while it is not. A claim the
	// holder already has finishes; exec and the guard refuse its next magus run.
	Paused int64 `json:"paused,omitzero" yaml:"paused,omitempty"`
	// Attempts numbers the live attempt from 1; each fork of an ended row starts the next.
	// Prior holds the attempts before it, newest last, at most three, as structured
	// diagnostics and never a transcript. Rejection is the last `magus job wait` that
	// refused the live attempt, which leaves State where it was. All three are
	// store-computed.
	Attempts  int            `json:"attempts,omitempty"  yaml:"attempts,omitempty"`
	Prior     []PriorAttempt `json:"prior,omitempty"     yaml:"prior,omitempty"`
	Rejection *JobRejection  `json:"rejection,omitempty" yaml:"rejection,omitempty"`
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
	// Goals are the job's definition of done that magus grades: a check that passed, paths
	// or symbols this job changed, added or removed. Check is graded as one more goal. Named
	// goals rather than gates so the word never collides with the CI gate.
	Goals []Goal `json:"goals,omitempty" yaml:"goals,omitempty"`
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
	// Entries are the acknowledged writes into this row's write paths by somebody other
	// than its holder, oldest first, at most two per job. Store-computed like Releases.
	Entries []JobEntry `json:"entries,omitempty" yaml:"entries,omitempty"`
	// WriteProof is what the fork could prove about this job's write paths against the
	// other live jobs bound to the checkout it was declared in. Store-computed and
	// output-only like Releases: it is a fact about the plan at one instant, and a caller
	// that could assert it could assert the proof it stands for. See [JobWriteProof].
	WriteProof JobWriteProof `json:"write_proof,omitempty" yaml:"write_proof,omitempty"`
	// Waivers are the waivable problems the declaration accepted, copied from it.
	Waivers []JobWaiver `json:"waivers,omitempty" yaml:"waivers,omitempty"`
	// Validated is when the store last validated the whole record, in unix seconds; zero
	// on a row never validated. Store-stamped like Created: a declaration cannot set it.
	Validated int64 `json:"validated,omitempty" yaml:"validated,omitempty"`
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
	// RegisteredBy is where the write that CREATED this row came from, stamped by the
	// store on the first write and carried unchanged afterwards. It is the row's
	// provenance, the same record the trail keeps for an action, so a row and the actions
	// that followed it join on one identity.
	//
	// Distinct from Registered below, which is when a WORKER reported the base it landed
	// on: one says who declared the work, the other when somebody turned up to do it.
	RegisteredBy Origin `json:"registered_by,omitempty" yaml:"registered_by,omitempty"`
	// Registered is unix seconds, stamped by the store on the write that recorded
	// ReportedBase, off the same clock read as Updated. No write door accepts it from a
	// caller, for the reason Created and Updated do not: a client-supplied timestamp is a
	// fact about the client's clock.
	Registered int64 `json:"registered,omitempty" yaml:"registered,omitempty"`
	// CheckoutRoot is the absolute root of the checkout `magus job exec` took this job in,
	// stamped by the store on that write. Once the directory is gone, nobody can be
	// holding the job, and the store ends it.
	CheckoutRoot string `json:"checkout_root,omitempty" yaml:"checkout_root,omitempty"`
	// Worktree is the checkout a fork made for this job (see [Declaration.Checkout]), nil
	// when the job works wherever it is taken. Store-computed like CheckoutRoot: only the
	// fork that made it and the removal at the job's end write it.
	Worktree *JobWorktree `json:"worktree,omitempty" yaml:"worktree,omitempty"`
	// EndReason is why the row ended as no_return: the sweep's reason when magus ended it
	// itself, or which end a holder's or a person's exit was. Store-computed; see
	// job.Store.List for the sweep's rules.
	EndReason string `json:"end_reason,omitempty" yaml:"end_reason,omitempty"`
	// Created and Updated are unix seconds, stamped by the store on write and
	// output-only to callers: a client-supplied timestamp is a fact about the client's
	// clock, not about when the row was recorded.
	//
	// Updated is the row's heartbeat, moved only by the row's own writes: the guard noting
	// an unattributed write inside its paths leaves it alone, or a dead row would look
	// alive for as long as other agents kept working near it.
	Created int64 `json:"created" yaml:"created"`
	Updated int64 `json:"updated" yaml:"updated"`
	// Deadline is unix seconds past which the guard denies this lease's writes, zero for no
	// bound. The store stamps it from a fork's timeout and no door accepts it, for the reason
	// Created is not accepted. A taken row that passes it ends as no_return on the next read
	// of the store, and an untaken one is ended by `magus job prune`.
	Deadline int64 `json:"deadline,omitempty" yaml:"deadline,omitempty"`
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
	// GateAttempts are the portable output-store snapshots for every explicit goal.
	// Attempt remains the snapshot for the primary Check.
	GateAttempts []JobGateAttempt `json:"gate_attempts,omitempty" yaml:"gate_attempts,omitempty"`
	// LastRun is the job's most recent run, nil until one is submitted. It is filled in
	// two steps because no single writer sees the whole of it: the invocation id at
	// submit, and what the run cost and whether it worked when it ends.
	//
	// The SIZE of what the job maintains is deliberately not stored beside it: a trail, a
	// run log and a cache all grow without the row being written, so a stored figure goes
	// stale in silence. It is measured when the job is listed.
	LastRun *JobRun `json:"last_run,omitempty" yaml:"last_run,omitempty"`
	// Integration is the latest grade of this job's check goals in another tree, nil until
	// `magus job wait --integration` records one. Store-computed like Result, and kept
	// beside State rather than moving it: a job that passed where it was written and fails
	// once merged is two facts, and one field could hold only the later.
	Integration *JobIntegration `json:"integration,omitempty" yaml:"integration,omitempty"`
	// WaiterEvidence names the goals whose passing run came from the waiter's checkout
	// rather than the holder's, nil unless `magus job wait --evidence` took some. It sits
	// beside GateAttempts, which keep the holder's own attempts untouched: a goal that
	// failed where the work was written and passed once re-run here is two facts. Each
	// Gates entry is one goal, the ref taken for it and its verdict; Checkout and At say
	// where and when.
	//
	// A JobIntegration because the record is the same one --integration keeps: goals graded
	// against runs in another checkout. What differs is only that these verdicts moved the
	// job's state, so the record exists only for a job that passed.
	WaiterEvidence *JobIntegration `json:"waiter_evidence,omitempty" yaml:"waiter_evidence,omitempty"`
}

// JobWorktree is a checkout `magus job fork --checkout` made for one job, which the job
// owns: the job's holder takes it there and nowhere else, and magus removes it once the job
// ends unless removing it would lose work.
type JobWorktree struct {
	// Root is the checkout's top level, absolute.
	Root string `json:"root" yaml:"root"`
	// Source is the checkout the fork made it from, which removes it: an hg share or a jj
	// workspace is recorded only there.
	Source string `json:"source" yaml:"source"`
	// Base is the full revision it was made at.
	Base string `json:"base" yaml:"base"`
	// Created is unix seconds.
	Created int64 `json:"created" yaml:"created"`
	// Binary is the magus the fork placed in it, the same file the forking checkout runs,
	// "" when the forking checkout ran magus from PATH and so the job's runs it too.
	Binary string `json:"binary,omitempty" yaml:"binary,omitempty"`
	// Removed is unix seconds when magus removed it, zero while it stands.
	Removed int64 `json:"removed,omitempty" yaml:"removed,omitempty"`
	// Kept is why magus left it standing after the job ended, "" until then or once it is
	// removed.
	Kept string `json:"kept,omitempty" yaml:"kept,omitempty"`
}

// Standing reports whether the checkout has not been removed: it binds its job's holder,
// and the guard treats it as its job's own.
func (w *JobWorktree) Standing() bool { return w != nil && w.Removed == 0 }

// JobIntegration is a job's check goals graded against runs recorded in Checkout, the tree
// an integration branch was merged into, at At (unix seconds).
type JobIntegration struct {
	Checkout string       `json:"checkout" yaml:"checkout"`
	At       int64        `json:"at" yaml:"at"`
	Verified bool         `json:"verified" yaml:"verified"`
	Gates    []GateStatus `json:"gates,omitempty" yaml:"gates,omitempty"`
	// ObservedGeneration is the row's Generation when the grade was taken.
	ObservedGeneration int `json:"observed_generation,omitzero" yaml:"observed_generation,omitempty"`
}

// JobRejection is a `magus job wait` that refused the live attempt, At unix seconds.
type JobRejection struct {
	Unmet              []GateStatus `json:"unmet,omitempty"               yaml:"unmet,omitempty"`
	Violations         []string     `json:"violations,omitempty"          yaml:"violations,omitempty"`
	At                 int64        `json:"at"                            yaml:"at"`
	ObservedGeneration int          `json:"observed_generation,omitzero"  yaml:"observed_generation,omitempty"`
}

// JobDenial is one guard denial of a job's holder, Ts unix seconds. VerdictRef reopens the
// guard's verdict.
type JobDenial struct {
	Rule       string `json:"rule"                  yaml:"rule"`
	Reason     string `json:"reason,omitempty"      yaml:"reason,omitempty"`
	Action     string `json:"action,omitempty"      yaml:"action,omitempty"`
	VerdictRef string `json:"verdict_ref,omitempty" yaml:"verdict_ref,omitempty"`
	Ts         int64  `json:"ts"                    yaml:"ts"`
}

// PriorAttempt is an ended attempt of a job, folded onto the row when the job is forked
// again, so the next holder reads what failed without a transcript. Ended is unix seconds.
// DenialsKnown is false once the trail rotated past the attempt, so empty Denials then
// means unknown rather than none.
type PriorAttempt struct {
	N                  int          `json:"n"                             yaml:"n"`
	State              JobState     `json:"state"                         yaml:"state"`
	ObservedGeneration int          `json:"observed_generation,omitzero"  yaml:"observed_generation,omitempty"`
	EndReason          string       `json:"end_reason,omitempty"          yaml:"end_reason,omitempty"`
	ModelTier          string       `json:"model_tier,omitempty"          yaml:"model_tier,omitempty"`
	Checkpoint         string       `json:"checkpoint,omitempty"          yaml:"checkpoint,omitempty"`
	ReportedBase       string       `json:"reported_base,omitempty"       yaml:"reported_base,omitempty"`
	Ended              int64        `json:"ended,omitempty"               yaml:"ended,omitempty"`
	ReworkRounds       int          `json:"rework_rounds,omitempty"       yaml:"rework_rounds,omitempty"`
	Widenings          int          `json:"widenings,omitempty"        yaml:"widenings,omitempty"`
	Unmet              []GateStatus `json:"unmet,omitempty"               yaml:"unmet,omitempty"`
	Violations         []string     `json:"violations,omitempty"          yaml:"violations,omitempty"`
	UnresolvedRisks    []string     `json:"unresolved_risks,omitempty"    yaml:"unresolved_risks,omitempty"`
	// FailingRefs are the output refs of the attempt's failed runs, to reopen.
	FailingRefs  []string     `json:"failing_refs,omitempty" yaml:"failing_refs,omitempty"`
	Denials      []JobDenial  `json:"denials,omitempty"      yaml:"denials,omitempty"`
	DenialsKnown bool         `json:"denials_known"          yaml:"denials_known"`
	Retry        RetryVerdict `json:"retry"                  yaml:"retry"`
}

// Declaration is the typed INPUT for one lease row: the fields a caller DECLARES, and nothing
// the store computes. A caller cannot say when its row was created or what it released, and
// the way to make that true is for the input type not to carry those fields rather than for
// the store to strip them afterwards.
//
// It is a DECLARATION and not a merge: every field it carries is written, so an omitted one
// is cleared rather than kept. magus\job.put deliberately does the opposite,
// since an agent advancing one field of a live row must not erase the rest (see
// job.PutMerge).
//
// JSON only: this is decoded from stdin and never emitted, so it carries no yaml tags.
//
// Registered in cmd/magus-utils/boundary_types.go with no RuntimeObject: a magusfile can
// construct one, but nothing hands one back out to Buzz; the job package's decoders, all
// through its decodeDeclaration, read JSON, not a Buzz value.
type Declaration struct {
	// Schema is the row shape this record is written in. The version is required, and one
	// newer than this magus is rejected by name. See JobSchemaVersion.
	Schema
	// ID is the lease's identity within the plan, the key a second register replaces on,
	// and the only required field besides the version.
	ID string `json:"id" schema:"leaseid"`
	// Parent is the lease this one was spawned under, empty for a lease the root declared.
	Parent string `json:"parent,omitempty" schema:"leaseid"`
	// Criteria is what this lease is for and what done means, as one block of prose. The
	// machine-checkable half is Goals.
	Criteria string `json:"criteria,omitempty"`
	// Checkpoint is the working state this lease starts from, as `magus vcs checkpoint -o
	// name` prints it.
	Checkpoint string `json:"checkpoint,omitempty"`
	// WritePaths is what this lease may write, empty on a read-only row by design. An entry
	// `<file>#<declaration>` claims one declaration of the file (see SplitClaim).
	WritePaths []string `json:"write_paths,omitempty" schema:"writepath"`
	// DenyPaths are the paths inside those this lease may not write.
	DenyPaths []string `json:"deny_paths,omitempty" schema:"writepath"`
	// SoftPaths are the entries of WritePaths that are soft claims (see [Job.SoftPaths]).
	SoftPaths []string `json:"soft_paths,omitempty" schema:"writepath"`
	// ReadPaths is what it may READ, widened to those projects' dependencies by the
	// guard. Empty means write_paths stands in.
	ReadPaths []string `json:"read_paths,omitempty" schema:"writepath"`
	// DependsOn names the lease ids that must land before this one.
	DependsOn []string `json:"depends_on,omitempty" schema:"leaseid"`
	// Integrates names the branches this job merges; see [Job.Integrates].
	Integrates []string `json:"integrates,omitempty"`
	// Breaks are the flags, JSON keys, exported symbols or stored formats the root approved
	// this job breaking; empty means it breaks nothing on its own authority. See [Job.Breaks].
	Breaks []string `json:"breaks,omitempty"`
	// Model is the model or effort tier the work was matched to, a free string because
	// hosts name their models differently.
	Model string `json:"model,omitempty"`
	// Weight is the job's lean on the machine budget, in (0,1]: omitted means 0.5, and an
	// explicit 0 is out of range. See [Job.Weight].
	Weight *float64 `json:"weight,omitempty"`
	// BriefFormat is the format a brief to this job's worker takes; empty defers to
	// magus.yaml's agents.brief_format. See [Job.BriefFormat].
	BriefFormat BriefFormat `json:"brief_format,omitempty"`
	// Follows and FreshReason name the job this one continues and why its worker is not
	// resumed for it. See [Job.Follows].
	Follows     string `json:"follows,omitempty" schema:"leaseid"`
	FreshReason string `json:"fresh_reason,omitempty"`
	// Checkout is an absolute directory, outside the workspace, where the fork makes a new
	// checkout of the repository for this job at its checkpoint, or at the forking
	// checkout's revision when it names none.
	// The job owns it: see [JobWorktree]. Empty works wherever the job is taken.
	Checkout string `json:"checkout,omitempty" schema:"abspath"`
	// LegacyWritePaths is the pre-rename spelling of write_paths, accepted on input and
	// never emitted.
	//
	// compat(until: no client or stored ledger still sends owned_paths/focus/
	// forbidden_paths/tier; observe: grep the leases-*.json archives and the trail for the
	// old keys): the decoder is strict, so an old client's row would be refused as an
	// unknown member rather than understood. A row naming both spellings of one field is
	// refused, since nothing here can say which one its author meant.
	LegacyWritePaths []string `json:"owned_paths,omitempty"`
	// LegacyDenyPaths is the pre-rename spelling of deny_paths. compat: see LegacyWritePaths.
	LegacyDenyPaths []string `json:"forbidden_paths,omitempty"`
	// LegacyReadPaths is the pre-rename spelling of read_paths. compat: see LegacyWritePaths.
	LegacyReadPaths []string `json:"focus,omitempty"`
	// LegacyModel is the pre-rename spelling of model. compat: see LegacyWritePaths.
	LegacyModel string `json:"tier,omitempty"`
	// Check is the one check this lease runs, and acceptance binds a worker's evidence
	// to it.
	Check *LeaseCheck `json:"check,omitempty"`
	// State is the row's lifecycle position, empty for a row that has not said where it
	// stands. no_return is a lease that never reported, which is not a failure.
	State JobState `json:"state,omitempty"`
	// ReadOnly marks a lease that gathers evidence and writes nothing, so empty write
	// paths are correct rather than missing.
	ReadOnly bool `json:"read_only,omitempty"`
	// Validation is the check as a rendered `magus run` line.
	//
	// compat(until: no client still sends a rendered line; observe: a grep of the ledger
	// archives for a row carrying `validation` and no `check`): it is what rows declared
	// before the check record existed, so it is accepted and parsed into Check. Sending
	// both is refused rather than merged, since nothing here can say which one meant it.
	Validation string `json:"validation,omitempty"`
	// Goals are what done means, graded by `magus job wait`. See [Job.Goals].
	Goals []Goal `json:"goals,omitempty"`
	// Timeout bounds the lease, as a Go duration. The store stamps Job.Deadline from it when
	// it writes the row, so a declaration carries a length and never an instant. Empty is no
	// bound; there is no default here (a workspace may set jobs.default_timeout).
	Timeout string `json:"timeout,omitempty" schema:"duration"`
	// MaxAttempts caps how many times the job may be forked, its first fork included; zero
	// is no cap. See [Job.MaxAttempts].
	MaxAttempts int `json:"max_attempts,omitempty"`
	// MaxClaims caps how many proved claims one attempt takes; zero takes the default. See
	// [Job.MaxClaims].
	MaxClaims int `json:"max_claims,omitempty"`
	// Waivers accept waivable problems (see [Waivable]) on the record's own authority.
	Waivers []JobWaiver `json:"waivers,omitempty"`
	// Enter turns the record into an entry rather than a declaration: it acknowledges one
	// write into this path, inside the write paths of the live job ID names, by somebody
	// other than its holder. A record carrying it declares nothing else.
	Enter string `json:"enter,omitempty"`
	// Claim turns the record into a proved claim of the job ID names: each entry adds
	// one write path with the proof the store re-proves before it writes. A record carrying
	// it declares nothing else.
	Claim []JobClaimRequest `json:"claim,omitempty"`
}

// JobClaimRequest is one write path a claim record asks for, and Because the proof:
// `refs:<symbol>`, `test:<file>`, `output:<project>:<target>` or `released`. See
// [ParseClaimEvidence].
//
// Callers, given instead of Path and Because, names a symbol: `magus job apply` expands
// the request into one per declaration holding an occurrence of it, path
// `<file>#<declaration>` because `refs:<symbol>`, and the store proves each of those as
// it would one written by hand.
type JobClaimRequest struct {
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
	switch {
	case !e.Kind.Valid():
		return e, fmt.Errorf("%q names no proof, so use one of refs:<symbol>, test:<file>, output:<project>:<target> or released", because)
	case e.Kind == ClaimReleased && e.Subject != "":
		return e, fmt.Errorf("%q: released names nothing after it", because)
	case e.Kind != ClaimReleased && e.Subject == "":
		return e, fmt.Errorf("%q names no %s subject", because, e.Kind)
	case e.Kind == ClaimOutput && !strings.Contains(e.Subject, ":"):
		return e, fmt.Errorf("%q names no target, so spell it output:<project>:<target>", because)
	}
	return e, nil
}

// JobClaim is one write path a holder added with a proof. Proof is the sentence the
// store found it true by, At unix seconds and By who wrote it.
type JobClaim struct {
	Path     string        `json:"path"     yaml:"path"`
	Evidence ClaimEvidence `json:"evidence" yaml:"evidence"`
	Proof    string        `json:"proof"    yaml:"proof"`
	At       int64         `json:"at"       yaml:"at"`
	By       Origin        `json:"by"       yaml:"by"`
}

// FoldLegacyNames moves a field declared under its old name onto the one that carries it,
// refusing a row that names the same field twice. Exported for the job package's
// decodeDeclaration, the one caller outside this package: it runs between the JSON decode and Validate.
//
// compat: see the legacy fields on [Declaration].
func (r *Declaration) FoldLegacyNames() error {
	var err error
	fold := func(name string, into *[]string, from []string) {
		switch {
		case len(from) == 0:
		case len(*into) > 0:
			err = errors.Join(err, DiagnosticErrorf(JobRecordInvalid, "a row declares %s or its renamed spelling, not both", name))
		default:
			*into = from
		}
	}
	fold("owned_paths", &r.WritePaths, r.LegacyWritePaths)
	fold("forbidden_paths", &r.DenyPaths, r.LegacyDenyPaths)
	fold("focus", &r.ReadPaths, r.LegacyReadPaths)
	foldString := func(name string, into *string, from string) {
		switch {
		case from == "":
		case *into != "":
			err = errors.Join(err, DiagnosticErrorf(JobRecordInvalid, "a row declares %s or its renamed spelling, not both", name))
		default:
			*into = from
		}
	}
	foldString("tier", &r.Model, r.LegacyModel)
	r.LegacyWritePaths, r.LegacyDenyPaths, r.LegacyReadPaths = nil, nil, nil
	r.LegacyModel = ""
	return err
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

// Validate reports what is wrong with a declared row, or nil: every problem
// [Declaration.Problems] finds, joined.
func (r Declaration) Validate() error { return r.Problems().Err() }

// Problems is every problem the record shows on its own, without a workspace: each field's
// shape, the check, the waivers and the goals. It never stops at the first.
func (r Declaration) Problems() JobProblems {
	var ps JobProblems
	invalid := func(field, message, fix string) {
		ps = append(ps, JobProblem{Code: JobRecordInvalid, Field: field, Message: message, Fix: fix})
	}
	if rule := JobIDRule(strings.TrimSpace(r.ID)); rule != "" {
		invalid("id", fmt.Sprintf("%q is not a lease id: %s", r.ID, rule), "name a branch-shaped id such as fix-parser or area/fix-parser")
	}
	if r.Enter != "" {
		rest := r
		rest.Schema, rest.ID, rest.Enter = Schema{}, "", ""
		if !reflect.ValueOf(rest).IsZero() {
			invalid("enter", "a record carrying enter enters a job and declares nothing, so it carries only schema_version, id and enter",
				"drop every other field, or drop enter")
		}
		return ps
	}
	if len(r.Claim) > 0 {
		return append(ps, r.claimProblems()...)
	}
	if p := strings.TrimSpace(r.Parent); p != "" && !ValidJobID(p) {
		invalid("parent", fmt.Sprintf("parent %q is not a lease id", r.Parent), "name the id of the job this one was forked under")
	}
	for i, dep := range r.DependsOn {
		if d := strings.TrimSpace(dep); d != "" && !ValidJobID(d) {
			invalid(fmt.Sprintf("depends_on[%d]", i), fmt.Sprintf("depends_on[%d] %q is not a lease id", i, dep),
				"name the id of a job that must land first")
		}
	}
	for i, branch := range r.Integrates {
		at := fmt.Sprintf("integrates[%d]", i)
		switch b := strings.TrimSpace(branch); {
		case !ValidIntegratedBranch(b):
			invalid(at, fmt.Sprintf("%s %q is not a branch name a merge can take", at, branch),
				"name a local branch, such as the one a finished job committed on")
		case slices.Contains(r.Integrates[:i], branch):
			invalid(at, fmt.Sprintf("%s %q is named twice", at, branch), "drop the repeat")
		}
	}
	if len(r.Integrates) > 0 && r.ReadOnly {
		invalid("read_only", "an integration job merges branches, so it writes", "drop read_only, or drop integrates")
	}
	if r.State != "" && !ValidJobState(r.State) {
		invalid("state", fmt.Sprintf("state %q is not a job state", r.State), "use one of "+JobStateVocabulary())
	}
	if _, err := ParseJobTimeout(r.Timeout); err != nil {
		invalid("timeout", fmt.Sprintf("timeout %q is not a positive duration", r.Timeout), "name one such as 30m or 2h, or leave it out")
	}
	if r.BriefFormat != "" && !r.BriefFormat.Valid() {
		invalid("brief_format", fmt.Sprintf("brief_format %q is not a brief format", r.BriefFormat),
			fmt.Sprintf("use one of %s, or leave it out for magus.yaml's agents.brief_format", strings.Join(r.BriefFormat.Values(), ", ")))
	}
	if r.Weight != nil && !ValidJobWeight(*r.Weight) {
		invalid("weight", fmt.Sprintf("weight %v is outside (0,1]", *r.Weight),
			fmt.Sprintf("use a weight above 0 and at most 1, or leave it out for %v", DefaultJobWeight))
	}
	if r.MaxAttempts < 0 {
		invalid("max_attempts", fmt.Sprintf("max_attempts is %d, and it is a count of forks", r.MaxAttempts), "use a positive count, or 0 for no cap")
	}
	if r.MaxClaims < 0 {
		invalid("max_claims", fmt.Sprintf("max_claims is %d, and it is a count of claims", r.MaxClaims), "use a positive count, or 0 for the default")
	}
	if r.Checkout != "" && !filepath.IsAbs(r.Checkout) {
		invalid("checkout", fmt.Sprintf("checkout %q is relative, and a fork names the checkout it makes by an absolute path", r.Checkout),
			"name an absolute directory outside the workspace")
	}
	for _, field := range []struct {
		name  string
		paths []string
	}{{"write_paths", r.WritePaths}, {"deny_paths", r.DenyPaths}, {"read_paths", r.ReadPaths}} {
		for i, p := range field.paths {
			if why := pathOutsideWorkspace(p); why != "" {
				at := fmt.Sprintf("%s[%d]", field.name, i)
				invalid(at, fmt.Sprintf("%s %q %s, so it matches no workspace path and bounds nothing", at, p, why),
					"name it relative to the workspace root")
			}
		}
	}
	for i, p := range r.SoftPaths {
		if !slices.Contains(r.WritePaths, p) {
			at := fmt.Sprintf("soft_paths[%d]", i)
			invalid(at, fmt.Sprintf("%s %q is not in write_paths, and a soft path is a write path that overlaps nothing", at, p),
				"name an entry of write_paths, or drop it")
		}
	}
	_, declared, checkProblems := r.check()
	ps = append(ps, checkProblems...)
	for i, b := range r.Breaks {
		if strings.TrimSpace(b) == "" {
			at := fmt.Sprintf("breaks[%d]", i)
			invalid(at, at+" is blank", "name the flag, key, symbol or format the job may break, or drop the entry")
		}
	}
	for i, w := range r.Waivers {
		at := fmt.Sprintf("waivers[%d]", i)
		if !Waivable(w.Code) {
			invalid(at+".code", fmt.Sprintf("%s waives %q, which is not a waivable code", at, w.Code),
				fmt.Sprintf("waive only %s, %s or %s, and fix the record for anything else", WritePathTooBroad, OutputOutsideWritePaths, GateNotRun))
		}
		if strings.TrimSpace(w.Subject) == "" {
			invalid(at+".subject", at+" names no subject", "name the write path, `target project` or output path the problem names")
		}
		if strings.TrimSpace(w.Reason) == "" {
			invalid(at+".reason", at+" gives no reason", "say why the problem is accepted, such as \"the root regenerates it\"")
		}
	}
	return append(ps, goalsProblems(r.Goals, declared)...)
}

// claimProblems is every problem of a claim record: it carries only schema_version, id
// and claim, and each entry names one literal workspace path and a proof that parses.
func (r Declaration) claimProblems() JobProblems {
	var ps JobProblems
	invalid := func(field, message, fix string) {
		ps = append(ps, JobProblem{Code: JobRecordInvalid, Field: field, Message: message, Fix: fix})
	}
	rest := r
	rest.Schema, rest.ID, rest.Claim = Schema{}, "", nil
	if !reflect.ValueOf(rest).IsZero() {
		invalid("claim", "a record carrying claim claims a job and declares nothing, so it carries only schema_version, id and claim",
			"drop every other field, or drop claim")
	}
	for i, w := range r.Claim {
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
		case IsGlobMeta(p) || strings.Contains(p, "#"):
			invalid(at+".path", fmt.Sprintf("%s %q is a pattern or a claim, and a proof covers one file", at, w.Path), "name one file")
		}
		if _, err := ParseClaimEvidence(w.Because); err != nil {
			invalid(at+".because", fmt.Sprintf("%s: %s", at, err), "name the proof the path rests on")
		}
	}
	return ps
}

// goalsProblems is every problem of a record's goals: each goal's own shape, then what
// only the set shows. primary says the record declares a check, the goal named
// PrimaryGoalID a depends_on may name.
func goalsProblems(goals []Goal, primary bool) JobProblems {
	var ps JobProblems
	bad := func(field, message, fix string) {
		ps = append(ps, JobProblem{Code: JobGoalInvalid, Field: field, Message: message, Fix: fix})
	}
	index := make(map[string]int, len(goals))
	if primary {
		index[PrimaryGoalID] = -1
	}
	for i, gate := range goals {
		at := fmt.Sprintf("goals[%d]", i)
		ps = append(ps, gate.problems(at)...)
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
func oneSignaturePerSymbol(goals []Goal) JobProblems {
	var ps JobProblems
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
				ps = append(ps, JobProblem{
					Code:    JobGoalInvalid,
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

// Validate reports whether a goal can be matched to an output record without
// interpreting a shell command: every problem it shows on its own, joined.
func (g Goal) Validate() error { return g.problems("").Err() }

// problems is every problem g shows on its own, each Field under field (`goals[1]`).
func (g Goal) problems(field string) JobProblems {
	var ps JobProblems
	bad := func(sub, message, fix string) {
		at := sub
		if field != "" {
			at = fmt.Sprintf("%s.%s", field, sub)
		}
		ps = append(ps, JobProblem{Code: JobGoalInvalid, Field: at, Message: message, Fix: fix})
	}
	if rule := JobIDRule(strings.TrimSpace(g.ID)); rule != "" {
		bad("id", fmt.Sprintf("id %q is not a goal id: %s", g.ID, rule), "use a lowercase, branch-shaped id")
	}
	// Validates what the row WILL BE, not what arrived: a declaration off the wire has not
	// been through Resolve yet, and refusing it for naming no kind would refuse the
	// shorthand the defaults exist to allow. Resolving here is not read-time defaulting;
	// the stored row is resolved by cloneGoals on the way in.
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
		return ps
	}
	if err := g.Check.validScript(); err != nil {
		bad("check.script", err.Error(), "name a target or a script, and give a script no project or charms")
	} else if g.Check.Script == "" {
		if _, err := ParseLeaseCheck(fmt.Sprintf("%s %s", g.Check.Target, g.Check.Project)); err != nil {
			bad("check.target", err.Error(), "name one target and the project it runs in")
		}
	}
	return ps
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

// ParseJobTimeout reads a declared timeout: empty is no bound and parses to zero, anything
// else must be a positive Go duration.
func ParseJobTimeout(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, DiagnosticErrorf(JobRecordInvalid, "timeout %q is not a positive duration such as 30m or 2h", s)
	}
	return d, nil
}

// Overdue reports whether a live row has passed its deadline at now, in unix seconds. A
// terminal row has no writes left to bound, so it is never overdue.
func (u Job) Overdue(now int64) bool {
	return u.Deadline > 0 && u.State.Live() && now >= u.Deadline
}

// EndReasonSessionEnded opens the end_reason of a row the store ended because the agent
// session holding it ended. It is the one ending `magus job exec` takes back: a host may
// resume a session it reported over, and the resumed session's next exec of the row
// rebinds it.
const EndReasonSessionEnded = "its holder's session ended"

// EndedWithSession reports whether the row is no_return only because its holder's
// session ended (see [EndReasonSessionEnded]).
func (u Job) EndedWithSession() bool {
	return u.State == StateNoReturn && strings.HasPrefix(u.EndReason, EndReasonSessionEnded)
}

// StaleAt reports whether a record stamped with observed was taken against an earlier
// declared spec than the row's. A zero on either side is unknown, never stale.
func (u Job) StaleAt(observed int) bool {
	return observed != 0 && u.Generation != 0 && observed != u.Generation
}

// JobIdleSince is when row's jobs.stale_after clock started, in unix seconds, reading its
// dependencies from rows. A declared row nobody took is QUEUED while a job it depends on is
// still editing (declared or running), so waiting is true and it has no clock; once the last
// one ends or exits, the clock starts at the later of that and the row's own last update, so
// an exited dependency nobody collects cannot hold its dependent forever. Every other row's
// clock is its last update. A dependency no row declares holds nothing back.
func JobIdleSince(rows []Job, row Job) (since int64, waiting bool) {
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

// JobStaleAt reports whether row has been idle at least after as of now, in unix seconds,
// with its clock read by [JobIdleSince]: a queued row is never stale, and a zero or
// negative after is never.
func JobStaleAt(rows []Job, row Job, now int64, after time.Duration) bool {
	since, waiting := JobIdleSince(rows, row)
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

// check is the row's declared check, from either spelling, and whether it declares one at
// all. A line that does not parse is refused HERE, at the door, rather than at grading
// time, where the row is already stored and the worker has already run something.
func (r Declaration) check() (LeaseCheck, bool, JobProblems) {
	invalid := func(field string, err error, fix string) JobProblems {
		return JobProblems{{Code: JobRecordInvalid, Field: field, Message: err.Error(), Fix: fix}}
	}
	line := strings.TrimSpace(r.Validation)
	switch {
	case r.Check != nil && line != "":
		return LeaseCheck{}, false, JobProblems{{
			Code: JobRecordInvalid, Field: "validation",
			Message: "a row carries `check` or a rendered `validation` line, not both", Fix: "send `check` alone",
		}}
	case r.Check != nil && r.Check.Script != "":
		if err := r.Check.validScript(); err != nil {
			return LeaseCheck{}, false, invalid("check.script", err, "name a target or a script, and give a script no project or charms")
		}
		return LeaseCheck{Script: r.Check.Script, Args: r.Check.Args, Test: r.Check.Test}, true, nil
	case r.Check != nil:
		if err := r.Check.validScript(); err != nil {
			return LeaseCheck{}, false, invalid("check.test", err, "drop test, or name a script")
		}
		parsed, err := ParseLeaseCheck(fmt.Sprintf("%s %s", r.Check.Target, r.Check.Project))
		if err != nil {
			return LeaseCheck{}, false, invalid("check.target", err, "name one target and the project it runs in")
		}
		parsed.Args = r.Check.Args
		parsed.NoDefaultCharms = r.Check.NoDefaultCharms
		return parsed, true, nil
	case line != "":
		parsed, err := ParseLeaseRunLine(line)
		if err != nil {
			return LeaseCheck{}, false, invalid("validation", err, "send `check` with the target and project instead")
		}
		return parsed, true, nil
	}
	return LeaseCheck{}, false, nil
}

// JobStateVocabulary is the closed set of job states, as an error quotes it. Shared by
// Declaration.Validate and job.PutMerge, the two places a caller-supplied state is
// checked against the set.
func JobStateVocabulary() string {
	states := JobStates()
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// Apply writes this declaration onto a row, for job.Store.Update. Store-computed fields are
// untouched: a row that already carries releases or a registration keeps them.
func (r Declaration) Apply(u *Job) {
	u.Parent = strings.TrimSpace(r.Parent)
	u.Criteria = r.Criteria
	u.Checkpoint = strings.TrimSpace(r.Checkpoint)
	u.WritePaths = trimmedNonEmpty(r.WritePaths)
	u.DenyPaths = trimmedNonEmpty(r.DenyPaths)
	u.SoftPaths = trimmedNonEmpty(r.SoftPaths)
	u.ReadPaths = trimmedNonEmpty(r.ReadPaths)
	u.DependsOn = trimmedNonEmpty(r.DependsOn)
	u.Integrates = trimmedNonEmpty(r.Integrates)
	u.Breaks = trimmedNonEmpty(r.Breaks)
	u.Model = strings.TrimSpace(r.Model)
	u.Weight = 0
	if r.Weight != nil {
		u.Weight = *r.Weight
	}
	u.BriefFormat = r.BriefFormat
	u.Follows = strings.TrimSpace(r.Follows)
	u.FreshReason = strings.TrimSpace(r.FreshReason)
	// Validate refused an unparsable check before the row reached a store, so the problems
	// here cannot arise; the rendered line is written from the record so the two agree.
	check, declared, _ := r.check()
	u.Check, u.Validation = nil, ""
	if declared {
		u.Check, u.Validation = &check, check.String()
	}
	u.Goals = cloneGoals(r.Goals)
	u.State = r.State
	u.ReadOnly = r.ReadOnly
	u.MaxAttempts = r.MaxAttempts
	u.MaxClaims = r.MaxClaims
	u.Waivers = slices.Clone(r.Waivers)
}

// ApplySpec writes this declaration's spec onto a row, for `magus job apply`: as Apply, but
// the row's state is kept, since apply never moves a job, and so is its checkpoint when the
// declaration names none, since the checkpoint records where the work started.
func (r Declaration) ApplySpec(u *Job) {
	state, checkpoint := u.State, u.Checkpoint
	r.Apply(u)
	u.State = state
	if u.Checkpoint == "" {
		u.Checkpoint = checkpoint
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
	// Revoked is a path taken from a job a holder had taken, by a writer other than that
	// holder: the orchestrator revoking it rather than the worker giving it up.
	Revoked bool `json:"revoked,omitempty" yaml:"revoked,omitempty"`
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

// JobEntry is a party other than a job's holder acknowledging, on the record, that it
// will write one path inside the job's write paths. The guard lets exactly one such
// write through and stamps Consumed.
type JobEntry struct {
	Path string `json:"path" yaml:"path"`
	// By is where the acknowledging call came from, stamped by the store.
	By Origin `json:"by" yaml:"by"`
	// At and Consumed are unix seconds; Consumed is zero until the write lands.
	At       int64 `json:"at" yaml:"at"`
	Consumed int64 `json:"consumed,omitempty" yaml:"consumed,omitempty"`
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
	// Claims compares what the two sides DECLARED below the file, when either claims a
	// declaration (`run.go#executeStages`): ClaimsDisjoint for different declarations of the
	// files they share, ClaimsShared when a claim covers one the other side holds. Empty
	// when neither side claims below the file.
	Claims string `json:"claims,omitempty" yaml:"claims,omitempty"`
	// Footprint compares what the two jobs have actually changed, declaration by
	// declaration. Nil when no reader computed it: it needs each job's checkout and a VCS,
	// and deriving an overlap needs neither.
	Footprint *JobOverlapFootprint `json:"footprint,omitempty" yaml:"footprint,omitempty"`
}

// The verdicts a JobOverlap's Claims reaches.
const (
	ClaimsDisjoint = "disjoint"
	ClaimsShared   = "shared"
)

// The verdicts a JobOverlapFootprint reaches.
const (
	FootprintDisjoint = "disjoint"
	FootprintShared   = "shared"
	FootprintUnknown  = "unknown"
)

// JobOverlapFootprint is whether two overlapping jobs' diffs touch the same declaration.
// Like the overlap it hangs off, a report: two jobs sharing a function may have been
// planned that way.
type JobOverlapFootprint struct {
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

// JobList is what a reader of the ledger is served: the recorded rows, plus
// the overlaps derived from them. A constructor rather than a literal at each read
// door, because the MCP tool and the console's route must not be able to disagree
// about whether an overlap exists: the same reason types.NewFileReport exists.
type JobList struct {
	Jobs     []Job        `json:"jobs"               yaml:"jobs"`
	Overlaps []JobOverlap `json:"overlaps,omitempty" yaml:"overlaps,omitempty"`
	// Overdue, Orphans and Stale are live job ids a reader should look at, derived at one
	// instant by [JobList.Flag]. The store's read ends orphans and most untaken stale rows
	// first (see job.Store.List), so these mostly name work a holder took.
	Overdue []string `json:"overdue,omitempty" yaml:"overdue,omitempty"`
	Orphans []string `json:"orphans,omitempty" yaml:"orphans,omitempty"`
	Stale   []string `json:"stale,omitempty"   yaml:"stale,omitempty"`
	// Blocked are the live jobs that claim no paths yet because a dependency has not
	// passed, each naming the first such dependency. Derived with Overlaps, from the rows.
	Blocked []JobBlock `json:"blocked,omitempty" yaml:"blocked,omitempty"`
	// ReadOnly are the rows requiring a feature this magus lacks. It lists and honors them
	// and refuses every write to them.
	ReadOnly []JobReadOnly `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	// Changes are the open changes against the queue's base, joined to the jobs whose
	// checkouts are on their branches, in the order the provider listed them. Read from
	// the snapshot `magus queue ls` leaves; a list never fetches them.
	Changes []InflightChange `json:"changes,omitempty" yaml:"changes,omitempty"`
	// Fetched is when and where that snapshot was read, nil when none was.
	Fetched *InflightFetch `json:"fetched,omitempty" yaml:"fetched,omitempty"`
	// Unproposed are the caller's live jobs whose checkout is on a branch no change
	// carries: work in flight that has no change yet.
	Unproposed []string `json:"unproposed,omitempty" yaml:"unproposed,omitempty"`
}

// InflightChange is one open change as a person reads it: the provider's record joined to
// the jobs working on its branch and to the queue's newest plan for it. The queue's own
// Change is the record it acts on; this one is what it is waiting on and whose turn it is.
type InflightChange struct {
	ID     string `json:"id"               yaml:"id"`
	Title  string `json:"title,omitempty"  yaml:"title,omitempty"`
	Author string `json:"author,omitempty" yaml:"author,omitempty"`
	Head   string `json:"head"             yaml:"head"`
	Branch string `json:"branch,omitempty" yaml:"branch,omitempty"`
	Base   string `json:"base,omitempty"   yaml:"base,omitempty"`
	Fork   bool   `json:"fork,omitempty"   yaml:"fork,omitempty"`
	// Jobs are the ids of the jobs whose checkout is on Branch, in store order.
	Jobs []string `json:"jobs,omitempty" yaml:"jobs,omitempty"`
	// Intent is the merge method the change is queued with, empty when it carries no
	// merge intent.
	Intent string `json:"intent,omitempty" yaml:"intent,omitempty"`
	// Mark is the queue's mark the provider shows on a change carrying no intent:
	// queued, kicked_back or needs_regeneration.
	Mark string `json:"mark,omitempty" yaml:"mark,omitempty"`
	// Decision, Code and Reason are the plan's verdict on this head, empty when it
	// settled none.
	Decision string `json:"decision,omitempty" yaml:"decision,omitempty"`
	Code     string `json:"code,omitempty"     yaml:"code,omitempty"`
	Reason   string `json:"reason,omitempty"   yaml:"reason,omitempty"`
	// Partition and Position place the change in the plan, both counted from 1; zero
	// when the plan admitted no change at this head. Below is the change it is stacked on.
	Partition int    `json:"partition,omitempty" yaml:"partition,omitempty"`
	Position  int    `json:"position,omitempty"  yaml:"position,omitempty"`
	Below     string `json:"below,omitempty"     yaml:"below,omitempty"`
	// Mine is whether the caller is on the change: a job of the caller's job tree works
	// on its branch, or its author is the caller's forge login.
	Mine bool `json:"mine,omitempty" yaml:"mine,omitempty"`
	// Attention is whose turn the change is. See [InflightAttention].
	Attention InflightAttention `json:"attention" yaml:"attention"`
	// Neighbours are the other in-flight changes this one touches, on the caller's
	// changes only, strongest evidence first.
	Neighbours []InflightNeighbour `json:"neighbours,omitempty" yaml:"neighbours,omitempty"`
}

// InflightAttention is whose turn an in-flight change is, derived from its facts and
// never written by hand.
type InflightAttention string

const (
	// AttentionAuthor: the queue kicked it back, so its author acts next.
	AttentionAuthor InflightAttention = "author"
	// AttentionReview: it waits for an approval at its head.
	AttentionReview InflightAttention = "review"
	// AttentionQueue: it carries merge intent and the queue acts next.
	AttentionQueue InflightAttention = "queue"
	// AttentionNone: it carries no merge intent and nothing waits on anyone.
	AttentionNone InflightAttention = "none"
)

// InflightNeighbour is another in-flight change a change touches, by the strongest
// evidence that holds: merge_conflict, then declaration, file, affected.
type InflightNeighbour struct {
	// ID is the other change, or a job id when that work has no change yet.
	ID           string   `json:"id"                     yaml:"id"`
	Evidence     string   `json:"evidence"               yaml:"evidence"`
	Paths        []string `json:"paths,omitempty"        yaml:"paths,omitempty"`
	Declarations []string `json:"declarations,omitempty" yaml:"declarations,omitempty"`
	Units        []string `json:"units,omitempty"        yaml:"units,omitempty"`
	// Base is the merge base the comparison ran on.
	Base string `json:"base,omitempty" yaml:"base,omitempty"`
}

// InflightFetch is the network read an in-flight list rests on.
type InflightFetch struct {
	// Provider is the queue provider that answered, as `magus queue ls --provider` named it.
	Provider string `json:"provider"         yaml:"provider"`
	Host     string `json:"host,omitempty"   yaml:"host,omitempty"`
	Base     string `json:"base"             yaml:"base"`
	// Tip is the base commit the plan was built on, empty when no plan was recorded.
	Tip string `json:"tip,omitempty" yaml:"tip,omitempty"`
	// At is unix seconds, and ElapsedMS how long the provider took to answer.
	At        int64 `json:"at"         yaml:"at"`
	ElapsedMS int64 `json:"elapsed_ms" yaml:"elapsed_ms"`
}

// JobReadOnly names a row this magus will not write and the features it lacks for it.
type JobReadOnly struct {
	Job   string   `json:"job"   yaml:"job"`
	Lacks []string `json:"lacks" yaml:"lacks"`
}

// JobBlock is why a live job owns none of its write paths: On, a job it depends on, is in
// State rather than pass. State is empty when no row declares On.
type JobBlock struct {
	Job   string   `json:"job"             yaml:"job"`
	On    string   `json:"on"              yaml:"on"`
	State JobState `json:"state,omitempty" yaml:"state,omitempty"`
}

// JobBlockedOn reports the first job row depends on that has not reached pass. A blocked
// job claims none of its write paths: the overlap report, the guard and a job's terms all
// ask this before treating a row as an owner.
//
// A dependency no row declares blocks too, as it refuses the dependent's pass in
// verification: a dropped row must not hand its write paths to a waiter by vanishing.
func JobBlockedOn(rows []Job, row Job) (JobBlock, bool) {
	for _, dep := range row.DependsOn {
		i := slices.IndexFunc(rows, func(r Job) bool { return r.ID == dep })
		if i < 0 {
			return JobBlock{Job: row.ID, On: dep}, true
		}
		if rows[i].State != StatePass {
			return JobBlock{Job: row.ID, On: dep, State: rows[i].State}, true
		}
	}
	return JobBlock{}, false
}

// String renders the reason as "blocked on <dep> which is <state>".
func (b JobBlock) String() string {
	state := string(b.State)
	if state == "" {
		state = "undeclared"
	}
	return fmt.Sprintf("blocked on %s which is %s", b.On, state)
}

// Flag fills Overdue, Orphans and Stale as of now, in unix seconds. Overdue is past its
// deadline; an orphan is live while an ancestor has ended (pass, fail or no_return), so
// nobody is left to wait on it; stale was not updated within staleAfter, read by
// [JobStaleAt], and a zero staleAfter flags nothing.
func (l JobList) Flag(now int64, staleAfter time.Duration) JobList {
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
		if JobStaleAt(l.Jobs, row, now, staleAfter) {
			l.Stale = append(l.Stale, row.ID)
		}
	}
	return l
}

// EndedAncestor returns the nearest ancestor of id in a terminal state. A row with no
// state has not said it ended, so it does not count.
func EndedAncestor(rows []Job, id string) (Job, bool) {
	for _, a := range JobAncestors(rows, id) {
		if a.State.Terminal() {
			return a, true
		}
	}
	return Job{}, false
}

// JobAncestors walks id's parent chain nearest first. It stops at a parent the rows do not
// carry and at a cycle, since either means the plan is already damaged.
//
// A scan per step rather than an index of rows: chains are a few rows deep, and every
// list flags each live row through this, so a map of the whole store per call made one
// list quadratic in allocations. The chain walked so far is the cycle check.
func JobAncestors(rows []Job, id string) []Job {
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

// JobDescendants are the rows below id by parent chain, in store order.
func JobDescendants(rows []Job, id string) []Job {
	var out []Job
	for _, r := range rows {
		if r.ID != id && slices.ContainsFunc(JobAncestors(rows, r.ID), func(a Job) bool { return a.ID == id }) {
			out = append(out, r)
		}
	}
	return out
}

// NewJobList wraps the rows and derives the overlaps. Derived on READ and
// never stored: an overlap is a relation between two rows, so storing it on either
// one would mean a row that stopped being true when its neighbor changed.
//
// The registration facts take the opposite route and are NOT derived here. ReportedBase,
// BaseVerdict and Registered describe one row against the checkpoint that row was handed,
// so they belong on the row, are computed once when the worker registers, and reach every
// reader of this list (magus\job.list, JobService's ListJobs) by riding
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
	var blocked []JobBlock
	var readOnly []JobReadOnly
	known := JobSchema.Features()
	for _, row := range jobs {
		if b, ok := JobBlockedOn(jobs, row); ok && row.State.Live() {
			blocked = append(blocked, b)
		}
		if lacks := row.Unmet(known); lacks != nil {
			readOnly = append(readOnly, JobReadOnly{Job: row.ID, Lacks: lacks})
		}
	}
	return JobList{Jobs: jobs, Overlaps: jobOverlaps(jobs), Blocked: blocked, ReadOnly: readOnly}
}

// jobOverlaps reports every pair of jobs whose declared write paths
// intersect, in ledger order.
//
// Only a [JobState.Editing] job is in a pair. A released, exited or finished job is not
// competing for a path (that is the whole shape of the skill's early-release rule,
// where a worker shrinks its write paths so a waiter can start), and reporting one
// would make the report noisiest exactly when the plan is winding down. A job blocked on
// a dependency is not in any pair either: it claims nothing until that dependency passes.
func jobOverlaps(jobs []Job) []JobOverlap {
	claims := func(j Job) bool {
		_, blocked := JobBlockedOn(jobs, j)
		return j.State.Editing() && len(j.HardPaths()) > 0 && !blocked
	}
	var out []JobOverlap
	for i, a := range jobs {
		if !claims(a) {
			continue
		}
		for _, b := range jobs[i+1:] {
			if !claims(b) {
				continue
			}
			if pa, pb := intersectingPaths(a.HardPaths(), b.HardPaths()); len(pa) > 0 {
				out = append(out, JobOverlap{JobA: a.ID, JobB: b.ID, PathsA: pa, PathsB: pb, Claims: claimsVerdict(pa, pb)})
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
	// An entry that names nothing claims nothing. It cleans to ".", which the whole-tree
	// rule below would then read as a claim on everything, pairing a row that holds one
	// stray blank with every other lease in the plan.
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
	switch aMeta, bMeta := IsGlobMeta(a), IsGlobMeta(b); {
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
// Exported because the focus rule asks the same question of the same declarations:
// which project a lease's write_paths land in cannot be answered by a wildcard, and
// two packages deriving that prefix by their own rules would disagree about a
// declaration on the day the rules drifted. A claimed declaration (see SplitClaim) is
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
	innerGlob := IsGlobMeta(innerFile)
	switch {
	case outerDecl != "":
		return innerDecl == outerDecl && innerFile == outerFile && !innerGlob
	case innerFile == outerFile, outerFile == ".":
		return true
	case !IsGlobMeta(outerFile):
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
	if IsGlobMeta(base) {
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
	if inner == "**" || !strings.HasPrefix(outer, "*") || IsGlobMeta(outer[1:]) {
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

// Integrating reports whether the row is an integration job: one that names branches to merge.
func (u Job) Integrating() bool { return len(u.Integrates) > 0 }

// ValidIntegratedBranch reports whether b can name a branch an integration job merges: no
// leading dash, which git would read as an option, and nothing git refuses in a ref name.
func ValidIntegratedBranch(b string) bool {
	return b != "" && !strings.HasPrefix(b, "-") && !strings.HasSuffix(b, ".lock") && !strings.HasSuffix(b, "/") &&
		!strings.Contains(b, "..") && !strings.Contains(b, "@{") &&
		!strings.ContainsFunc(b, func(r rune) bool { return r <= ' ' || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r) })
}

// HardPaths is WritePaths without the soft entries: the paths the row owns against other
// rows. A reader asking who owns a path (an overlap, a boundary, a denial naming an owner)
// reads this; a reader asking what this row may write reads WritePaths.
func (u Job) HardPaths() []string {
	if len(u.SoftPaths) == 0 {
		return u.WritePaths
	}
	return slices.DeleteFunc(slices.Clone(u.WritePaths), func(p string) bool { return slices.Contains(u.SoftPaths, p) })
}

// Clone returns a deep copy: the slice fields are the only shared state, so copying
// them is what makes a value handed out of a store safe to keep. slices.Clone
// preserves nil, so a row that stored null does not come back as [].
func (u Job) Clone() Job {
	c := u
	c.Schema = u.clone()
	c.WritePaths = slices.Clone(u.WritePaths)
	c.DenyPaths = slices.Clone(u.DenyPaths)
	c.SoftPaths = slices.Clone(u.SoftPaths)
	c.ReadPaths = slices.Clone(u.ReadPaths)
	c.DependsOn = slices.Clone(u.DependsOn)
	c.Integrates = slices.Clone(u.Integrates)
	c.Breaks = slices.Clone(u.Breaks)
	c.Goals = cloneGoals(u.Goals)
	c.Releases = slices.Clone(u.Releases)
	c.Claims = slices.Clone(u.Claims)
	c.Unattributed = slices.Clone(u.Unattributed)
	c.Entries = slices.Clone(u.Entries)
	c.Waivers = slices.Clone(u.Waivers)
	if u.ModelTier != nil {
		tier := *u.ModelTier
		c.ModelTier = &tier
	}
	if u.Result != nil {
		result := *u.Result
		result.Schema = u.Result.clone()
		result.ChangedPaths = slices.Clone(u.Result.ChangedPaths)
		result.Descendants = slices.Clone(u.Result.Descendants)
		result.UnresolvedRisks = slices.Clone(u.Result.UnresolvedRisks)
		result.GateEvidence = slices.Clone(u.Result.GateEvidence)
		c.Result = &result
	}
	if u.Attempt != nil {
		attempt := *u.Attempt
		attempt.ExtraArgs = slices.Clone(u.Attempt.ExtraArgs)
		c.Attempt = &attempt
	}
	c.GateAttempts = slices.Clone(u.GateAttempts)
	for i := range c.GateAttempts {
		c.GateAttempts[i].Attempt.ExtraArgs = slices.Clone(c.GateAttempts[i].Attempt.ExtraArgs)
	}
	if u.LastRun != nil {
		run := *u.LastRun
		c.LastRun = &run
	}
	c.Integration = cloneIntegration(u.Integration)
	c.WaiterEvidence = cloneIntegration(u.WaiterEvidence)
	if u.Worktree != nil {
		wt := *u.Worktree
		c.Worktree = &wt
	}
	c.Prior = slices.Clone(u.Prior)
	for i, p := range c.Prior {
		c.Prior[i].Unmet = cloneGateStatuses(p.Unmet)
		c.Prior[i].Violations = slices.Clone(p.Violations)
		c.Prior[i].UnresolvedRisks = slices.Clone(p.UnresolvedRisks)
		c.Prior[i].FailingRefs = slices.Clone(p.FailingRefs)
		c.Prior[i].Denials = slices.Clone(p.Denials)
	}
	if u.Rejection != nil {
		r := *u.Rejection
		r.Unmet = cloneGateStatuses(u.Rejection.Unmet)
		r.Violations = slices.Clone(u.Rejection.Violations)
		c.Rejection = &r
	}
	return c
}

// cloneGateStatuses is a deep copy of gates, nil for nil.
func cloneGateStatuses(gates []GateStatus) []GateStatus {
	out := slices.Clone(gates)
	for i := range out {
		out[i].Violations = slices.Clone(out[i].Violations)
	}
	return out
}

// cloneIntegration is a deep copy of g, nil for nil.
func cloneIntegration(g *JobIntegration) *JobIntegration {
	if g == nil {
		return nil
	}
	c := *g
	c.Gates = cloneGateStatuses(g.Gates)
	return &c
}
