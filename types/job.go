package types

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
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

// PrimaryCompletionGateID names the existing singular check when it is projected
// into the completion-gate model. It is a stable identity, not a user assertion.
const PrimaryCompletionGateID = "check"

// CompletionGate is one machine-verifiable condition a job must satisfy before it
// can pass. Criteria remains the human-readable objective; gates bind that objective to
// recorded Magus output rather than a holder's boolean attestation. The output
// must have been captured after the job was declared; target execution limits
// remain the target's run policy rather than a second gate timeout.
type CompletionGate struct {
	ID          string   `json:"id"                     yaml:"id"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"  yaml:"depends_on,omitempty"`
	// Kind names WHAT this gate examines and Expect names what must be true of it. Two
	// fields rather than one, because the alternative is a kind per pair: `paths` beside
	// `exists` beside `absent` beside `no-symbol`, which is a vocabulary that grows by
	// multiplication and reads inconsistently the moment it has four members.
	//
	// Both are RESOLVED before a row is stored: Resolve fills a gate that named neither,
	// so a reader never applies a default and the published enums carry no empty member.
	// A default applied on read is a default every reader has to know about, and the
	// readers here are the verifier, the guard, the observer and two schemas.
	Kind   GateKind   `json:"kind"   yaml:"kind"`
	Expect GateExpect `json:"expect" yaml:"expect"`
	// Check is the run a GateKindCheck gate examines. Zero on every other kind.
	Check LeaseCheck `json:"check,omitempty" yaml:"check,omitempty"`
	// Paths are the globs a GateKindPaths gate examines. Zero on every other kind.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	// Symbols are the names a GateKindSymbol gate examines, as the knowledge graph
	// resolves them. Zero on every other kind.
	Symbols []string `json:"symbols,omitempty" yaml:"symbols,omitempty"`
}

// Resolve fills a gate's kind and expectation from what it declared, so every stored gate
// names both. A gate that named neither is a check, which is what the single Check field
// meant before gates existed; a gate that named a kind and no expectation takes that
// kind's natural one.
//
// Called at the WRITE boundary (Declaration.Apply, ParseMerge, the stored-row fold), never
// on read. That is the whole reason the enums have no empty member.
func (g CompletionGate) Resolve() CompletionGate {
	if g.Kind == "" {
		g.Kind = GateKindCheck
	}
	if g.Expect == "" {
		g.Expect = g.Kind.DefaultExpect()
	}
	return g
}

// GateKind names WHAT a completion gate examines, because not every condition a job can be
// held to is a target run.
//
// Each kind names an evidence source magus already holds: the output store, the VCS diff,
// the knowledge graph. A condition magus can observe no evidence for is not declarable
// here on purpose, and there is deliberately no escape hatch for one. A gate accepting an
// unrecorded exit status would be the easiest kind to satisfy falsely, which is the
// attestation the whole mechanism replaces; declare a target and use GateKindCheck.
type GateKind string

const (
	// GateKindCheck examines a recorded run of the gate's check. It is a return code, and
	// what raises it above one is that magus RECORDED it: the output store holds the
	// target, the project, the timestamp and the failure bit, so the run can be reopened
	// and attributed to this job rather than taken on the holder's word.
	GateKindCheck GateKind = "check"
	// GateKindPaths examines files, by glob. It is what "this job must actually produce
	// the migration" looks like when no target can say so.
	GateKindPaths GateKind = "paths"
	// GateKindSymbol examines named symbols in the knowledge graph, which is the
	// granularity below a file: a function added, renamed or deleted is a fact the graph
	// holds even when the file it lives in changed for ten other reasons.
	GateKindSymbol GateKind = "symbol"
)

// GateKinds is the closed set, for the same reason JobStates is one: the validator, the
// published schema and the error each of them raises all quote it, and a vocabulary that
// drifts rejects a client for a value the schema told it to send.
func GateKinds() []GateKind {
	return []GateKind{GateKindCheck, GateKindPaths, GateKindSymbol}
}

// GateExpect names what must be TRUE of what a gate examines. One vocabulary across every
// kind, so `absent` means the same thing of a file, a symbol and a reference.
type GateExpect string

