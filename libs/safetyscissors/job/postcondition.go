package job

import (
	"fmt"
	"slices"
	"strings"
)

// PrimaryPostconditionID names the job's singular check when it is projected into its postconditions. It
// is a stable identity, not a user assertion.
const PrimaryPostconditionID = "check"

// Postcondition is one machine-verifiable condition a job must satisfy before it can pass.
// Criteria remains the human-readable objective; postconditions bind that objective to recorded
// output rather than a holder's boolean attestation. The output must have been
// captured after the job was declared; target execution limits remain the target's run
// policy rather than a second postcondition timeout.
type Postcondition struct {
	ID          string   `json:"id"                     yaml:"id"                     schema:"leaseid"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"  yaml:"depends_on,omitempty"  schema:"leaseid"`
	// Kind names WHAT this postcondition examines and Expect names what must be true of it. Two
	// fields rather than one, because the alternative is a kind per pair: `paths` beside
	// `exists` beside `absent` beside `no-symbol`, which is a vocabulary that grows by
	// multiplication and reads inconsistently the moment it has four members.
	//
	// Both are RESOLVED before a job is stored. A default applied on read is
	// one every reader must know.
	Kind PostconditionKind `json:"kind" yaml:"kind"`
	// Expect is optional and defaults to the kind's own: passed for check, changed for
	// paths and symbol. The common postcondition names only its kind and its subject.
	Expect PostconditionExpect `json:"expect,omitempty" yaml:"expect,omitempty"`
	// Check is the run a PostconditionKindCheck postcondition examines, which the store reads through
	// [WithCheckSpec]. Zero on every other kind.
	Check Check `json:"check,omitzero"`
	// Paths are the globs a PostconditionKindPaths postcondition examines. Zero on every other kind.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	// Symbols are the names a PostconditionKindSymbol postcondition examines, as the knowledge graph
	// resolves them. Zero on every other kind.
	Symbols []string `json:"symbols,omitempty" yaml:"symbols,omitempty"`
	// Signature is the declaration an ExpectSignature postcondition holds its one symbol to, in the
	// form the symbol index renders it. A fork that declares none records the indexed one,
	// which is how a stub the root wrote becomes the contract. Zero on every other postcondition.
	Signature string `json:"signature,omitempty" yaml:"signature,omitempty"`
	// Accept are the bases a run of a PostconditionKindCheck postcondition's check may rest on (see
	// [Store.Grade]). A check postcondition names at least one: [Postcondition.Resolve] fills none in, so
	// one accepting nothing is a fault, never a postcondition that takes the strongest or the
	// weakest. Zero on every other kind.
	Accept []Basis `json:"accept,omitempty" yaml:"accept,omitempty"`
}

// Resolve fills a postcondition's kind and expectation from what it declared, so every stored postcondition
// names both. A postcondition that named neither is a check, which is what the single Check field
// meant before postconditions existed; a postcondition that named a kind and no expectation takes that
// kind's natural one.
//
// Called at the WRITE boundary (Declaration.Apply, and the merge a caller makes of a [Put]), never on read. That is the whole reason the enums have no empty member.
func (g Postcondition) Resolve() Postcondition {
	if g.Kind == "" {
		g.Kind = PostconditionKindCheck
	}
	if g.Expect == "" {
		g.Expect = g.Kind.defaultExpect()
	}
	return g
}

// PostconditionKind names WHAT a postcondition examines, because not every condition a job can be held to is
// a target run.
//
// Each kind names an evidence source the grader already holds: the output store, the VCS
// diff, the knowledge graph. A condition the grader can observe no evidence for is not declarable
// here on purpose, and there is deliberately no escape hatch for one. A postcondition accepting an
// unrecorded exit status would be the easiest kind to satisfy falsely, which is the
// attestation the whole mechanism replaces; declare a target and use PostconditionKindCheck.
type PostconditionKind string

const (
	// PostconditionKindCheck examines a recorded run of the postcondition's check. It is a return code, and
	// what raises it above one is that the grader RECORDED it: the output store holds the
	// target, the project, the timestamp and the failure bit, so the run can be reopened
	// and attributed to this job rather than taken on the holder's word.
	PostconditionKindCheck PostconditionKind = "check"
	// PostconditionKindPaths examines files, by glob. It is what "this job must actually produce
	// the migration" looks like when no target can say so.
	PostconditionKindPaths PostconditionKind = "paths"
	// PostconditionKindSymbol examines named symbols in the knowledge graph, which is the
	// granularity below a file: a function added, renamed or deleted is a fact the graph
	// holds even when the file it lives in changed for ten other reasons.
	PostconditionKindSymbol PostconditionKind = "symbol"
)

