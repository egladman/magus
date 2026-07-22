package knowledge

import (
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Assembly is composition, not analysis: it maps machinery magus already owns
// (the static magusfile extraction behind DescribeGraph, the spell/module/
// diagnostic registries) onto knowledge nodes and edges. No execution, no LLM.
// Every Phase 1 edge is EXTRACTED with score 1.0.

// RegistryShardName is the logical name of the singleton shard holding
// workspace-independent, compiled-in entities (spells, ops, modules, methods,
// diagnostics). The leading "@" keeps it from colliding with any project path.
const RegistryShardName = "@registry"

// Inputs are the already-gathered describe outputs the assembler composes. The
// caller (the CLI/composition root) fetches these from the workspace so that
// internal/graph/knowledge depends only on types - it never reaches into the registry,
// host, or spell packages itself.
type Inputs struct {
	Graph       types.TargetGraphOutput // DescribeGraph(): projects, targets, deps, charms, spell ops
	Spells      types.SpellsOutput      // DescribeSpells(): spell + op nodes
	Modules     []types.ModuleEntry     // host modules, each with Methods populated
	Diagnostics []types.DiagnosticCode  // AllDiagnosticCodes()
	// Root is the absolute workspace root, used by the docs and buzz-source
	// extractors to scan the filesystem. Empty disables those extractors (the
	// store tests build from synthetic Inputs with no tree to scan).
	Root string
	// Runtime carries diagnostics fired during prior runs, read from the local
	// runtime records. It is the ONLY non-deterministic input: derived from run
	// history, not workspace sources, so it lands in an isolated @runtime shard
	// that is excluded from remote export and skippable at load.
	Runtime []types.DiagnosticEvent
	// Timings carries observed per-target run cost (p75 duration, cache hit rate)
	// from the local timing history. Like Runtime it is non-deterministic and lands
	// in the @runtime shard; it annotates existing target nodes rather than adding
	// edges.
	Timings []types.KnowledgeTiming
	// OutputRefs carries each target's most recent captured-output reference from the
	// local output store. Like Timings it is non-deterministic and lands in the @runtime
	// shard, folding last_output_ref / last_run_ok attrs onto existing target nodes rather
	// than adding edges - the query -> target -> last output two-hop.
	OutputRefs []types.KnowledgeOutputRef
	// Symbols maps a project path to the code symbols ingested from its SCIP index
	// (empty unless the project declares one in config). Each becomes a per-project
	// @symbols shard - deterministic, so remote-shareable like the other extracted
	// shards, and destined for lazy loading (it can dwarf the domain graph).
	Symbols map[string][]types.KnowledgeSymbol
	// VCS carries per-file git history metadata (empty unless knowledge.vcs.enabled and
	// the workspace is a git repo). It folds onto existing file nodes in the @vcs shard
	// as attrs - deterministic per commit, so remote-shareable.
	VCS []types.KnowledgeVCS
	// DeclaredSpells is the set of spell names some project declares in its magusfile
	// `spells:` list (the union over projects). It lets the orphan lens tell a genuinely
	// dead spell (declared here, nothing runs it) from a compiled-in builtin that is
	// merely available and unused - only declared spells are orphan candidates.
	DeclaredSpells map[string]bool
	// VCSAuthorship includes the author nodes + authored edges in the @vcs shard
	// (knowledge.vcs.authorship, default on). False keeps only the per-file vcs_* attrs.
	VCSAuthorship bool
	// Coverage carries per-file statement coverage parsed from the local Go coverage
	// profile (empty unless a profile is present). Like Runtime/Timings it is observed,
	// not extracted, so it lands in the isolated @coverage shard - folding a coverage
	// ratio onto the file (and, via SCIP def lines, symbol) nodes rather than churning
	// the deterministic @symbols shards it annotates.
	Coverage []FileCoverage
}

// Shard is a named, independently-fingerprinted slice of the graph: one per
// project (its magusfile-derived nodes) plus the singleton registry shard.
// Shards are authoritative on disk; the merged graph is assembled in memory.
type Shard struct {
	Name  string
	Nodes []types.KnowledgeNode
	Edges []types.KnowledgeEdge
}

// AssembleShards builds every shard from the gathered inputs: the registry shard
// plus one per project in the graph. Order is registry first, then projects in
// their DescribeGraph order.
func AssembleShards(in Inputs) []Shard {
	shards := make([]Shard, 0, len(in.Graph.Projects)+3)
	shards = append(shards, assembleRegistry(in))
	for _, p := range in.Graph.Projects {
		shards = append(shards, assembleProject(p))
	}
	// The path-bearing nodes CODEOWNERS is matched against: every project, plus buzz
	// files once that shard is built below. Projects are known up front from the graph.
	owned := make([]ownedNode, 0, len(in.Graph.Projects))
	for _, p := range in.Graph.Projects {
		owned = append(owned, ownedNode{ID: projectID(p.Path), Path: p.Path})
	}
	// Docs and buzz-source extraction scan the filesystem, so they run only when a
	// workspace root is set (synthetic-Inputs tests leave it empty). Empty shards
	// are dropped so no empty files are persisted.
	// pathToNode maps a workspace-relative path to the file/doc node sitting at it, so the
	// I/O pass can resolve each target's output/input globs to the nodes they produce or
	// consume. Populated as the path-bearing shards (docs, buzz, symbols) are built.
	pathToNode := map[string]string{}
	if in.Root != "" {
		if d := assembleDocs(in.Root, in.Spells, in.Graph.Projects); len(d.Nodes) > 0 {
			for _, n := range d.Nodes {
				pathToNode[n.Source] = n.ID
			}
			shards = append(shards, d)
		}
		fileNodePaths := map[string]bool{}
		if b := assembleBuzz(in.Root); len(b.Nodes) > 0 {
			for _, n := range b.Nodes {
				if n.Kind != types.KindFile {
					continue
				}
				owned = append(owned, ownedNode{ID: n.ID, Path: n.Source})
				fileNodePaths[n.Source] = true
				pathToNode[n.Source] = n.ID
				// Link each source file to the project that owns it. Without this a file
				// (and any symbol defined in it) reaches only its functions and imports,
				// never up to its project or the workspace. Edges added before the shard
				// is appended, since a Shard is stored by value.
				if owner, ok := owningProjectPath(n.Source, in.Graph.Projects); ok {
					dn, de := containsChain(owner, n.Source, n.ID)
					b.Nodes = append(b.Nodes, dn...)
					b.Edges = append(b.Edges, de...)
				}
			}
			shards = append(shards, b)
		}
		if o := assembleOwners(in.Root, owned); len(o.Edges) > 0 {
			shards = append(shards, o)
		}
		// Git history folds onto the file nodes just built; a path with no file node
		// (VCS metadata for a file the graph does not model) is dropped, no phantom.
		if v := assembleVCS(in.VCS, fileNodePaths, in.VCSAuthorship); len(v.Nodes) > 0 {
			shards = append(shards, v)
		}
	}
	// The runtime shard carries both non-deterministic inputs: emits edges from
	// prior diagnostics and timing attrs on existing targets. Timings are filtered
	// to targets that actually exist so stale history never conjures a phantom node.
	if r := assembleRuntime(in.Runtime, in.Timings, in.OutputRefs, knownTargetIDs(in.Graph)); len(r.Edges) > 0 || len(r.Nodes) > 0 {
		shards = append(shards, r)
	}
	// One @symbols shard per project that declared an index, in sorted project order
	// so the shard slice is deterministic despite the map input.
	for _, project := range slices.Sorted(maps.Keys(in.Symbols)) {
		if s := assembleSymbols(project, in.Symbols[project], in.Graph.Projects); len(s.Nodes) > 0 {
			for _, n := range s.Nodes {
				if n.Kind == types.KindFile {
					pathToNode[n.Source] = n.ID
				}
			}
			shards = append(shards, s)
		}
	}
	// The build I/O layer: produces/consumes edges from each target's declared outputs and
	// inputs to the file and doc nodes they match. Runs last, so every path-bearing node
	// (docs, buzz, symbols) is known; it links only existing nodes, never a phantom.
	if io := assembleIO(in.Graph.Projects, pathToNode); len(io.Edges) > 0 {
		shards = append(shards, io)
	}
	// The observed coverage overlay: a single isolated shard folding a coverage ratio
	// onto the file/symbol nodes above. Lazily loaded (its targets are), so an empty
	// shard is dropped rather than persisted.
	if c := assembleCoverage(in.Coverage, in.Symbols); len(c.Nodes) > 0 {
		shards = append(shards, c)
	}
	// Directory aggregates: roll up file count, summed churn, and languages onto each
	// dir node from every path-bearing leaf (pathToNode). Runs last so it sees every
	// file/doc/symbol path across the shards above; its dir attrs fold onto the
	// structural dir nodes containsChain emitted in those shards.
	if len(pathToNode) > 0 {
		churnByPath := make(map[string]int, len(in.VCS))
		for _, e := range in.VCS {
			churnByPath[e.Path] = e.Commits
		}
		if d := assembleDirs(in.Graph.Projects, slices.Sorted(maps.Keys(pathToNode)), churnByPath); len(d.Nodes) > 0 {
			shards = append(shards, d)
		}
	}
	return shards
}

// containsChain builds the directory containment tree from a project down to a
// path-bearing leaf node (a file or doc), returning a KindDir node for each directory
// between the project root and the leaf plus the chain of `contains` edges:
// project -> topdir -> ... -> parentdir -> leaf. It replaces a single flat
// project -> leaf edge so a directory (a subsystem/package) is a first-class node -
// the granularity agent memory anchors to and dir-level coupling/churn reads against.
//
// Dir nodes and edges dedup across shards on merge (a directory holds files from the
// buzz, docs, and symbols shards, each of which emits the same chain for its own
// files). A leaf sitting directly in the project root yields just project -> leaf, as
// before. Paths are workspace-relative and slash-separated, so path (not filepath) is
// the right splitter regardless of host OS.
func containsChain(projectPath, leafPath, leafID string) ([]types.KnowledgeNode, []types.KnowledgeEdge) {
	var dirs []string // deepest-first: the directories strictly between the project and the leaf
	for d := path.Dir(leafPath); d != "." && d != "/" && d != "" && d != projectPath; d = path.Dir(d) {
		dirs = append(dirs, d)
	}
	slices.Reverse(dirs) // chain shallow -> deep

	parent := projectID(projectPath)
	nodes := make([]types.KnowledgeNode, 0, len(dirs))
	edges := make([]types.KnowledgeEdge, 0, len(dirs)+1)
	for _, d := range dirs {
		dID := dirID(d)
		nodes = append(nodes, types.KnowledgeNode{ID: dID, Kind: types.KindDir, Label: d, Source: d})
		edges = append(edges, extractedEdge(parent, dID, types.RelationContains, d))
		parent = dID
	}
	edges = append(edges, extractedEdge(parent, leafID, types.RelationContains, leafPath))
	return nodes, edges
}

// owningProjectPath returns the path of the project that contains file: the longest
// project path that is a path-prefix of the file, or ok=false when no project claims
// it. The workspace-root project (".") owns any file not under a more specific nested
// project, so the longest-match rule keeps a file with the closest project.
func owningProjectPath(file string, projects []types.TargetGraphProject) (string, bool) {
	best, found := "", false
	for _, p := range projects {
		if !projectContainsFile(p.Path, file) {
			continue
		}
		if !found || len(p.Path) > len(best) {
			best, found = p.Path, true
		}
	}
	return best, found
}

// projectContainsFile reports whether the project at projectPath owns file. The root
// project (".") owns everything; any other project owns a file equal to its path or
// beneath it. The trailing "/" guard stops "foo" from claiming "foobar/x".
func projectContainsFile(projectPath, file string) bool {
	return projectPath == "." || file == projectPath || strings.HasPrefix(file, projectPath+"/")
}

// knownTargetIDs collects every target node ID the project shards will define, so
// the runtime shard can drop timings for targets no longer in any magusfile.
func knownTargetIDs(g types.TargetGraphOutput) map[string]bool {
	ids := map[string]bool{}
	for _, p := range g.Projects {
		for _, n := range p.Nodes {
			ids[targetID(p.Path, n.Name)] = true
		}
	}
	return ids
}

// assembleRegistry builds the workspace-independent shard: spell/op nodes with
// their contains edges, module/method nodes with theirs, and diagnostic nodes
// carrying their doc URL.
func assembleRegistry(in Inputs) Shard {
	var s Shard
	s.Name = RegistryShardName

	toolSeen := map[string]bool{}      // tool nodes minted once per registry shard
	spellToolSeen := map[string]bool{} // spell->tool edges deduped per (spell, tool)
	for _, sp := range in.Spells.Spells {
		sID := spellID(sp.Name)
		spellAttrs := map[string]string{}
		if sp.Language != "" {
			// Tag the adapter with the language it builds, so `language:go` reaches the
			// go spell alongside the Go files and symbols it governs - the same attr
			// key the file/symbol nodes carry.
			spellAttrs["language"] = sp.Language
		}
		if in.DeclaredSpells[sp.Name] {
			// A workspace project declares this spell, so an unused one is genuinely dead
			// (the orphan lens flags it); a compiled-in builtin no project declares is
			// merely available and never flagged.
			spellAttrs[AttrDeclared] = "true"
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    sID,
			Kind:  types.KindSpell,
			Label: sp.Name,
			Attrs: nilIfEmpty(spellAttrs),
		})
		for _, op := range sp.Targets {
			oID := opID(sp.Name, op)
			// The base argv this op runs (empty charms), when statically knowable. A
			// function-op contributes no entry, so it carries no argv and links to no
			// tool - the "not statically knowable" boundary the command kind used to draw.
			argv := sp.OpCommands[op]
			var opAttrs map[string]string
			var toolName string
			if len(argv) > 0 {
				toolName = filepath.Base(argv[0])
				opAttrs = map[string]string{
					AttrArgv: sanitize(strings.Join(argv, " "), maxLabelLen),
					AttrTool: sanitize(toolName, maxLabelLen),
				}
			}
			s.Nodes = append(s.Nodes, types.KnowledgeNode{
				ID:    oID,
				Kind:  types.KindOp,
				Label: op,
				Doc:   sp.TargetDocs[op],
				Attrs: opAttrs,
			})
			s.Edges = append(s.Edges, extractedEdge(sID, oID, types.RelationContains, ""))
			if toolName == "" {
				continue
			}
			// The tool the op runs: one node per distinct argv[0] basename, its own `tool`
			// kind (a program is an entity, not an operation). op->tool makes `explain
			// tool:go` reach every op that runs go; a target reaches its tool via target->op.
			tID := toolID(toolName)
			if !toolSeen[tID] {
				toolSeen[tID] = true
				s.Nodes = append(s.Nodes, types.KnowledgeNode{
					ID:    tID,
					Kind:  types.KindTool,
					Label: sanitize(toolName, maxLabelLen),
					Attrs: map[string]string{AttrTool: sanitize(toolName, maxLabelLen)},
				})
			}
			s.Edges = append(s.Edges, extractedEdge(oID, tID, types.RelationUses, ""))
			// The spell runs the tool too - conveys the spell<->tool relationship directly
			// (spell:go uses tool:go), so `explain tool:go` shows its ops and its spell.
			// Deduped per (spell, tool).
			if key := sp.Name + "\x00" + toolName; !spellToolSeen[key] {
				spellToolSeen[key] = true
				s.Edges = append(s.Edges, extractedEdge(sID, tID, types.RelationUses, ""))
			}
		}
	}

	for _, m := range in.Modules {
		mID := moduleID(m.Name)
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    mID,
			Kind:  types.KindModule,
			Label: m.Name,
			Doc:   m.Doc,
		})
		for _, meth := range m.Methods {
			methID := methodID(m.Name, meth.Name)
			attrs := map[string]string{}
			if meth.Buzz != "" {
				attrs["buzz"] = meth.Buzz
			}
			s.Nodes = append(s.Nodes, types.KnowledgeNode{
				ID:    methID,
				Kind:  types.KindMethod,
				Label: m.Name + "." + meth.Name,
				Doc:   meth.Doc,
				Attrs: nilIfEmpty(attrs),
			})
			s.Edges = append(s.Edges, extractedEdge(mID, methID, types.RelationContains, ""))
		}
	}

	for _, code := range in.Diagnostics {
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    diagnosticID(string(code)),
			Kind:  types.KindDiagnostic,
			Label: string(code),
			Attrs: map[string]string{"url": types.CodeURL(code)},
		})
	}

	return s
}

