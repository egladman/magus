package job

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/codec"
)

// PrimaryGoalID names the job's singular check when it is projected into its goals. It
// is a stable identity, not a user assertion.
const PrimaryGoalID = "check"

// Goal is one machine-verifiable condition a job must satisfy before it can pass.
// Criteria remains the human-readable objective; goals bind that objective to recorded
// output rather than a holder's boolean attestation. The output must have been
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
	// Check is the run a GoalKindCheck goal examines, as opaque JSON the store reads
	// through [WithCheckSpec]. Nil on every other kind.
	Check codec.RawMessage `json:"check,omitzero" yaml:"check,omitempty"`
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
// Called at the WRITE boundary (Declaration.Apply, and the merge a caller makes of a [Put]), never on read. That is the whole reason the enums have no empty member.
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
// Each kind names an evidence source the grader already holds: the output store, the VCS
// diff, the knowledge graph. A condition the grader can observe no evidence for is not declarable
// here on purpose, and there is deliberately no escape hatch for one. A goal accepting an
// unrecorded exit status would be the easiest kind to satisfy falsely, which is the
// attestation the whole mechanism replaces; declare a target and use GoalKindCheck.
type GoalKind string

const (
	// GoalKindCheck examines a recorded run of the goal's check. It is a return code, and
	// what raises it above one is that the grader RECORDED it: the output store holds the
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

// goalKinds is the closed set, for the same reason states is one: the validator, the
// published schema and the error each of them raises all quote it, and a vocabulary that
// drifts rejects a client for a value the schema told it to send.
func goalKinds() []GoalKind {
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

// goalExpects is the closed set. See goalKinds.
func goalExpects() []GoalExpect {
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

// Subject is the paths or symbols the goal examines, for a message that has to name them.
// A check goal's subject is its check, which only the store's [CheckRender] renders, so
// it is nil here.
func (g Goal) Subject() []string {
	switch g.Kind {
	case GoalKindCheck:
		return nil
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
func goalAccepts(kind GoalKind) ([]GoalExpect, bool) {
	switch kind {
	case GoalKindCheck:
		return []GoalExpect{ExpectPassed}, true
	case GoalKindPaths:
		return []GoalExpect{ExpectChanged, ExpectPresent, ExpectAbsent}, true
	case GoalKindSymbol:
		return []GoalExpect{ExpectChanged, ExpectPresent, ExpectAbsent, ExpectUnreferenced, ExpectSignature}, true
	}
	return nil, false
}

// EffectiveGoals returns the declared goals plus the primary Check as a goal. Keeping the
// projection at the model boundary means guards that still read Check retain their
// capability semantics while every verifier has one complete collection to grade.
func (j Job) EffectiveGoals() []Goal {
	goals := cloneGoals(j.Goals)
	if HasCheck(j.Check) {
		// The lease's own check is run by whoever holds the lease, through the runner that
		// started it, so its run is witnessed; Check carries no accept list of its own.
		primary := Goal{ID: PrimaryGoalID, Check: slices.Clone(j.Check), Accept: []Basis{BasisWitnessed}}.Resolve()
		goals = append([]Goal{primary}, goals...)
	}
	return goals
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
	switch accepted, known := goalAccepts(g.Kind); {
	case !known:
		bad("kind", fmt.Sprintf("goal %q names kind %q", g.ID, g.Kind), "use one of "+quoteJoin(goalKinds(), ", "))
	case !slices.Contains(accepted, g.Expect):
		bad("expect", fmt.Sprintf("goal %q is a %s goal expecting %q", g.ID, g.Kind, g.Expect),
			fmt.Sprintf("a %s goal expects %s", g.Kind, quoteJoin(accepted, " or ")))
	}
	// Exactly one subject, named by the kind. A goal carrying two is one whose author
	// changed their mind, and grading the one the kind happens to read would silently
	// ignore the other.
	if g.Kind != GoalKindCheck && HasCheck(g.Check) {
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
	names := len(trimmedNonEmpty(g.Subject())) > 0
	if g.Kind == GoalKindCheck {
		names = HasCheck(g.Check)
	}
	if subject != "" && !names {
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