// postconditionKinds is the closed set, for the same reason states is one: the validator, the
// published schema and the error each of them raises all quote it, and a vocabulary that
// drifts rejects a client for a value the schema told it to send.
func postconditionKinds() []PostconditionKind {
	return []PostconditionKind{PostconditionKindCheck, PostconditionKindPaths, PostconditionKindSymbol}
}

// PostconditionExpect names what must be TRUE of what a postcondition examines. One vocabulary across every
// kind, so `absent` means the same thing of a file, a symbol and a reference.
type PostconditionExpect string

const (
	// ExpectPassed is a recorded run that finished without failing. PostconditionKindCheck only.
	ExpectPassed PostconditionExpect = "passed"
	// ExpectChanged is "the diff since the job's checkpoint touches this".
	ExpectChanged PostconditionExpect = "changed"
	// ExpectPresent is "this is here now", whether or not this job is what put it here.
	ExpectPresent PostconditionExpect = "present"
	// ExpectAbsent is "this is not here now", which is how a deletion or a removal is
	// declared as a condition rather than reported as one.
	ExpectAbsent PostconditionExpect = "absent"
	// ExpectUnreferenced is "nothing names this any more", and it belongs to symbols. It
	// is the one that answers the remainder a partitioned rename leaks: split the work per
	// project and the callers in no project belong to no job, so every job passes and the
	// rename is unfinished.
	//
	// An EXPECTATION rather than a kind of its own, because the subject it examines is
	// still the symbol. A `refs` kind read the same field as `symbol` and forced every
	// reader to treat the two as one, which is a vocabulary that says it has four members
	// and behaves as though it has three.
	ExpectUnreferenced PostconditionExpect = "unreferenced"
	// ExpectSignature is "this symbol is declared with this shape": the declaration the
	// postcondition's Signature names. Go and Buzz compare the parsed shape, so a renamed parameter
	// still holds; other languages compare the text.
	ExpectSignature PostconditionExpect = "signature"
)

// postconditionExpects is the closed set. See postconditionKinds.
func postconditionExpects() []PostconditionExpect {
	return []PostconditionExpect{ExpectPassed, ExpectChanged, ExpectPresent, ExpectAbsent, ExpectUnreferenced, ExpectSignature}
}

// defaultExpect is what a kind means when a postcondition names no condition, so the common postcondition of
// each kind declares only its subject. A check is asked whether it passed; files and
// symbols are asked whether this job changed them, which is the question a job is for.
func (k PostconditionKind) defaultExpect() PostconditionExpect {
	if k == PostconditionKindCheck {
		return ExpectPassed
	}
	return ExpectChanged
}

// Subject is the paths or symbols the postcondition examines, for a message that has to name them.
// A check postcondition's subject is its check, which only the store's [CheckSpec] renders, so
// it is nil here.
func (g Postcondition) Subject() []string {
	switch g.Kind {
	case PostconditionKindCheck:
		return nil
	case PostconditionKindPaths:
		return g.Paths
	default:
		return g.Symbols
	}
}

// postconditionAccepts is the kind-to-condition matrix, and the one place it is written down.
// Anything outside it is refused at declaration rather than graded into a verdict nobody
// can act on: `check` + `absent` has no meaning, and a postcondition nobody can satisfy reads as a
// job nobody can finish.
func postconditionAccepts(kind PostconditionKind) ([]PostconditionExpect, bool) {
	switch kind {
	case PostconditionKindCheck:
		return []PostconditionExpect{ExpectPassed}, true
	case PostconditionKindPaths:
		return []PostconditionExpect{ExpectChanged, ExpectPresent, ExpectAbsent}, true
	case PostconditionKindSymbol:
		return []PostconditionExpect{ExpectChanged, ExpectPresent, ExpectAbsent, ExpectUnreferenced, ExpectSignature}, true
	}
	return nil, false
}

