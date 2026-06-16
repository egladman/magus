package magus

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/targetgraph"
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
			Name:           p.Name(),
			Sources:        p.Sources(),
			Outputs:        p.Outputs(),
			Claims:         p.Claims(),
			Targets:        p.Targets(),
			ForeignProcess: p.ForeignProcess(),
			TargetDocs:     docs,
		})
	}
	slices.SortFunc(entries, func(a, b types.SpellEntry) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return types.SpellsOutput{
		Definition: types.SpellDefinition,
		Count:      len(entries),
		Spells:     entries,
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
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return types.TargetsOutput{
		Definition: types.TargetDefinition,
		Count:      len(entries),
		Targets:    entries,
	}
}

// vcsRoot returns the nearest ancestor of dir (inclusive) holding a `.git` entry,
// or "" if none. A lightweight walk rather than a `git` exec: DescribeGraph has no
// context to run a command under, and all it needs is the directory to render a
// project's path relative to. The `.git` entry is a directory in a normal clone
// and a file in a worktree or submodule, so a bare existence check covers both.
func vcsRoot(dir string) string {
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

// DescribeGraph returns the target dependency graph of each project, extracted
// statically from its magusfile (no target body is evaluated). Buzz magusfiles
// are supported; a project on any other engine yields an engine-tagged entry
// with no nodes until that extractor lands.
func (m *Magus) DescribeGraph() types.TargetGraphOutput {
	out := types.TargetGraphOutput{Definition: types.TargetGraphDefinition}
	repoRoot := vcsRoot(m.ws.Root) // "" outside a repo; drives the repo-relative MAGUS.md heading
	for _, p := range m.ws.All() {
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			continue // best-effort introspection: a project we can't read just omits its graph
		}
		for _, src := range srcs {
			entry := types.TargetGraphProject{Path: p.Path, Engine: src.Engine}
			if repoRoot != "" {
				if rel, err := filepath.Rel(repoRoot, p.Dir); err == nil {
					entry.RelPath = filepath.ToSlash(rel)
				}
			}
			if src.Engine == "buzz" {
				var sb strings.Builder
				for _, f := range src.Files {
					b, err := os.ReadFile(f)
					if err != nil {
						continue
					}
					sb.Write(b)
					sb.WriteByte('\n')
				}
				nodes := targetgraph.Extract(sb.String())
				entry.Nodes = nodes
				entry.Cycle = targetgraph.Cycle(nodes)
			}
			out.Projects = append(out.Projects, entry)
		}
	}
	return out
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
		spec := m.baseSpec(p)

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
			// `magus describe target lint:rw` previews exactly what would run.
			if cmd, args, ok := s.RenderCommand(et.Name, t.Charms); ok {
				se.Command = append([]string{cmd}, args...)
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
			Sources:   spec.Sources,
			Outputs:   spec.Outputs,
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
		spec := m.baseSpec(p)

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
			Sources:   spec.Sources,
			Outputs:   spec.Outputs,
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