const (
	// ExpectPassed is a recorded run that finished without failing. GateKindCheck only.
	ExpectPassed GateExpect = "passed"
	// ExpectChanged is "the diff since the job's checkpoint touches this".
	ExpectChanged GateExpect = "changed"
	// ExpectPresent is "this is here now", whether or not this job is what put it here.
	ExpectPresent GateExpect = "present"
	// ExpectAbsent is "this is not here now", which is how a deletion or a removal is
	// declared as a condition rather than reported as one.
	ExpectAbsent GateExpect = "absent"
	// ExpectUnreferenced is "nothing names this any more", and it belongs to symbols. It
	// is the one that answers the remainder a partitioned rename leaks: split the work per
	// project and the callers in no project belong to no job, so every job passes and the
	// rename is unfinished.
	//
	// An EXPECTATION rather than a kind of its own, because the subject it examines is
	// still the symbol. A `refs` kind read the same field as `symbol` and forced every
	// reader to treat the two as one, which is a vocabulary that says it has four members
	// and behaves as though it has three.
	ExpectUnreferenced GateExpect = "unreferenced"
)

// GateExpects is the closed set. See GateKinds.
func GateExpects() []GateExpect {
	return []GateExpect{ExpectPassed, ExpectChanged, ExpectPresent, ExpectAbsent, ExpectUnreferenced}
}

// DefaultExpect is what a kind means when a gate names no condition, so the common gate of
// each kind declares only its subject. A check is asked whether it passed; files and
// symbols are asked whether this job changed them, which is the question a job is for.
func (k GateKind) DefaultExpect() GateExpect {
	if k == GateKindCheck {
		return ExpectPassed
	}
	return ExpectChanged
}

// Subject is what the gate examines, rendered for a message that has to name it.
func (g CompletionGate) Subject() []string {
	switch g.Kind {
	case GateKindCheck:
		if g.Check.Target == "" {
			return nil
		}
		return []string{g.Check.String()}
	case GateKindPaths:
		return g.Paths
	default:
		return g.Symbols
	}
}

// gateAccepts is the kind-to-condition matrix, and the one place it is written down.
// Anything outside it is refused at declaration rather than graded into a verdict nobody
// can act on: `check` + `absent` has no meaning, and a gate nobody can satisfy reads as a
// job nobody can finish.
var gateAccepts = map[GateKind][]GateExpect{
	GateKindCheck:  {ExpectPassed},
	GateKindPaths:  {ExpectChanged, ExpectPresent, ExpectAbsent},
	GateKindSymbol: {ExpectChanged, ExpectPresent, ExpectAbsent, ExpectUnreferenced},
}

// EffectiveCompletionGates returns the declared gates plus the historical primary
// Check as a gate. Keeping the projection at the model boundary means guards that
// still read Check retain their capability semantics while every verifier has one
// complete gate collection to grade.
func (u Job) EffectiveCompletionGates() []CompletionGate {
	gates := cloneCompletionGates(u.CompletionGates)
	if u.Check != nil {
		primary := CompletionGate{ID: PrimaryCompletionGateID, Check: *u.Check}.Resolve()
		gates = append([]CompletionGate{primary}, gates...)
	}
	return gates
}

// String renders the check as a DECLARATION, the shape a person types and a row stores.
// It is not the command to run: a check naming no charm means the charmless run, and
// spelling that needs the --no-default-charms flag, which this package cannot render
// because types imports no CLI surface. internal/job renders the runnable form through
// the hint command builder; see gateCommand.
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