// EffectivePostconditions returns the declared postconditions plus the primary Check as a postcondition. Keeping the
// projection at the model boundary means guards that still read Check retain their
// capability semantics while every verifier has one complete collection to grade.
func (j Job) EffectivePostconditions() []Postcondition {
	postconditions := clonePostconditions(j.Postconditions)
	if !j.Check.IsZero() {
		// The lease's own check is run by whoever holds the lease, through the runner that
		// started it, so its run is witnessed; Check carries no accept list of its own.
		primary := Postcondition{ID: PrimaryPostconditionID, Check: j.Check, Accept: []Basis{BasisWitnessed}}.Resolve()
		postconditions = append([]Postcondition{primary}, postconditions...)
	}
	return postconditions
}

// postconditionFaults is every fault of a record's postconditions: each postcondition's own shape, then what
// only the set shows. primary says the record declares a check, the postcondition named
// PrimaryPostconditionID a depends_on may name.
func postconditionFaults(postconditions []Postcondition, primary bool) []Fault {
	var ps []Fault
	bad := func(field, message, fix string) {
		ps = append(ps, Fault{Kind: FaultPostcondition, Field: field, Message: message, Fix: fix})
	}
	index := make(map[string]int, len(postconditions))
	if primary {
		index[PrimaryPostconditionID] = -1
	}
	for i, gate := range postconditions {
		at := fmt.Sprintf("postconditions[%d]", i)
		ps = append(ps, gate.faults(at)...)
		if _, dup := index[gate.ID]; dup {
			bad(at+".id", fmt.Sprintf("postconditions carries duplicate id %q", gate.ID), "give each postcondition its own id")
			continue
		}
		index[gate.ID] = i
	}
	ps = append(ps, oneSignaturePerSymbol(postconditions)...)
	for i, gate := range postconditions {
		for j, dep := range gate.DependsOn {
			at := fmt.Sprintf("postconditions[%d].depends_on[%d]", i, j)
			switch _, known := index[dep]; {
			case dep == gate.ID:
				bad(at, fmt.Sprintf("postcondition %q cannot depend on itself", gate.ID), "drop the entry")
			case !known:
				bad(at, fmt.Sprintf("postcondition %q depends_on unknown postcondition %q", gate.ID, dep), "name a postcondition this record declares")
			}
		}
	}
	visiting := make(map[string]bool, len(postconditions))
	visited := make(map[string]bool, len(postconditions))
	var visit func(string)
	visit = func(id string) {
		i, known := index[id]
		switch {
		case !known || i < 0 || visited[id]:
			return
		case visiting[id]:
			visited[id] = true
			bad(fmt.Sprintf("postconditions[%d].depends_on", i), fmt.Sprintf("postcondition dependencies contain a cycle at %q", id),
				"drop one depends_on entry on the cycle")
			return
		}
		visiting[id] = true
		for _, dep := range postconditions[i].DependsOn {
			if dep != id {
				visit(dep)
			}
		}
		visiting[id] = false
		visited[id] = true
	}
	for _, gate := range postconditions {
		visit(gate.ID)
	}
	return ps
}