// assembleProject builds one project's shard: the project node, its targets and
// contains edges, target->target dependencies (intra- and cross-project),
// target->op uses edges, charm->target references, and project->project deps. What a
// target runs is reached via its target->op edge; the op carries the argv and tool.
func assembleProject(p types.TargetGraphProject) Shard {
	s := Shard{Name: p.Path}
	pID := projectID(p.Path)
	// target_count is always present, so projAttrs is never empty and needs no
	// nilIfEmpty guard (unlike the per-target attrs below, which are engine-only).
	projAttrs := map[string]string{AttrTargetCount: strconv.Itoa(len(p.Nodes))}
	if p.Engine != "" {
		projAttrs[AttrEngine] = p.Engine
	}
	// Display the resolved name, not the raw path: the workspace-root project's Path
	// is ".", which reads as a bare dot in the graph. RelPath carries the normalized
	// label (types.ProjectLabel collapses "." to the workspace name, e.g. "magus");
	// fall back to Path when it is unset (outside a repo). The ID and Source stay keyed
	// on Path so edges and source links are unaffected.
	label := p.Path
	if p.RelPath != "" {
		label = p.RelPath
	}
	s.Nodes = append(s.Nodes, types.KnowledgeNode{
		ID:     pID,
		Kind:   types.KindProject,
		Label:  label,
		Source: p.Path,
		Attrs:  projAttrs,
	})
	for _, dep := range p.DependsOn {
		s.Edges = append(s.Edges, extractedEdge(pID, projectID(dep), types.RelationDependsOn, p.Path))
	}

	for _, n := range p.Nodes {
		tID := targetID(p.Path, n.Name)
		var tAttrs map[string]string
		if p.Engine != "" {
			tAttrs = map[string]string{AttrEngine: p.Engine}
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:     tID,
			Kind:   types.KindTarget,
			Label:  n.Name,
			Doc:    n.Doc,
			Source: p.Path,
			Attrs:  tAttrs,
		})
		s.Edges = append(s.Edges, extractedEdge(pID, tID, types.RelationContains, p.Path))

		for _, dep := range n.Dependencies {
			s.Edges = append(s.Edges, extractedEdge(tID, targetID(p.Path, dep), types.RelationDependsOn, p.Path))
		}
		for _, cd := range n.CrossDependencies {
			s.Edges = append(s.Edges, extractedEdge(tID, targetID(cd.Project, cd.Target), types.RelationDependsOn, p.Path))
		}
		for _, su := range n.Spells {
			for _, op := range su.Ops {
				oID := opID(su.Spell, op)
				// Emit a minimal op node too, so a target using an op the registry
				// shard did not declare (alias handle, workspace spell) never leaves
				// a dangling edge; it dedups against the registry's richer node.
				s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: oID, Kind: types.KindOp, Label: op})
				s.Edges = append(s.Edges, extractedEdge(tID, oID, types.RelationUses, p.Path))
			}
		}
		for _, c := range n.Charms {
			cID := charmID(c)
			s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: cID, Kind: types.KindCharm, Label: c})
			// Direction per plan: a charm references the targets that declare it, so
			// its out-degree is its fan-out (the charm-fan-out metric, later phase).
			s.Edges = append(s.Edges, extractedEdge(cID, tID, types.RelationReferences, p.Path))
		}
	}
	return s
}

