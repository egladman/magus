package cache

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
)

// SameStepConflict is one target reading, inside a single step, what another target of
// that same step writes, with nothing sequencing the two.
//
// This is the one overlap magus must refuse rather than schedule around. Across steps the
// engine derives writer-before-reader order itself (DeriveTargetOrder); within one step
// the sequencing belongs to the composing body, and ctx.needs is the only thing that can
// express it.
type SameStepConflict struct {
	// Step is the node key of the step whose chain runs both targets.
	Step string
	// Writer and Reader are node keys (DepKey).
	Writer, Reader string
	// Write and Read are the overlapping pair of declared globs, workspace-rooted.
	Write, Read string
	// SameProject reports that reader and writer belong to one project, so the fix is a
	// ctx.needs in the file that composes both. A cross-project pair is real too, but its
	// sequencing may belong to another project's magusfile, so it advises rather than
	// refuses (see SameStepConflictError).
	SameProject bool
}

// OverlapWitness decides whether a write glob and a read glob meet on a path that
// actually exists. Glob-against-glob intersection is conservative on purpose, since
// over-ordering is cheap, but a refusal has to stand on a file: "reads **/MAGUS.md
// alongside a writer of cmd/magus/completions/*" intersects as globs and never as paths.
// nil means no witness is required, which keeps fixtures with invented globs testable.
type OverlapWitness func(write, read string) bool