// NamesCharm reports whether the check pins a charm on its target, which is what decides
// whether the runnable form needs --no-default-charms. Asked here rather than re-parsed
// at each renderer: the charm is part of the target's identity (see internal/job.bindsTo)
// and only this type knows how a target is spelled.
func (c LeaseCheck) NamesCharm() bool {
	_, target, _ := strings.Cut(c.Target, "::")
	if target == "" {
		target = c.Target
	}
	return strings.Contains(target, ":")
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
// rejects sends it back to the same write paths; dropping the job out of live here would
// leave every write after `job exit` graded by nothing at all.
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
const JobSchemaVersion = 9

// JobWriteProof is what the fork could prove about a job's write paths against the other
// live jobs bound to the SAME CHECKOUT at the moment it was declared.
//
// Recorded rather than enforced, with one exception (write paths covering a workspace-load
// file, which is refused outright). Two workers overlapping is a call only the
// orchestrator can make: it may have sequenced them, or split one file deliberately. What
// nobody could do before is READ that call back afterwards, so a plan full of overlapping
// write paths and a plan whose write paths were checked looked identical.
type JobWriteProof string

const (
	// WriteProofAlone is a fork made in a checkout no other live job with write paths was
	// bound to. There was nothing to be disjoint FROM, which is not the same claim as
	// disjoint and is why it is its own value.
	WriteProofAlone JobWriteProof = "alone"
	// WriteProofDisjoint is a job whose write paths intersect no other bound job's here.
	WriteProofDisjoint JobWriteProof = "disjoint"
	// WriteProofOverlapping is one whose write paths intersect another's. The fork stands;
	// the row says so.
	WriteProofOverlapping JobWriteProof = "overlapping"
)

// JobWriteProofs is the closed set, for the reason JobStates is one: the published schema
// and every reader that renders a row quote it.
func JobWriteProofs() []JobWriteProof {
	return []JobWriteProof{WriteProofAlone, WriteProofDisjoint, WriteProofOverlapping}
}

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

// JobResult is the result a holder files for a job.
type JobResult struct {
	SchemaVersion   int                 `json:"schema_version" yaml:"schema_version"`
	Job             string              `json:"job,omitempty" yaml:"job,omitempty"`
	ChangedPaths    []string            `json:"changed_paths" yaml:"changed_paths"`
	Validation      JobResultValidation `json:"validation,omitempty" yaml:"validation,omitempty"`
	GateEvidence    []GateEvidence      `json:"gate_evidence,omitempty" yaml:"gate_evidence,omitempty"`
	Descendants     []string            `json:"descendants,omitempty" yaml:"descendants,omitempty"`
	UnresolvedRisks []string            `json:"unresolved_risks" yaml:"unresolved_risks"`
}

// GateEvidence links a completion gate to captured Magus output.
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
	Job        string       `json:"job" yaml:"job"`
	Verified   bool         `json:"verified" yaml:"verified"`
	Violations []string     `json:"violations,omitempty" yaml:"violations,omitempty"`
	Risks      []string     `json:"unresolved_risks,omitempty" yaml:"unresolved_risks,omitempty"`
	Command    string       `json:"command,omitempty" yaml:"command,omitempty"`
	Gates      []GateStatus `json:"gates,omitempty" yaml:"gates,omitempty"`
	// StaleIndexes are the projects whose symbol index was older than their sources when
	// symbol gates were graded, so a symbol verdict may be drawn from missing facts.
	StaleIndexes []string `json:"stale_indexes,omitempty" yaml:"stale_indexes,omitempty"`
}

// GateStatus reports verification of one completion gate.
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
}

// JobGateAttempt is the stored output record for one completion gate.
type JobGateAttempt struct {
	GateID  string     `json:"gate_id" yaml:"gate_id"`
	Attempt JobAttempt `json:"attempt" yaml:"attempt"`
}