// extractedEdge builds a directly-observed edge (confidence extracted, score 1.0).
func extractedEdge(source, target, relation, provenance string) types.KnowledgeEdge {
	return types.KnowledgeEdge{
		Source:     source,
		Target:     target,
		Relation:   relation,
		Confidence: types.ConfidenceExtracted,
		Score:      1.0,
		Provenance: provenance,
	}
}

// inferredEdge builds a rubric-inferred edge (confidence inferred, sub-1.0 score)
// for fuzzy evidence such as an in-body doc mention or an unresolved buzz import.
func inferredEdge(source, target, relation, provenance string, score float64) types.KnowledgeEdge {
	return types.KnowledgeEdge{
		Source:     source,
		Target:     target,
		Relation:   relation,
		Confidence: types.ConfidenceInferred,
		Score:      score,
		Provenance: provenance,
	}
}

// nilIfEmpty returns m, or nil when m has no entries, so an empty Attrs map
// serializes as absent (omitempty) rather than {}.
func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// The runtime shard carries the graph's non-deterministic inputs, all derived from
// local run records (see the persistence half in store.go), not workspace sources,
// so they are isolated in a dedicated @runtime shard and excluded from remote
// export: "emits" edges from a unit to each MGS code it tripped in actual runs
// ("what has this target tripped" - history the static "documents" edge cannot),
// observed performance attrs (p75 duration, cache hit rate) on target nodes, and the
// last captured-output ref plus its outcome (last_output_ref / last_run_ok) so an agent
// can hop from a target to its last output.

