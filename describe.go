package magus

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// DescribeSpells returns the catalog of registered spells, sorted by name.
func (*Magus) DescribeSpells() types.SpellsOutput {
	all := project.DefaultSpellRegistry().All()
	entries := make([]types.SpellEntry, 0, len(all))
	for _, p := range all {
		var docs map[string]string
		var opCommands map[string][]string
		for _, t := range p.Targets() {
			if d := p.TargetDoc(t); d != "" {
				if docs == nil {
					docs = map[string]string{}
				}
				docs[t] = d
			}
			// Render the op's base command (empty charms). ok is false for a function-op
			// (no static renderer), which simply contributes no entry - exactly the ops
			// whose argv is not statically knowable.
			if cmd, args, ok, err := p.RenderCommand(t, nil); ok && err == nil && cmd != "" {
				if opCommands == nil {
					opCommands = map[string][]string{}
				}
				opCommands[t] = append([]string{cmd}, args...)
			}
		}
		entries = append(entries, types.SpellEntry{
			Name:       p.Name(),
			Sources:    p.Sources(),
			Outputs:    p.Outputs(),
			Claims:     p.Claims(),
			Targets:    p.Targets(),
			Opaque:     p.Opaque(),
			Language:   p.Language(),
			TargetDocs: docs,
			OpCommands: opCommands,
		})
	}
	slices.SortFunc(entries, func(a, b types.SpellEntry) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return types.SpellsOutput{
		Definition: types.SpellDefinition,
		Count:      len(entries),
		Spells:     entries,
	}
}

// DescribeCharms builds the inverse charm index: every charm name a target in the
// workspace declares, plus the reserved built-ins and any workspace default, and for
// each the project/target/spell declarations that give it a patch. defaults is the
// workspace default_charms set, so the report can mark which charms apply to every
// run without a :suffix. It is the transpose of DescribeTarget: one charm, every
// target that declares it, rather than one target and the charms it declares.
func (m *Magus) DescribeCharms(defaults []string) types.CharmsOutput {
	defaultSet := map[string]struct{}{}
	for _, c := range defaults {
		defaultSet[types.NormalizeCharmName(c)] = struct{}{}
	}

	byName := map[string]*types.CharmEntry{}
	ensure := func(name string) *types.CharmEntry {
		e, ok := byName[name]
		if !ok {
			_, isDefault := defaultSet[name]
			e = &types.CharmEntry{
				Name:    name,
				Builtin: types.IsReservedCharm(name),
				Default: isDefault,
				Doc:     types.ReservedCharmDoc(name),
			}
			byName[name] = e
		}
		return e
	}

	// The reserved built-ins are vocabulary even where no target declares a patch for
	// them; a workspace default that isn't reserved is real vocabulary too.
	for _, name := range types.ReservedCharms() {
		ensure(name)
	}
	for name := range defaultSet {
		ensure(name)
	}

	for _, p := range m.ws.All() {
		for _, s := range p.ResolvedSpells {
			for _, target := range s.Targets() {
				for _, c := range s.Charms(target) {
					name := types.NormalizeCharmName(c)
					decl := types.CharmDeclaration{Project: p.Path, Target: target, Spell: s.Name()}
					// Render base -> +charm so the report shows the patch's effect on this
					// target legibly rather than raw RFC 6902 ops. A charm that changes
					// nothing leaves Before == After (a no-op declaration).
					if steps, ok, err := s.ExplainCommand(target, []string{name}); err == nil && ok && len(steps) > 0 {
						decl.Before = steps[0].Command
						decl.After = steps[len(steps)-1].Command
					}
					e := ensure(name)
					e.Declarations = append(e.Declarations, decl)
				}
			}
		}
	}

	entries := make([]types.CharmEntry, 0, len(byName))
	for _, e := range byName {
		slices.SortFunc(e.Declarations, func(a, b types.CharmDeclaration) int {
			if c := cmp.Compare(a.Project, b.Project); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Target, b.Target); c != 0 {
				return c
			}
			return cmp.Compare(a.Spell, b.Spell)
		})
		entries = append(entries, *e)
	}
	slices.SortFunc(entries, func(a, b types.CharmEntry) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return types.CharmsOutput{
		Definition: types.CharmDefinition,
		Count:      len(entries),
		Charms:     entries,
	}
}

// DescribeTargets enumerates targets known in the workspace.
func (m *Magus) DescribeTargets() types.TargetsOutput {
	type targetInfo struct {
		spells   []string
		projects []string
		kind     string
	}
	byName := map[string]*targetInfo{}

	byName[types.TargetCI] = &targetInfo{kind: "canonical"}

	spellInUse := map[string]bool{}
	for _, p := range m.ws.All() {
		for _, bp := range p.Spells {
			spellInUse[bp] = true
		}
		if p.Spell != "" {
			spellInUse[p.Spell] = true
		}
	}
	for _, spell := range project.DefaultSpellRegistry().All() {
		if !spellInUse[spell.Name()] {
			continue
		}
		for _, target := range spell.Targets() {
			if _, ok := byName[target]; !ok {
				byName[target] = &targetInfo{kind: "spell"}
			}
			byName[target].spells = appendUniq(byName[target].spells, spell.Name())
		}
	}

	for _, p := range m.ws.All() {
		for target := range p.TargetPolicies {
			if _, ok := byName[target]; !ok {
				byName[target] = &targetInfo{kind: "custom"}
			}
			byName[target].projects = appendUniq(byName[target].projects, p.Path)
		}
	}

	entries := make([]types.TargetEntry, 0, len(byName))
	for name, info := range byName {
		e := types.TargetEntry{
			Name:     name,
			Kind:     info.kind,
			Spells:   info.spells,
			Projects: info.projects,
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b types.TargetEntry) int {
		if a.Kind == "canonical" && b.Kind != "canonical" {
			return -1
		}
		if b.Kind == "canonical" && a.Kind != "canonical" {
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return types.TargetsOutput{
		Definition: types.TargetDefinition,
		Count:      len(entries),
		Targets:    entries,
	}
}

// gitRoot returns the nearest ancestor of dir (inclusive) holding a `.git` entry,
// or "" if none. A lightweight walk rather than a `git` exec: DescribeGraph has no
// context to run a command under, and all it needs is the directory to render a
// project's path relative to. The `.git` entry is a directory in a normal clone
// and a file in a worktree or submodule, so a bare existence check covers both.
func gitRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// applyTargetDepsAndFootprint folds the two things magus recovers statically from a
// target body into the workspace's projects: cross-project dependencies and the
// per-target cache footprint (magus.inputs / magus.outputs).
//
// Cross-project deps (project imports) union into DependsOn, so the affected set and
// scheduling treat them exactly like a project-level depends_on: a magusfile declares a
// cross-project dependency once, at the target, rather than also in magus.project.
// Per-target inputs populate TargetInputs in one representation (each InputRef resolved
// to its owning project's workspace-relative path); outputs populate TargetOutputs
// (project-root relative). Both add to that target's cache/snapshot footprint, unioned
// onto the project-wide globs, never replacing them. A cross-project input's owning
// project is also unioned into DependsOn, so an input change marks the consumer affected
// exactly like a project-level depends_on; a same-project input's owning project is this
// project itself, which is skipped (a project cannot depend on itself, and it already
// seeds by directory containment).
//
// It mutates projects in place. ctx is honored between projects. A project whose source
// can't be read or whose dep path won't resolve contributes nothing (best-effort,
// matching the static extractor's never-error contract). One deliberate exception:
// a magus.inputs/outputs call with a non-literal argument is a hard load error, because
// a computed footprint is invisible to this static read and silently under-declaring it
// risks a stale cache hit.
// nodesWithDiscovery returns src's combined target graph nodes: the old-form nodes
// describe.Extract reads statically, plus the ctx-form nodes interp.DiscoverCtxNodes
// learns by running each ctx-form target under discovery. Both the cache-footprint
// path (applyTargetDepsAndFootprint) and the graph render (DescribeGraph) read
// through here so a ctx-form target's inputs/outputs/cross-deps reach the cache key
// and affected-tracking, not only MAGUS.md.
//
// Discovery RUNS the target bodies, so it needs the Buzz interpreter linked. A bare
// library caller that does not link it (interp.Available() == false; see doc.go) gets
// only the static old-form nodes here - the same graceful degradation validateTargetPolicies
// and magus.project() evaluation take when the interpreter is absent. Best-effort even
// when linked: a discovery failure logs and yields just the old-form nodes, so a
// discovery bug degrades that project's footprint/graph rather than failing the load.
// It also returns the per-target execution policy the ctx-form targets declared
// (ctx.skip_cache/exclusive/slots), for the footprint path to fold into
// Project.TargetPolicies; the graph path ignores it. Nil when the interpreter is not
// linked or discovery failed.
func nodesWithDiscovery(ctx context.Context, src *interp.Source, projectPath string) ([]types.TargetGraphNode, map[string]types.Target) {
	nodes := describe.Extract(concatSource(src))
	if !interp.Available() {
		return nodes, nil
	}
	dnodes, policies, derr := interp.DiscoverCtxNodes(ctx, src)
	if derr != nil {
		slog.Warn("magus: ctx-form target discovery failed; graph omits its ctx-form targets",
			slog.String("project", projectPath), slog.String("error", derr.Error()))
		return nodes, nil
	}
	return mergeTargetNodes(nodes, dnodes), policies
}

// mergeTargetNodes unifies the static (describe.Extract) and discovered
// (interp.DiscoverCtxNodes) node sets into ONE node per target, keyed by normalized
// name. A ctx-form target appears in BOTH sets and they are COMPLEMENTARY: the static
// read sees its spell ops (go["go-build"]) but not its ctx.needs/inputs (wrong
// receiver), while discovery sees its deps/inputs/outputs/charms/policy but not the
// spell ops. So the two are field-unioned rather than one shadowing the other (or the
// renderer silently dropping a duplicate). An old-form target appears only in the
// static set, a pure-ctx target only in the discovered set. Static (source) order is
// preserved; a discovered-only node is appended.
func mergeTargetNodes(static, discovered []types.TargetGraphNode) []types.TargetGraphNode {
	out := make([]types.TargetGraphNode, len(static))
	copy(out, static)
	idx := make(map[string]int, len(out))
	for i := range out {
		idx[out[i].Name] = i
	}
	for _, d := range discovered {
		if i, ok := idx[d.Name]; ok {
			out[i] = unionTargetNode(out[i], d)
			continue
		}
		idx[d.Name] = len(out)
		out = append(out, d)
	}
	return out
}

// unionTargetNode merges b into a field by field: the slice fields are deduped-unioned,
// Doc and Spells are taken from whichever node carries them (they never conflict - one
// side is always empty for a given target), and DynamicIO is ORed.
func unionTargetNode(a, b types.TargetGraphNode) types.TargetGraphNode {
	if a.Doc == "" {
		a.Doc = b.Doc
	}
	for _, dep := range b.Dependencies {
		a.Dependencies = appendUniq(a.Dependencies, dep)
	}
	for _, cd := range b.CrossDependencies {
		if !slices.Contains(a.CrossDependencies, cd) {
			a.CrossDependencies = append(a.CrossDependencies, cd)
		}
	}
	for _, ch := range b.Charms {
		a.Charms = appendUniq(a.Charms, ch)
	}
	for _, in := range b.Inputs {
		if !slices.Contains(a.Inputs, in) {
			a.Inputs = append(a.Inputs, in)
		}
	}
	for _, o := range b.Outputs {
		a.Outputs = appendUniq(a.Outputs, o)
	}
	if len(a.Spells) == 0 {
		a.Spells = b.Spells
	}
	a.DynamicIO = a.DynamicIO || b.DynamicIO
	return a
}

func (m *Magus) applyTargetDepsAndFootprint(ctx context.Context) error {
	for _, p := range m.ws.All() {
		if err := ctx.Err(); err != nil {
			return err
		}
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			continue
		}
		var extra []string
		for _, src := range srcs {
			if src.Engine != "buzz" {
				continue
			}
			nodes, policies := nodesWithDiscovery(ctx, src, p.Path)
			for _, n := range nodes {
				for _, ref := range n.CrossDependencies {
					// Skip a self-resolving import (r == p.Path): a self-edge is both
					// unnecessary and rejected by the depgraph as a self-loop - same guard
					// the input loop below applies.
					if r, err := file.Resolve(ref.Project, p.Path); err == nil && r != p.Path {
						extra = append(extra, r)
					}
				}
				if n.DynamicIO {
					return fmt.Errorf("%s: target %q: magus.inputs/outputs requires string-literal globs; a computed argument is invisible to the cache and would risk a stale hit", types.ProjectLabel(p.Path, p.Dir), n.Name)
				}
				// Every input, same-project or cross, flows through one loop. Resolve each
				// to its owning project's workspace-relative path (a bare-literal glob's
				// owner is this project; a <alias>.file cross ref's owner is file.Resolve of
				// the raw import path), then store the resolved InputRef. buildStep folds it
				// to the cache key via joinGlob(Project, Glob). A cross
				// input's owner is also unioned into DependsOn so a change to it marks this
				// project affected (project.Affected is a DependsOn-reverse-closure); a
				// same-project owner is this project itself and is skipped - a self-edge is
				// both unnecessary (it seeds by directory containment) and rejected by the
				// depgraph as a self-loop.
				for _, ref := range n.Inputs {
					owner := ref.Project
					if owner == "" {
						owner = p.Path // same-project input: owned by this project
					} else if r, rerr := file.Resolve(ref.Project, p.Path); rerr == nil {
						owner = r
					} else {
						continue // unresolvable cross ref: drop (best-effort)
					}
					if p.TargetInputs == nil {
						p.TargetInputs = map[string][]types.InputRef{}
					}
					resolved := types.InputRef{Project: owner, Glob: ref.Glob}
					if !slices.Contains(p.TargetInputs[n.Name], resolved) {
						p.TargetInputs[n.Name] = append(p.TargetInputs[n.Name], resolved)
					}
					if owner != p.Path {
						extra = append(extra, owner)
					}
				}
				for _, g := range n.Outputs {
					if p.TargetOutputs == nil {
						p.TargetOutputs = map[string][]string{}
					}
					p.TargetOutputs[n.Name] = appendUniq(p.TargetOutputs[n.Name], g)
				}
			}
			// A ctx-form target declares run policy through ctx.skip_cache/exclusive/slots;
			// fold it into the same TargetPolicies map the old global-magus form populates,
			// composing with any existing entry rather than replacing it.
			for name, ctxPol := range policies {
				if p.TargetPolicies == nil {
					p.TargetPolicies = map[string]types.Target{}
				}
				pol := p.TargetPolicies[name]
				pol.SkipCache = pol.SkipCache || ctxPol.SkipCache
				pol.Exclusive = pol.Exclusive || ctxPol.Exclusive
				if ctxPol.Slots != 0 {
					pol.Slots = ctxPol.Slots
				}
				p.TargetPolicies[name] = pol
			}
		}
		if len(extra) > 0 {
			p.DependsOn = append(p.DependsOn, extra...)
			slices.Sort(p.DependsOn)
			p.DependsOn = slices.Compact(p.DependsOn)
		}
	}
	return nil
}

// concatSource reads a project source's files in load order into one string for the
// static extractor, skipping any file that can't be read (best-effort).
func concatSource(src *interp.Source) string {
	var sb strings.Builder
	for _, f := range src.Files {
		if b, err := os.ReadFile(f); err == nil {
			sb.Write(b)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// DescribeGraph returns the target dependency graph of each project, extracted
// statically from its magusfile (no target body is evaluated). Buzz magusfiles
// are supported; a project on any other engine yields an engine-tagged entry
// with no nodes until that extractor lands.
func (m *Magus) DescribeGraph() types.TargetGraphOutput {
	out := types.TargetGraphOutput{Definition: types.TargetGraphDefinition}
	repoRoot := gitRoot(m.ws.Root) // "" outside a repo; drives the repo-relative MAGUS.md heading
	for _, p := range m.ws.All() {
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			continue // best-effort introspection: a project we can't read just omits its graph
		}
		for _, src := range srcs {
			entry := types.TargetGraphProject{Path: p.Path, Engine: src.Engine, DependsOn: p.DependsOn}
			if repoRoot != "" {
				if rel, err := filepath.Rel(repoRoot, p.Dir); err == nil {
					entry.RelPath = filepath.ToSlash(rel)
				}
			}
			// The workspace-root project's path is ".", which would render as the
			// ambiguous "## Project: ." heading; types.ProjectLabel collapses it to the
			// workspace directory name (e.g. "magus"). A non-root RelPath is kept as-is.
			entry.RelPath = types.ProjectLabel(entry.RelPath, p.Dir)
			if src.Engine == "buzz" {
				nodes, _ := nodesWithDiscovery(context.Background(), src, p.Path)
				resolveNodeRefs(nodes, p.Path)
				entry.Nodes = nodes
				entry.Cycle = describe.Cycle(nodes)
			}
			out.Projects = append(out.Projects, entry)
		}
	}
	return out
}

// resolveNodeRefs rewrites each node's cross-project dependency paths and its
// input owning-project paths from the form written in the magusfile to the
// workspace-relative path the rest of the graph keys projects by — the same resolution
// WithDependsOn does for project-level deps. For inputs: a same-project entry (empty
// Project) takes this project's path; a cross-project entry resolves its raw import path.
// Resolving here lets assembleIO link every consumes edge by path.Join(Project, Rel)
// against the file node in the owning project directly, without re-anchoring. An
// unresolvable path is dropped (best-effort, matching the static extractor's never-error
// contract).
func resolveNodeRefs(nodes []types.TargetGraphNode, projectPath string) {
	for i := range nodes {
		if len(nodes[i].CrossDependencies) > 0 {
			resolved := make([]types.CrossTargetRef, 0, len(nodes[i].CrossDependencies))
			for _, ref := range nodes[i].CrossDependencies {
				r, err := file.Resolve(ref.Project, projectPath)
				if err != nil {
					continue
				}
				resolved = append(resolved, types.CrossTargetRef{Project: r, Target: ref.Target})
			}
			nodes[i].CrossDependencies = resolved
		}
		if len(nodes[i].Inputs) > 0 {
			resolved := make([]types.InputRef, 0, len(nodes[i].Inputs))
			for _, ref := range nodes[i].Inputs {
				if ref.Project == "" {
					resolved = append(resolved, types.InputRef{Project: projectPath, Glob: ref.Glob})
					continue
				}
				r, err := file.Resolve(ref.Project, projectPath)
				if err != nil {
					continue
				}
				resolved = append(resolved, types.InputRef{Project: r, Glob: ref.Glob})
			}
			nodes[i].Inputs = resolved
		}
	}
}

// DescribeProjects returns the project inventory of the workspace.
func (m *Magus) DescribeProjects() types.ProjectsOutput {
	all := m.ws.All()
	entries := make([]types.ProjectEntry, 0, len(all))
	for _, p := range all {
		entries = append(entries, types.ProjectEntry{
			Path:      p.Path,
			Dir:       p.Dir,
			Spell:     p.Spell,
			Spells:    p.Spells,
			Sources:   p.Sources,
			Outputs:   p.Outputs,
			DependsOn: p.DependsOn,
			Exclusive: p.Exclusive,
		})
	}
	return types.ProjectsOutput{
		Definition: types.ProjectDefinition,
		Workspace:  m.ws.Root,
		Count:      len(entries),
		Projects:   entries,
	}
}

// DescribeWorkspaces returns the single-entry view of m's workspace. A *Magus is
// always exactly one workspace; the CLI's `describe workspaces` merges these
// across the daemon's declared roots when daemon.workspaces is set.
func (m *Magus) DescribeWorkspaces(cfg types.WorkspaceConfig) types.WorkspacesOutput {
	entry := types.WorkspaceEntry{
		Root:         m.ws.Root,
		VCSBaseRef:   m.ws.VCSOptions.BaseRef,
		CacheDir:     cfg.CacheDir,
		Concurrency:  cfg.Concurrency,
		ProjectCount: len(m.ws.All()),
	}
	return types.WorkspacesOutput{
		Definition: types.WorkspaceDefinition,
		Count:      1,
		Workspaces: []types.WorkspaceEntry{entry},
	}
}

// DescribeTarget returns the fully-evaluated dispatch plan for t.
func (m *Magus) DescribeTarget(t types.Target) (types.EvaluatedTargetsOutput, error) {
	expanded, err := m.ExpandPath(t)
	if err != nil {
		return types.EvaluatedTargetsOutput{}, err
	}

	entries := make([]types.EvaluatedTargetEntry, 0, len(expanded))
	for _, et := range expanded {
		p := m.Get(et.Path)
		if p == nil {
			continue
		}
		step := m.baseStep(p)

		spellEntries := make([]types.EvaluatedSpellEntry, 0, len(p.ResolvedSpells))
		charmSet := map[string]struct{}{}
		for i, s := range p.ResolvedSpells {
			se := types.EvaluatedSpellEntry{
				Name:            s.Name(),
				TargetSources:   s.TargetSources()[et.Name],
				EffectiveClaims: project.EffectiveClaims(p, i),
			}
			if i < len(p.Bindings) {
				se.ClaimWeight = p.Bindings[i].ClaimWeight
			}
			for _, c := range s.Charms(et.Name) {
				charmSet[c] = struct{}{}
			}
			// Render the fork command with the requested charms applied, so
			// `magus describe target lint:rw` previews exactly what would run. A
			// well-formed charm patch that does not apply to this op's argv is a
			// silent no-op at render time, so surface it (MGS6001) rather than
			// omitting the command line without explanation.
			cmd, args, ok, rerr := s.RenderCommand(et.Name, t.Charms)
			if rerr != nil {
				return types.EvaluatedTargetsOutput{}, types.DiagnosticErrorf(types.CharmPatchInvalid,
					"target %q in project %q: charm(s) %v do not apply to spell %q's command (%v)",
					et.Name, et.Path, t.Charms, s.Name(), rerr)
			}
			if ok {
				se.Command = append([]string{cmd}, args...)
			}
			// A service target is described, not just rendered: surface its readiness
			// probe, stop command, idle window, and fingerprint so the supervision plan
			// is visible before the service ever starts.
			if view, sok := s.ServiceView(et.Name); sok {
				se.Service = view
			}
			// Attach the per-charm application trace (base -> +charm -> +charm) when
			// charms are active and actually reshape the command, so `--explain` can
			// render the RFC 6902 patch as a legible before/after. A trace with only
			// the base step (no active charm touched this spell) is left off.
			if len(t.Charms) > 0 {
				if steps, sok, serr := s.ExplainCommand(et.Name, t.Charms); serr == nil && sok && len(steps) > 1 {
					se.CharmTrace = steps
				}
				// Two active charms that edit the same argument silently override one
				// another; surface the loser here so a preview catches the mistake
				// before a run does.
				if conflicts, cok, cerr := s.ConflictingCharms(et.Name, t.Charms); cerr == nil && cok && len(conflicts) > 0 {
					se.Conflicts = conflicts
				}
			}
			spellEntries = append(spellEntries, se)
		}
		var charms []string
		for c := range charmSet {
			charms = append(charms, c)
		}
		slices.Sort(charms)

		entry := types.EvaluatedTargetEntry{
			Project:   et.Path,
			Target:    et.Name,
			Dir:       p.Dir,
			Sources:   step.Sources,
			Outputs:   step.Outputs,
			DependsOn: p.DependsOn,
			Charms:    charms,
			Spells:    spellEntries,
			Exclusive: p.Exclusive,
		}
		if pol, ok := p.TargetPolicies[et.Name]; ok {
			entry.Policy = &pol
		}
		entries = append(entries, entry)
	}

	return types.EvaluatedTargetsOutput{
		Definition: types.EvaluatedTargetDefinition,
		Count:      len(entries),
		Targets:    entries,
	}, nil
}

// DescribeEvaluatedProjects returns the fully-evaluated project inventory.
func (m *Magus) DescribeEvaluatedProjects() types.EvaluatedProjectsOutput {
	all := m.ws.All()
	entries := make([]types.EvaluatedProjectEntry, 0, len(all))
	for _, p := range all {
		step := m.baseStep(p)

		spellEntries := make([]types.EvaluatedSpellEntry, 0, len(p.ResolvedSpells))
		for i, s := range p.ResolvedSpells {
			se := types.EvaluatedSpellEntry{
				Name:            s.Name(),
				EffectiveClaims: project.EffectiveClaims(p, i),
			}
			if i < len(p.Bindings) {
				se.ClaimWeight = p.Bindings[i].ClaimWeight
			}
			spellEntries = append(spellEntries, se)
		}

		entry := types.EvaluatedProjectEntry{
			Path:      p.Path,
			Dir:       p.Dir,
			Sources:   step.Sources,
			Outputs:   step.Outputs,
			DependsOn: p.DependsOn,
			Spells:    spellEntries,
			Exclusive: p.Exclusive,
		}
		if len(p.TargetPolicies) > 0 {
			entry.TargetPolicies = p.TargetPolicies
		}
		entries = append(entries, entry)
	}
	return types.EvaluatedProjectsOutput{
		Definition: types.ProjectDefinition,
		Workspace:  m.ws.Root,
		Count:      len(entries),
		Projects:   entries,
	}
}

func appendUniq(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// DescribeFiles classifies workspace-relative paths against every project's
// declared source and output globs (the same workspace-rooted globs baseStep
// feeds the cache), plus directory containment for ownership. It is pure
// declaration lookup - no target evaluation, no VCS - so it is cheap enough to
// run over a whole dirty tree. An absolute path is re-rooted onto the workspace;
// a path outside it (or matching nothing) reports as unclaimed.
func (m *Magus) DescribeFiles(paths []string) types.FilesOutput {
	all := m.ws.All()
	// Longest project path first, so nested projects claim ownership before ".".
	owners := slices.Clone(all)
	slices.SortFunc(owners, func(a, b *types.Project) int {
		if c := cmp.Compare(len(b.Path), len(a.Path)); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})

	entries := make([]types.FileEntry, 0, len(paths))
	for _, raw := range paths {
		entries = append(entries, m.describeFile(raw, all, owners))
	}
	return types.FilesOutput{
		Definition: types.FileDefinition,
		Count:      len(entries),
		Files:      entries,
	}
}

func (m *Magus) describeFile(raw string, all, owners []*types.Project) types.FileEntry {
	path := filepath.ToSlash(raw)
	if filepath.IsAbs(raw) {
		if rel, err := filepath.Rel(m.ws.Root, raw); err == nil && !strings.HasPrefix(rel, "..") {
			path = filepath.ToSlash(rel)
		}
	}
	path = strings.TrimPrefix(path, "./")

	entry := types.FileEntry{Path: path, Role: "unclaimed"}
	for _, p := range owners {
		if p.Path == "." || path == p.Path || strings.HasPrefix(path, p.Path+"/") {
			entry.Project = p.Path
			break
		}
	}
	for _, p := range all {
		step := m.baseStep(p)
		if matchAnyGlob(step.Outputs, path) {
			entry.OutputOf = append(entry.OutputOf, p.Path)
		}
		if matchAnyGlob(step.Sources, path) {
			entry.SourceOf = append(entry.SourceOf, p.Path)
		}
	}

	switch {
	case len(entry.OutputOf) > 0:
		entry.Role = "output"
		entry.Hint = "generated: never hand-edit or read its diff; change the source of truth, regenerate (magus run generate), and commit it with the source change"
	case len(entry.SourceOf) > 0:
		entry.Role = "source"
		entry.Hint = "declared source: edits invalidate the owning project's cache keys and pull it into the affected set"
	default:
		entry.Hint = "no project declares this path: it invalidates no cache key and affects no target; check your VCS ignore rules before committing it"
	}
	return entry
}

// matchAnyGlob reports whether path matches any of the workspace-rooted
// doublestar globs. Same matcher family the cache uses for these globs; an
// invalid pattern simply never matches, mirroring the cache's tolerance.
func matchAnyGlob(globs []string, path string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}
