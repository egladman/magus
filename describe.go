package magus

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
		for _, t := range p.Targets() {
			if d := p.TargetDoc(t); d != "" {
				if docs == nil {
					docs = map[string]string{}
				}
				docs[t] = d
			}
		}
		entries = append(entries, types.SpellEntry{
			Name:       p.Name(),
			Sources:    p.Sources(),
			Outputs:    p.Outputs(),
			Claims:     p.Claims(),
			Targets:    p.Targets(),
			Opaque:     p.Opaque(),
			TargetDocs: docs,
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

// applyCrossProjectDependencies unions each project's target-level cross-project
// dependencies (project imports, recovered statically) into its DependsOn, so
// the affected set and scheduling treat them exactly like a project-level depends_on
// — letting a magusfile declare a cross-project dependency once, at the target,
// rather than also in magus.project. It mutates the workspace's projects in
// place. ctx is honored between projects so a cancelled construction stops promptly;
// a project whose source can't be read or whose dep path won't resolve contributes
// nothing (best-effort, matching the static extractor's never-error contract).
func (m *Magus) applyCrossProjectDependencies(ctx context.Context) error {
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
			for _, n := range describe.Extract(concatSource(src)) {
				for _, ref := range n.CrossDependencies {
					if r, err := file.Resolve(ref.Project, p.Path); err == nil {
						extra = append(extra, r)
					}
				}
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
				nodes := describe.Extract(concatSource(src))
				resolveCrossDependencies(nodes, p.Path)
				entry.Nodes = nodes
				entry.Cycle = describe.Cycle(nodes)
			}
			out.Projects = append(out.Projects, entry)
		}
	}
	return out
}

// resolveCrossDependencies rewrites each node's cross-project dependency paths from the
// dot-/repo-relative form written in the magusfile to the workspace-relative path
// the rest of the graph keys projects by — the same resolution WithDependsOn does
// for project-level deps. An unresolvable path is dropped (best-effort, matching
// the static extractor's never-error contract).
func resolveCrossDependencies(nodes []types.TargetGraphNode, projectPath string) {
	for i := range nodes {
		if len(nodes[i].CrossDependencies) == 0 {
			continue
		}
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