// RuntimeShardName is the isolated shard holding runtime "emits" edges; the leading
// "@" keeps it clear of any project path and is the remote-export exclusion key.
const RuntimeShardName = "@runtime"

// assembleRuntime builds the isolated shard from the non-deterministic inputs:
// one "emits" edge per (unit, code) from the target/project node to the diagnostic
// node, a partial target node per timing carrying observed performance attrs, and a
// partial target node per output ref carrying the last-output attrs. All connect to
// nodes the registry and project shards define; timings and refs for a target no longer
// in known are dropped so stale history never adds a phantom node.
func assembleRuntime(events []types.DiagnosticEvent, timings []types.KnowledgeTiming, refs []types.KnowledgeOutputRef, known map[string]bool) Shard {
	s := Shard{Name: RuntimeShardName}
	seen := map[string]bool{}
	for _, ev := range events {
		unit := runtimeUnitID(ev.Unit)
		if unit == "" || ev.Code == "" {
			continue
		}
		diag := diagnosticID(string(ev.Code))
		key := unit + "\x00" + diag
		if seen[key] {
			continue
		}
		seen[key] = true
		s.Edges = append(s.Edges, extractedEdge(unit, diag, types.RelationEmits, "runtime"))
	}
	for _, t := range timings {
		tID := targetID(t.Project, t.Target)
		if !known[tID] {
			continue
		}
		attrs := timingAttrs(t)
		if len(attrs) == 0 {
			continue
		}
		// A typed partial node so the merge is order-independent: whichever shard
		// loads first, the project shard fills Doc/Source and these attrs merge in.
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    tID,
			Kind:  types.KindTarget,
			Label: t.Target,
			Attrs: attrs,
		})
	}
	for _, r := range refs {
		if r.Ref == "" {
			continue // no ref minted -> no attr; never an empty last_output_ref
		}
		tID := targetID(r.Project, r.Target)
		if !known[tID] {
			continue
		}
		// Same typed-partial-node merge as timings: a target with both timing and a ref
		// yields two partial nodes whose attrs fold together onto the project shard's node.
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    tID,
			Kind:  types.KindTarget,
			Label: r.Target,
			Attrs: map[string]string{
				AttrLastOutputRef: r.Ref,
				AttrLastRunOK:     strconv.FormatBool(r.OK),
			},
		})
	}
	return s
}