// String renders the recorded command.
func (a JobAttempt) String() string {
	target := a.Target
	if a.Spell != "" {
		target = a.Spell + "::" + target
	}
	return LeaseCheck{Target: target, Project: a.Project}.String()
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
	// Criteria is what this lease is for and what done means, as one block of prose. Not
	// split into two fields: the skill requires criteria to be observable, and a separate
	// empty field would read as "none required" rather than as "the author did not write
	// any".
	//
	// PROSE, graded by a reader. The machine-checkable half is CompletionGates, and the
	// two are deliberately separate: a condition magus can verify belongs in a gate, where
	// it is a contract, rather than in a sentence here that nothing reads.
	Criteria string `json:"criteria,omitempty" yaml:"criteria,omitempty"`
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
	// ReadPaths is what this lease may READ: the paths whose projects it may read,
	// widened to those projects' own dependencies when the guard resolves it. Empty
	// means WritePaths stands in, because a worker leased to edit a project is a
	// worker that was pointed at that project.
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
	// CompletionGates are additional evidence-backed acceptance conditions. Check
	// remains the primary gate projection for rows declared before this collection
	// existed; no caller can mark either kind passed without a recorded output ref.
	CompletionGates []CompletionGate `json:"completion_gates,omitempty" yaml:"completion_gates,omitempty"`
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
	// WriteProof is what the fork could prove about this job's write paths against the
	// other live jobs bound to the checkout it was declared in. Store-computed and
	// output-only like Releases: it is a fact about the plan at one instant, and a caller
	// that could assert it could assert the proof it stands for. See [JobWriteProof].
	//
	// Written as `lane_proof` through schema 8. A schema-8 row decodes with this UNSET
	// rather than through a legacy fold: unlike the Declaration fields, which a person
	// authors and would lose work by, this one is store-computed, informational, and
	// already renders as "-" when empty. Carrying a second key for it would add a decode
	// path with no reader to justify it. JobSchemaVersion is 9 for exactly this.
	WriteProof JobWriteProof `json:"write_proof,omitempty" yaml:"write_proof,omitempty"`
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
	// Deadline is unix seconds past which the guard denies this lease's writes, zero for no
	// bound. The store stamps it from a fork's timeout and no door accepts it, for the reason
	// Created is not accepted. Nothing transitions a row that passes it.
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
	// GateAttempts are the portable output-store snapshots for every explicit
	// completion gate. Attempt remains the snapshot for the primary Check.
	GateAttempts []JobGateAttempt `json:"gate_attempts,omitempty" yaml:"gate_attempts,omitempty"`
	// LastRun is the job's most recent run, nil until one is submitted. It is filled in
	// two steps because no single writer sees the whole of it: the invocation id at
	// submit, and what the run cost and whether it worked when it ends.
	//
	// The SIZE of what the job maintains is deliberately not stored beside it: a trail, a
	// run log and a cache all grow without the row being written, so a stored figure goes
	// stale in silence. It is measured when the job is listed.
	LastRun *JobRun `json:"last_run,omitempty" yaml:"last_run,omitempty"`
}

// Declaration is the typed INPUT for one lease row: the fields a caller DECLARES, and nothing
// the store computes. A caller cannot say when its row was created or what it released, and
// the way to make that true is for the input type not to carry those fields rather than for
// the store to strip them afterwards.
//
// It is a DECLARATION and not a merge: every field it carries is written, so an omitted one
// is cleared rather than kept. The magus_job tool's fork deliberately does the opposite,
// since an agent advancing one field of a live row must not erase the rest (see
// job.ParseMerge).
//
// JSON only: this is decoded from stdin and never emitted, so it carries no yaml tags.
//
// Registered in cmd/magus-utils/boundary_types.go with no RuntimeObject: a magusfile can
// construct one, but nothing hands one back out to Buzz; job.DecodeDeclaration is the only
// decoder, and it reads JSON, not a Buzz value.
type Declaration struct {
	// SchemaVersion is the row shape this record is written in, and it is required: a
	// version this magus does not know is rejected by name. See JobSchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// ID is the lease's identity within the plan, the key a second register replaces on,
	// and the only required field besides the version.
	ID string `json:"id" schema:"leaseid"`
	// Parent is the lease this one was spawned under, empty for a lease the root declared.
	Parent string `json:"parent,omitempty"`
	// Criteria is what this lease is for and what done means, as one block of prose. The
	// machine-checkable half is CompletionGates.
	Criteria string `json:"criteria,omitempty"`
	// Checkpoint is the working state this lease starts from, as `magus vcs checkpoint -o
	// name` prints it.
	Checkpoint string `json:"checkpoint,omitempty"`
	// WritePaths is what this lease may write, empty on a read-only row by design.
	WritePaths []string `json:"write_paths,omitempty"`
	// DenyPaths are the paths inside those this lease may not write.
	DenyPaths []string `json:"deny_paths,omitempty"`
	// ReadPaths is what it may READ, widened to those projects' dependencies by the
	// guard. Empty means write_paths stands in.
	ReadPaths []string `json:"read_paths,omitempty"`
	// DependsOn names the lease ids that must land before this one.
	DependsOn []string `json:"depends_on,omitempty"`
	// Model is the model or effort tier the work was matched to, a free string because
	// hosts name their models differently.
	Model string `json:"model,omitempty"`
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
	// CompletionGates declare additional evidence-backed acceptance conditions.
	CompletionGates []CompletionGate `json:"completion_gates,omitempty"`
	// Timeout bounds the lease, as a Go duration. The store stamps Job.Deadline from it when
	// it writes the row, so a declaration carries a length and never an instant. Empty is no
	// bound; there is no default here (a workspace may set jobs.default_timeout).
	Timeout string `json:"timeout,omitempty"`
}

