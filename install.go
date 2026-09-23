package magus

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// isSpellInstall reports whether target on p is an install op a spell synthesized, as
// opposed to a magusfile export that shadows the name.
func isSpellInstall(p *types.Project, target string) bool {
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
	prober     *toolProber
	revision   string
	dirty      bool
	vcsName    string
	skipReplay bool
	opts       []cache.RunOption
}

// installRunner is the types.InstallRunner one invocation installs: it runs each
// spell's install as its own cache step, so an unchanged install with its stamps in
// place replays without forking the package manager.
func (m *Magus) installRunner(k installKeying) types.InstallRunner {
	return func(ctx context.Context, dir, spellName string, choice spells.InstallChoice, run func(context.Context) error) error {
		p := m.projectByDir(dir)
		if p == nil {
			return run(ctx)
		}
		// Only the install's own tools: they are all its step keys on, and the gate
		// judges only what this install runs.
		only := func(spell, tool string) bool {
			return spell == spellName && slices.Contains(choice.Install.Tools, tool)
		}
		windows := map[string]string{}
		tv := k.prober.versions(ctx, []*types.Project{p}, only, windows)[p.Path]
		if err := checkToolWindows([]*types.Project{p}, windows); err != nil {
			return err
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

// prewarmInstallProbes starts, in the background, the version probes each spell install
// in stages will key on. The scheduler walks installs a dependency level at a time, and
// each level otherwise waits on its own probe spawns before it can replay anything.
func (m *Magus) prewarmInstallProbes(ctx context.Context, prober *toolProber, stages []stage) {
	for _, st := range stages {
		for _, p := range st.projects {
			if !spellInstall(p, st.target) {
				continue
			}
			for _, s := range p.ResolvedSpells {
				op, ok := s.Op(spells.InstallOp)
				if !ok || op.Kind != spells.OpKindInstall {
					continue
				}
				choice, found, err := spell.ResolveInstall(op.Install, p.Dir, m.ws.Root)
				if err != nil || !found {
					continue
				}
				name, tools := s.Name(), choice.Install.Tools
				go prober.versions(ctx, []*types.Project{p}, func(sp, tool string) bool {
					return sp == name && slices.Contains(tools, tool)
				}, nil)
			}
		}
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

// projectByDir returns the project whose directory is dir, or nil. Unlike Get, which
// is keyed by workspace-relative Path, this is keyed by the caller's filesystem dir, so
// it scans rather than looking up. One install runs per changed spell per project, not
// per file, so the linear scan does not show up even on a workspace with thousands of
// projects.
func (m *Magus) projectByDir(dir string) *types.Project {
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