// timingAttrs renders the observed attrs that a timing actually backs: the p75
// duration only when a settled sample count supports it, the hit rate only when
// any hit/miss was observed. An empty map means "no signal", so no node is emitted.
func timingAttrs(t types.KnowledgeTiming) map[string]string {
	attrs := map[string]string{}
	if t.P75Ms > 0 && t.Samples > 0 {
		attrs[AttrDurationP75Ms] = strconv.FormatInt(t.P75Ms, 10)
		attrs[AttrRunSamples] = strconv.Itoa(t.Samples)
	}
	if t.HitRateSamples > 0 {
		attrs[AttrCacheHitRate] = strconv.FormatFloat(t.HitRate, 'f', 2, 64)
	}
	return attrs
}

// runtimeUnitID maps a DiagnosticEvent.Unit to a node ID: "<project>:<target>" for
// a target-scoped diagnostic or "<project>" for a project-scoped one. Project paths
// carry no colon, so a single colon marks the target boundary.
func runtimeUnitID(unit string) string {
	if unit == "" {
		return ""
	}
	if i := strings.IndexByte(unit, ':'); i > 0 {
		return targetID(unit[:i], unit[i+1:])
	}
	return projectID(unit)
}

// IsRuntimeShard reports whether name is the isolated runtime shard, excluded from
// remote export (local run history, not shareable derived data).
func IsRuntimeShard(name string) bool { return name == RuntimeShardName }