// FoldLegacyNames moves a field declared under its old name onto the one that carries it,
// refusing a row that names the same field twice. Exported for job.DecodeDeclaration, the
// one caller outside this package: it runs between the JSON decode and Validate.
//
// compat: see the legacy fields on [Declaration].
func (r *Declaration) FoldLegacyNames() error {
	var err error
	fold := func(name string, into *[]string, from []string) {
		switch {
		case len(from) == 0:
		case len(*into) > 0:
			err = errors.Join(err, fmt.Errorf("job: a row declares %s or its renamed spelling, not both", name))
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
			err = errors.Join(err, fmt.Errorf("job: a row declares %s or its renamed spelling, not both", name))
		default:
			*into = from
		}
	}
	foldString("tier", &r.Model, r.LegacyModel)
	r.LegacyWritePaths, r.LegacyDenyPaths, r.LegacyReadPaths = nil, nil, nil
	r.LegacyModel = ""
	return err
}

// Validate reports what is wrong with a declared row, or nil.
func (r Declaration) Validate() error {
	if !ValidJobID(strings.TrimSpace(r.ID)) {
		return fmt.Errorf("job: %q is not a lease id (letters, digits and -_./: only, at most %d characters)", r.ID, MaxJobIDLen)
	}
	if r.State != "" && !ValidJobState(r.State) {
		return fmt.Errorf("job: state must be one of %s", JobStateVocabulary())
	}
	if _, err := ParseJobTimeout(r.Timeout); err != nil {
		return err
	}
	check, declared, err := r.check()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	if declared {
		_ = check
		seen[PrimaryCompletionGateID] = true
	}
	for i, gate := range r.CompletionGates {
		if err := gate.Validate(); err != nil {
			return fmt.Errorf("job: completion_gates[%d]: %w", i, err)
		}
		if seen[gate.ID] {
			return fmt.Errorf("job: completion_gates carries duplicate id %q", gate.ID)
		}
		seen[gate.ID] = true
	}
	for _, gate := range r.CompletionGates {
		for _, dep := range gate.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("job: completion gate %q depends_on unknown gate %q", gate.ID, dep)
			}
			if dep == gate.ID {
				return fmt.Errorf("job: completion gate %q cannot depend on itself", gate.ID)
			}
		}
	}
	visiting := make(map[string]bool, len(r.CompletionGates))
	visited := make(map[string]bool, len(r.CompletionGates))
	byID := make(map[string]CompletionGate, len(r.CompletionGates))
	for _, gate := range r.CompletionGates {
		byID[gate.ID] = gate
	}
	var visit func(string) error
	visit = func(id string) error {
		if id == PrimaryCompletionGateID {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("job: completion gate dependencies contain a cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range byID[id].DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, gate := range r.CompletionGates {
		if err := visit(gate.ID); err != nil {
			return err
		}
	}
	return nil
}

// Validate reports whether a gate can be matched to an output record without
// interpreting a shell command.
func (g CompletionGate) Validate() error {
	if !ValidJobID(strings.TrimSpace(g.ID)) {
		return fmt.Errorf("id %q is not a gate id", g.ID)
	}
	// Validates what the row WILL BE, not what arrived: a declaration off the wire has not
	// been through Resolve yet, and refusing it for naming no kind would refuse the
	// shorthand the defaults exist to allow. Resolving here is not read-time defaulting;
	// the stored row is resolved by cloneCompletionGates on the way in.
	g = g.Resolve()
	accepted, known := gateAccepts[g.Kind]
	if !known {
		return fmt.Errorf("gate %q names kind %q, and the kinds are %s", g.ID, g.Kind, quoteJoin(GateKinds(), ", "))
	}
	if !slices.Contains(accepted, g.Expect) {
		return fmt.Errorf("gate %q is a %s gate expecting %q, and a %s gate expects %s",
			g.ID, g.Kind, g.Expect, g.Kind, quoteJoin(accepted, " or "))
	}
	// Exactly one subject, named by the kind. A gate carrying two is one whose author
	// changed their mind, and grading the one the kind happens to read would silently
	// ignore the other.
	if g.Kind != GateKindCheck && g.Check.Target != "" {
		return fmt.Errorf("gate %q is a %s gate and also carries a check; a gate examines one subject", g.ID, g.Kind)
	}
	if g.Kind != GateKindPaths && len(trimmedNonEmpty(g.Paths)) > 0 {
		return fmt.Errorf("gate %q is a %s gate and also carries paths; a gate examines one subject", g.ID, g.Kind)
	}
	if g.Kind != GateKindSymbol && len(trimmedNonEmpty(g.Symbols)) > 0 {
		return fmt.Errorf("gate %q is a %s gate and also carries symbols; a gate examines one subject", g.ID, g.Kind)
	}
	if len(trimmedNonEmpty(g.Subject())) == 0 {
		return fmt.Errorf("gate %q is a %s gate and names nothing to examine, so nothing could ever satisfy it", g.ID, g.Kind)
	}
	if g.Kind == GateKindCheck {
		if _, err := ParseLeaseCheck(g.Check.Target + " " + g.Check.Project); err != nil {
			return err
		}
	}
	return nil
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
		return 0, fmt.Errorf("job: timeout %q is not a positive duration such as 30m or 2h", s)
	}
	return d, nil
}

