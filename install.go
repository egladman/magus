package magus

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// spellInstall reports whether target on p is an install op a spell synthesized, as
// opposed to a magusfile export that shadows the name.
func spellInstall(p *types.Project, target string) bool {
	if target != spells.InstallOp || magusfileOverride(p, p.ResolvedSpells, target) >= 0 {
		return false
	}
	for _, s := range p.ResolvedSpells {
		if op, ok := s.Op(target); ok && op.Kind == spells.OpKindInstall {
			return true
		}
	}
	return false
}

// installKeying is what every install step of one invocation shares with the steps the
// scheduler builds, so an install keys the same whether scheduled or composed.
type installKeying struct {
	toolVersions map[string][]string
	revision     string
	dirty        bool
	vcsName      string
	skipReplay   bool
	opts         []cache.RunOption
}

// installRunner is the types.InstallRunner one invocation installs: it runs each
// spell's install as its own cache step, so an unchanged install with its stamps in
// place replays without forking the package manager.
func (m *Magus) installRunner(k installKeying) types.InstallRunner {
	return func(ctx context.Context, dir, spellName string, choice spells.InstallChoice, run func(context.Context) error) error {
		p := m.projectAt(dir)
		if p == nil {
			return run(ctx)
		}
		tv, ok := k.toolVersions[p.Path]
		if !ok {
			tv = m.toolVersionsByProject(ctx, []*types.Project{p})[p.Path]
		}
		step := m.installStep(p, spellName, choice, tv, types.CharmsFromContext(ctx))
		step.ExtraArgs = project.ExtraArgs(ctx)
		step.Revision, step.Dirty, step.VCSName = k.revision, k.dirty, k.vcsName
		step.SkipReplay = k.skipReplay
		call := func() error {
			_, err := m.cache.RunAside(cache.WithoutSlotHeld(ctx), step, run, k.opts...)
			return err
		}
		// The caller holds a slot while it waits here, and RunAside takes its own; at a
		// budget of one, keeping both would deadlock.
		if lim := cache.LimiterFromContext(ctx); lim != nil && cache.SlotHeld(ctx) {
			return lim.Yield(ctx, call)
		}
		return call()
	}
}

// installStep keys one spell's install for p on what decides its result: the
// manifest, the live lock, the tool's settings files, the tools the install names, and
// the platform, which is forced on because a dependency tree can hold native binaries.
// The tool's completion stamps gate the replay; with none declared the install always
// runs, since nothing else could notice a deleted tree.
func (m *Magus) installStep(p *types.Project, spellName string, choice spells.InstallChoice, toolVersions, charms []string) cache.Step {
	base := m.baseStep(p)
	step := cache.Step{
		ProjectPath:     p.Path,
		Target:          spells.InstallOp,
		Spell:           spellName,
		Sources:         []string{m.workspaceRel(choice.Manifest), m.workspaceRel(choice.Lock)},
		IgnoreDirs:      base.IgnoreDirs,
		WorkspaceRoot:   m.ws.Root,
		SpellDefVersion: base.SpellDefVersion,
		Label:           base.Label,
		IncludeOS:       true,
		IncludeArch:     true,
	}
	for _, in := range choice.Install.Inputs {
		step.Sources = append(step.Sources, joinGlob(p.Path, in))
	}
	for _, s := range choice.Install.Stamps {
		step.Stamps = append(step.Stamps, joinGlob(p.Path, s))
	}
	for _, v := range toolVersions {
		for _, t := range choice.Install.Tools {
			if strings.HasPrefix(v, spellName+":"+t+":") {
				step.ToolVersions = append(step.ToolVersions, v)
			}
		}
	}
	// Only the charms the command declares: rw or gha change nothing an install does, and
	// keying on them would split one install into an entry per default-charm setting.
	for _, c := range charms {
		if _, ok := choice.Install.Command.Charms[c]; ok && !slices.Contains(step.Charms, c) {
			step.Charms = append(step.Charms, c)
		}
	}
	slices.Sort(step.Charms)
	step.NoCache = len(step.Stamps) == 0
	// update rewrites the manifest and lock by design, and replaying it would be an
	// update that never happened.
	if slices.Contains(step.Charms, types.CharmUpdate) {
		step.NoCache = true
		step.Updates = slices.Clone(step.Sources[:2])
	}
	return step
}

// projectAt returns the project whose directory is dir, or nil.
func (m *Magus) projectAt(dir string) *types.Project {
	dir = filepath.Clean(dir)
	for _, p := range m.All() {
		if filepath.Clean(p.Dir) == dir {
			return p
		}
	}
	return nil
}

// workspaceRel is abs relative to the workspace root, slash-separated like a source glob.
func (m *Magus) workspaceRel(abs string) string {
	rel, err := filepath.Rel(m.ws.Root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}