// WorkspaceOverlapWitness answers from the workspace tree: the write glob is expanded
// once and each hit is matched against the read glob. Expansion is memoized per write
// glob because the same writer meets many readers in one plan.
func WorkspaceOverlapWitness(root string) OverlapWitness {
	tree := os.DirFS(root)
	hits := map[string][]string{}
	return func(write, read string) bool {
		paths, seen := hits[write]
		if !seen {
			// An unreadable or absent path reads as no witness: a refusal over a glob
			// that matched nothing on disk is the guess this exists to rule out.
			paths, _ = doublestar.Glob(tree, write, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
			hits[write] = paths
		}
		for _, p := range paths {
			if ok, err := doublestar.Match(read, p); ok && err == nil {
				return true
			}
		}
		return false
	}
}

// FindSameStepConflicts reports the same-step pairs no schedule can order: both sides
// declared explicitly, the writer's writes reaching the reader's reads on a path the
// witness confirms, and no ctx.needs path between the two in either direction.
//
// Baseline fallbacks are excluded on either side. A fallback footprint is a whole-project
// over-approximation, so an overlap through one is a guess, and refusing a run over a
// guess costs more than the stale read it would prevent.
//
// A needs path in EITHER direction clears the pair. Reader-after-writer is the fix this
// reports. Writer-after-reader is a sequencing the author wrote down: the reader reads
// what was there beforehand, which is a staleness question rather than a plan that cannot
// be scheduled.
//
// Deterministic: results are sorted by step, then writer, then reader.
func FindSameStepConflicts(nodes []TargetNode, witness OverlapWitness) []SameStepConflict {
	byKey := make(map[string]TargetNode, len(nodes))
	for _, n := range nodes {
		byKey[n.Key()] = n
	}
	var out []SameStepConflict
	for w := range nodes {
		for r := range nodes {
			if w == r || nodes[w].Key() == nodes[r].Key() {
				continue
			}
			if !nodes[w].DeclaredWrites || !nodes[r].DeclaredReads {
				continue
			}
			step, ok := sharedStep(nodes[w], nodes[r])
			if !ok {
				continue
			}
			write, read, ok := overlappingGlobs(nodes[w], nodes[r])
			if !ok {
				continue
			}
			if witness != nil && !witness(write, read) {
				continue
			}
			if needsPath(byKey, nodes[r].Key(), nodes[w].Key()) || needsPath(byKey, nodes[w].Key(), nodes[r].Key()) {
				continue
			}
			out = append(out, SameStepConflict{
				Step: step, Writer: nodes[w].Key(), Reader: nodes[r].Key(),
				Write: write, Read: read,
				SameProject: nodes[w].Project == nodes[r].Project,
			})
		}
	}
	slices.SortFunc(out, func(a, b SameStepConflict) int {
		return strings.Compare(a.Step+a.Writer+a.Reader, b.Step+b.Writer+b.Reader)
	})
	return out
}

// sharedStep returns the first step key (in a's order) that runs both nodes.
func sharedStep(a, b TargetNode) (string, bool) {
	for _, s := range a.Steps {
		if slices.Contains(b.Steps, s) {
			return s, true
		}
	}
	return "", false
}

// needsPath reports whether from reaches to by following ctx.needs edges, so the schedule
// already puts to first. Bounded by the node count, which is what keeps a chain the loader
// somehow admitted with a cycle in it from spinning here.
func needsPath(byKey map[string]TargetNode, from, to string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, next := range byKey[k].Needs {
			if next == to {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// SameStepConflictError is the MGS4008 refusal for the SAME-project conflicts, or nil
// for none. It names the reader, the writer, the globs that overlap and both fixes,
// because a reader meeting this has to change a declaration and the message is where
// the choice is made.
//
// Same-project only: there the fix is one ctx.needs in the file that composes both, and
// nothing else can be meant. A cross-project pair reaches the same step through another
// project's chain, whose sequencing that project's author owns, and this tree's routing
// indexes read each other by design, so those pairs go to SameStepAdvice instead.
//
// Only the first conflict is spelled out. The rest are counted: they are usually the same
// authoring mistake seen from several members, and a wall of near-identical sentences
// buries the one worth reading.
func SameStepConflictError(conflicts []SameStepConflict) error {
	var own []SameStepConflict
	for _, c := range conflicts {
		if c.SameProject {
			own = append(own, c)
		}
	}
	if len(own) == 0 {
		return nil
	}
	c := own[0]
	more := ""
	if n := len(own) - 1; n > 0 {
		more = fmt.Sprintf(" %d further pair(s) in this run overlap the same way.", n)
	}
	return types.DiagnosticErrorf(types.UnorderedSameStepWrite,
		"refusing to run %s: %s reads %q, which %s writes as %q, and nothing orders the two."+
			" Both run inside %s's ctx.needs chain, with no needs path between them, so the reader"+
			" can start before the writer finishes and can wedge the run waiting for a slot the"+
			" writer needs. Declare ctx.needs(%s) in %s so it runs after the writer, or narrow %s's"+
			" ctx.readsFiles so it no longer matches what the writer produces.%s",
		displayKey(c.Step), displayKey(c.Reader), c.Read, displayKey(c.Writer), c.Write,
		displayKey(c.Step), targetOf(c.Writer), displayKey(c.Reader), displayKey(c.Reader), more)
}

// SameStepAdvice is the one-line notice for the CROSS-project conflicts, or "" for none:
// the same overlap, reported rather than refused, with the first pair named and the rest
// counted. `magus doctor` lists every one under the same code.
func SameStepAdvice(conflicts []SameStepConflict) string {
	var cross []SameStepConflict
	for _, c := range conflicts {
		if !c.SameProject {
			cross = append(cross, c)
		}
	}
	if len(cross) == 0 {
		return ""
	}
	c := cross[0]
	more := ""
	if n := len(cross) - 1; n > 0 {
		more = fmt.Sprintf("; %d more such pair(s)", n)
	}
	return fmt.Sprintf("[%s] %s reads %q inside %s's chain, which %s writes as %q, and nothing orders the two across projects%s; `magus doctor` lists them (see %s)",
		types.UnorderedSameStepWrite, displayKey(c.Reader), c.Read, displayKey(c.Step),
		displayKey(c.Writer), c.Write, more, types.CodeURL(types.UnorderedSameStepWrite))
}

// targetOf is a node key's target half, which is what a ctx.needs call names.
func targetOf(key string) string {
	_, target, ok := strings.Cut(key, nodeKeySep)
	if !ok {
		return key
	}
	return target
}

// DeclaredNodes builds the nodes of one composer's ctx.needs closure: the composer itself
// and every target it composes, transitively, with the step key of the composer.
//
// It reads DECLARED footprints only, where a batch's own node collection also carries a
// project baseline for a target that declares none. That is not a second opinion about
// what a target touches: FindSameStepConflicts ignores a baseline on either side, so the
// two collections agree on every pair either can report, and a caller with no batch in
// hand (doctor) needs no engine to ask the question.
//
// lookup resolves a cross-project chain step and may return nil, in which case that step
// and everything under it contribute nothing rather than a guess.
func DeclaredNodes(p *types.Project, composer string, lookup func(path string) *types.Project) []TargetNode {
	if p == nil {
		return nil
	}
	step := DepKey(p.Path, composer)
	byKey := map[string]*TargetNode{}
	var order []string

	var walk func(proj *types.Project, target string)
	walk = func(proj *types.Project, target string) {
		if proj == nil {
			return
		}
		key := DepKey(proj.Path, target)
		if _, ok := byKey[key]; ok {
			return
		}

		updates := make([]string, 0, len(proj.TargetUpdates[target]))
		for _, ref := range proj.TargetUpdates[target] {
			updates = append(updates, rootedGlob(proj, ref.Project, ref.Glob))
		}
		var reads []string
		declaredReads := len(proj.TargetInputs[target]) > 0
		if declaredReads {
			for _, ref := range proj.TargetInputs[target] {
				reads = append(reads, rootedGlob(proj, ref.Project, ref.Glob))
			}
			reads = append(reads, updates...)
		}
		writes := make([]string, 0, len(proj.TargetOutputs[target])+len(updates))
		for _, ref := range proj.TargetOutputs[target] {
			writes = append(writes, rootedGlob(proj, ref.Project, ref.Glob))
		}
		writes = append(writes, updates...)

		node := &TargetNode{
			Project: proj.Path, Target: target, Steps: []string{step},
			Reads: reads, Writes: writes,
			DeclaredReads:  declaredReads,
			DeclaredWrites: len(proj.TargetOutputs[target]) > 0 || len(updates) > 0,
		}
		byKey[key] = node
		order = append(order, key)
		chain := proj.TargetChains[target]
		keyOf := func(cs types.ChainStep) (string, bool) {
			owner := lookupOwner(proj, cs, lookup)
			if owner == nil {
				return "", false
			}
			return DepKey(owner.Path, cs.Target), true
		}
		for _, cs := range chain {
			memberKey, ok := keyOf(cs)
			if !ok {
				continue
			}
			node.Needs = append(node.Needs, memberKey)
			walk(lookupOwner(proj, cs, lookup), cs.Target)
		}
		for memberKey, earlier := range StageNeeds(chain, keyOf) {
			// Every member walked above exists; a step keyOf rejected has no entry.
			member := byKey[memberKey]
			for _, e := range earlier {
				if !slices.Contains(member.Needs, e) {
					member.Needs = append(member.Needs, e)
				}
			}
		}
	}
	walk(p, composer)

	nodes := make([]TargetNode, 0, len(order))
	for _, k := range order {
		nodes = append(nodes, *byKey[k])
	}
	return nodes
}

// lookupOwner resolves the project a chain step runs in: the composer's own for a local
// step, lookup's answer for a cross-project one, nil when there is none.
func lookupOwner(proj *types.Project, cs types.ChainStep, lookup func(path string) *types.Project) *types.Project {
	if cs.Project == "" || cs.Project == proj.Path {
		return proj
	}
	if lookup == nil {
		return nil
	}
	return lookup(cs.Project)
}

// StageNeeds lists, per chain member, the members of the earlier ctx.needs calls in the
// same body. One call fans its arguments out unordered and returns when all of them have
// run, so a later stage is ordered after every earlier one by the composer itself; a
// collector records that as needs edges on the member, which is the form needsPath
// already reads every other ordering in. keyOf names a step's node and says no for a
// step that resolves to nothing, which then neither orders nor is ordered.
//
// The edges land on the member's node, shared with every composer that reaches it. A
// target one composer runs in a later stage so carries that ordering into another
// composer that fans it out with the same writer in one call, and that pair goes
// unreported; both composers have to be scheduled in one invocation for it to matter.
func StageNeeds(chain []types.ChainStep, keyOf func(types.ChainStep) (string, bool)) map[string][]string {
	out := map[string][]string{}
	var earlier, current []string
	stage := 0
	for _, cs := range chain {
		key, ok := keyOf(cs)
		if !ok {
			continue
		}
		if cs.Stage != stage {
			earlier = append(earlier, current...)
			current = nil
			stage = cs.Stage
		}
		current = append(current, key)
		if len(earlier) > 0 {
			out[key] = slices.Clone(earlier)
		}
	}
	return out
}

// rootedGlob resolves a declared ref to a workspace-rooted glob. A ref with no project of
// its own belongs to the project that declared it.
func rootedGlob(p *types.Project, refProject, glob string) string {
	if refProject == "" {
		return types.RootGlob(p.Path, glob)
	}
	return types.RootGlob(refProject, glob)
}