// Overdue reports whether a live row has passed its deadline at now, in unix seconds. A
// terminal row has no writes left to bound, so it is never overdue.
func (u Job) Overdue(now int64) bool {
	return u.Deadline > 0 && u.State.Live() && now >= u.Deadline
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
func (r Declaration) check() (LeaseCheck, bool, error) {
	line := strings.TrimSpace(r.Validation)
	switch {
	case r.Check != nil && line != "":
		return LeaseCheck{}, false, errors.New("job: a row carries `check` or a rendered `validation` line, not both")
	case r.Check != nil:
		parsed, err := ParseLeaseCheck(r.Check.Target + " " + r.Check.Project)
		if err != nil {
			return LeaseCheck{}, false, fmt.Errorf("job: %w", err)
		}
		parsed.Args = r.Check.Args
		return parsed, true, nil
	case line != "":
		parsed, err := ParseLeaseRunLine(line)
		if err != nil {
			return LeaseCheck{}, false, fmt.Errorf("job: %w", err)
		}
		return parsed, true, nil
	}
	return LeaseCheck{}, false, nil
}

// JobStateVocabulary is the closed set of job states, as an error quotes it. Shared by
// Declaration.Validate and job.ParseMerge, the two places a caller-supplied state is
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
	u.ReadPaths = trimmedNonEmpty(r.ReadPaths)
	u.DependsOn = trimmedNonEmpty(r.DependsOn)
	u.Model = strings.TrimSpace(r.Model)
	// Validate refused an unparsable check before the row reached a store, so the error
	// here cannot fire; the rendered line is written from the record so the two agree.
	check, declared, _ := r.check()
	u.Check, u.Validation = nil, ""
	if declared {
		u.Check, u.Validation = &check, check.String()
	}
	u.CompletionGates = cloneCompletionGates(r.CompletionGates)
	u.State = r.State
	u.ReadOnly = r.ReadOnly
}