// oneSignaturePerSymbol refuses two signature postconditions on one symbol: the graph answers one
// declaration per name, so two shapes for it make a postcondition nobody could meet.
func oneSignaturePerSymbol(postconditions []Postcondition) []Fault {
	var ps []Fault
	held := map[string]string{}
	for i, gate := range postconditions {
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
					Kind:    FaultPostcondition,
					Field:   fmt.Sprintf("postconditions[%d].symbols[%d]", i, j),
					Message: fmt.Sprintf("postconditions %q and %q both expect a signature of %q, and a symbol has one", other, gate.ID, name),
					Fix:     "keep one signature postcondition per symbol",
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
func (g Postcondition) Faults() []Fault { return g.faults("") }

// faults is every fault g shows on its own, each Field under field (`postconditions[1]`).
func (g Postcondition) faults(field string) []Fault {
	var ps []Fault
	bad := func(sub, message, fix string) {
		at := sub
		if field != "" {
			at = fmt.Sprintf("%s.%s", field, sub)
		}
		ps = append(ps, Fault{Kind: FaultPostcondition, Field: at, Message: message, Fix: fix})
	}
	if rule := idRule(strings.TrimSpace(g.ID)); rule != "" {
		bad("id", fmt.Sprintf("id %q is not a postcondition id: %s", g.ID, rule), "use a lowercase, branch-shaped id")
	}
	// Validates what the job WILL BE, not what arrived. A declaration off the
	// wire has not been through Resolve yet.
	g = g.Resolve()
	switch accepted, known := postconditionAccepts(g.Kind); {
	case !known:
		bad("kind", fmt.Sprintf("postcondition %q names kind %q", g.ID, g.Kind), "use one of "+quoteJoin(postconditionKinds(), ", "))
	case !slices.Contains(accepted, g.Expect):
		bad("expect", fmt.Sprintf("postcondition %q is a %s postcondition expecting %q", g.ID, g.Kind, g.Expect),
			fmt.Sprintf("a %s postcondition expects %s", g.Kind, quoteJoin(accepted, " or ")))
	}
	// Exactly one subject, named by the kind. A postcondition carrying two is one whose author
	// changed their mind, and grading the one the kind happens to read would silently
	// ignore the other.
	if g.Kind != PostconditionKindCheck && !g.Check.IsZero() {
		bad("check", fmt.Sprintf("postcondition %q is a %s postcondition and also carries a check", g.ID, g.Kind), "a postcondition examines one subject: drop the check")
	}
	if g.Kind != PostconditionKindPaths && len(trimmedNonEmpty(g.Paths)) > 0 {
		bad("paths", fmt.Sprintf("postcondition %q is a %s postcondition and also carries paths", g.ID, g.Kind), "a postcondition examines one subject: drop the paths")
	}
	if g.Kind != PostconditionKindSymbol && len(trimmedNonEmpty(g.Symbols)) > 0 {
		bad("symbols", fmt.Sprintf("postcondition %q is a %s postcondition and also carries symbols", g.ID, g.Kind), "a postcondition examines one subject: drop the symbols")
	}
	// An unknown kind has no subject field, and its own problem above says so.
	subject := map[PostconditionKind]string{PostconditionKindCheck: "check", PostconditionKindPaths: "paths", PostconditionKindSymbol: "symbols"}[g.Kind]
	names := len(trimmedNonEmpty(g.Subject())) > 0
	if g.Kind == PostconditionKindCheck {
		names = !g.Check.IsZero()
	}
	if subject != "" && !names {
		bad(subject, fmt.Sprintf("postcondition %q is a %s postcondition and names nothing to examine, so nothing could ever satisfy it", g.ID, g.Kind),
			"name what the postcondition examines")
	}
	switch {
	case g.Expect != ExpectSignature && strings.TrimSpace(g.Signature) != "":
		bad("signature", fmt.Sprintf("postcondition %q expects %q and carries a signature, which only a %q postcondition reads", g.ID, g.Expect, ExpectSignature),
			"drop the signature")
	case g.Expect == ExpectSignature && len(trimmedNonEmpty(g.Symbols)) != 1:
		bad("symbols", fmt.Sprintf("postcondition %q expects the signature of %d symbols, and a signature is one declaration", g.ID, len(trimmedNonEmpty(g.Symbols))),
			"name one symbol per postcondition")
	}
	if g.Kind != PostconditionKindCheck {
		if len(g.Accept) > 0 {
			bad("accept", fmt.Sprintf("postcondition %q is a %s postcondition and carries accept, which only a check postcondition reads", g.ID, g.Kind), "drop accept")
		}
		return ps
	}
	acceptFaults(g, bad)
	return ps
}

// acceptFaults reports every fault of a check postcondition's accept list through bad.
func acceptFaults(g Postcondition, bad func(sub, message, fix string)) {
	fix := "accept one or more of " + quoteJoin(Bases(), ", ")
	if len(g.Accept) == 0 {
		bad("accept", fmt.Sprintf("postcondition %q is a check postcondition accepting no basis, so no run could grade it", g.ID), fix)
		return
	}
	seen := map[Basis]bool{}
	for i, b := range g.Accept {
		switch {
		case b == "" || !b.Valid():
			bad(fmt.Sprintf("accept[%d]", i), fmt.Sprintf("postcondition %q accepts %q, which is not a basis", g.ID, b), fix)
		case seen[b]:
			bad(fmt.Sprintf("accept[%d]", i), fmt.Sprintf("postcondition %q accepts %q twice", g.ID, b), "name each basis once")
		}
		seen[b] = true
	}
}