func cloneCompletionGates(in []CompletionGate) []CompletionGate {
	if in == nil {
		return nil
	}
	out := make([]CompletionGate, len(in))
	for i, gate := range in {
		// RESOLVED on the way through, which is what makes this the write boundary the
		// enums' no-empty-member rule depends on: every path that stores gates (Apply,
		// ParseMerge, the stored-row fold) clones them through here.
		out[i] = gate.Resolve()
		out[i].Check.Args = slices.Clone(gate.Check.Args)
		out[i].Paths = slices.Clone(gate.Paths)
		out[i].Symbols = slices.Clone(gate.Symbols)
		out[i].DependsOn = slices.Clone(gate.DependsOn)
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
	// Overdue, Orphans and Stale are live job ids a reader should look at, derived at one
	// instant by [JobList.Flag]. Reports, never transitions: ending a row stays the
	// orchestrator's call.
	Overdue []string `json:"overdue,omitempty" yaml:"overdue,omitempty"`
	Orphans []string `json:"orphans,omitempty" yaml:"orphans,omitempty"`
	Stale   []string `json:"stale,omitempty"   yaml:"stale,omitempty"`
	// Blocked are the live jobs that claim no paths yet because a dependency has not
	// passed, each naming the first such dependency. Derived with Overlaps, from the rows.
	Blocked []JobBlock `json:"blocked,omitempty" yaml:"blocked,omitempty"`
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
// deadline; an orphan is live while its root ancestor has ended, so nobody is left to wait
// on it; stale was not updated within staleAfter, and a zero staleAfter flags nothing.
func (l JobList) Flag(now int64, staleAfter time.Duration) JobList {
	l.Overdue, l.Orphans, l.Stale = nil, nil, nil
	for _, row := range l.Jobs {
		if !row.State.Live() {
			continue
		}
		if row.Overdue(now) {
			l.Overdue = append(l.Overdue, row.ID)
		}
		if ancestors := JobAncestors(l.Jobs, row.ID); len(ancestors) > 0 && !ancestors[len(ancestors)-1].State.Live() {
			l.Orphans = append(l.Orphans, row.ID)
		}
		if staleAfter > 0 && now-row.Updated >= int64(staleAfter/time.Second) {
			l.Stale = append(l.Stale, row.ID)
		}
	}
	return l
}

// JobAncestors walks id's parent chain nearest first. It stops at a parent the rows do not
// carry and at a cycle, since either means the plan is already damaged.
func JobAncestors(rows []Job, id string) []Job {
	byID := make(map[string]Job, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	var out []Job
	seen := map[string]bool{id: true}
	cur, ok := byID[id]
	for ok && cur.Parent != "" && !seen[cur.Parent] {
		seen[cur.Parent] = true
		if cur, ok = byID[cur.Parent]; ok {
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
// reader of this list (magus_job's list op, JobService's ListJobs) by riding
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
	for _, row := range jobs {
		if b, ok := JobBlockedOn(jobs, row); ok && row.State.Live() {
			blocked = append(blocked, b)
		}
	}
	return JobList{Jobs: jobs, Overlaps: jobOverlaps(jobs), Blocked: blocked}
}

// jobOverlaps reports every pair of jobs whose declared write paths
// intersect, in ledger order.
//
// A job in a terminal state is not in any pair. A released or finished job is not
// competing for a path (that is the whole shape of the skill's early-release rule,
// where a worker shrinks its write paths so a waiter can start), and reporting one
// would make the surface noisiest exactly when the plan is winding down. A job blocked on
// a dependency is not in any pair either: it claims nothing until that dependency passes.
func jobOverlaps(jobs []Job) []JobOverlap {
	claims := func(j Job) bool {
		_, blocked := JobBlockedOn(jobs, j)
		return !j.State.Terminal() && len(j.WritePaths) > 0 && !blocked
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
	c.CompletionGates = cloneCompletionGates(u.CompletionGates)
	c.Releases = slices.Clone(u.Releases)
	c.Unattributed = slices.Clone(u.Unattributed)
	if u.Result != nil {
		result := *u.Result
		result.ChangedPaths = slices.Clone(u.Result.ChangedPaths)
		result.Descendants = slices.Clone(u.Result.Descendants)
		result.UnresolvedRisks = slices.Clone(u.Result.UnresolvedRisks)
		result.GateEvidence = slices.Clone(u.Result.GateEvidence)
		c.Result = &result
	}
	if u.Attempt != nil {
		attempt := *u.Attempt
		c.Attempt = &attempt
	}
	c.GateAttempts = slices.Clone(u.GateAttempts)
	if u.LastRun != nil {
		run := *u.LastRun
		c.LastRun = &run
	}
	return c
}
