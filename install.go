package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// isSpellInstall reports whether target on p is an install op a spell synthesized (one
// of possibly several per spell, one per binary: pnpm-install, npm-ci, ...), as opposed
// to a magusfile export that shadows the name.
func isSpellInstall(p *types.Project, target string) bool {
	if magusfileOverride(p, p.ResolvedSpells, target) >= 0 {
		return false
	}
	for _, s := range p.ResolvedSpells {
		if op, ok := s.Op(target); ok && op.Kind == spells.OpKindInstall {
			return true
		}
	}
	return false
}

// spellOp is one named op of one of a project's resolved spells.
type spellOp struct {
	spell *spells.Spell
	name  string
	op    spells.Op
}

// findInstallOps returns the spell installs target's step on p runs when running them is
// ALL the step does, and nil otherwise: the synthesized install op itself, or a magusfile
// target whose body provably only calls install ops (types.TargetGraphNode.DispatchOnly),
// the way a conventional `install` target composes its spell's.
func findInstallOps(p *types.Project, target string) []spellOp {
	if isSpellInstall(p, target) {
		var out []spellOp
		for _, s := range p.ResolvedSpells {
			if op, ok := s.Op(target); ok && op.Kind == spells.OpKindInstall {
				out = append(out, spellOp{s, target, op})
			}
		}
		return out
	}
	if !slices.Contains(p.DispatchOnlyTargets, target) {
		return nil
	}
	var out []spellOp
	for _, use := range p.TargetSpellOps[target] {
		// Spell is the import handle, which names the spell only when unaliased; an
		// aliased handle fails this lookup and the target keeps its ordinary step.
		i := slices.IndexFunc(p.ResolvedSpells, func(s *spells.Spell) bool { return s.Name() == use.Spell })
		if i < 0 {
			return nil
		}
		for _, name := range use.Ops {
			op, ok := p.ResolvedSpells[i].Op(name)
			if !ok || op.Kind != spells.OpKindInstall {
				return nil
			}
			out = append(out, spellOp{p.ResolvedSpells[i], name, op})
		}
	}
	return out
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
	return func(ctx context.Context, dir, spellName, target string, choice spells.InstallChoice, run func(context.Context) error) error {
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
		byProject, err := k.prober.probeVersions(ctx, []*types.Project{p}, only, windows)
		if err != nil {
			return err
		}
		tv := byProject[p.Path]
		if err := checkToolWindows([]*types.Project{p}, windows); err != nil {
			return err
		}
		step := m.installStep(p, spellName, target, choice, tv, types.CharmsFromContext(ctx))
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
			for _, so := range findInstallOps(p, st.target) {
				choice, found, err := spell.ResolveInstall(so.op.Install, p.Dir, m.ws.Root)
				if err != nil || !found {
					continue
				}
				name, tools := so.spell.Name(), choice.Install.Tools
				// The error is memoized with the reading; installRunner returns it.
				go func() {
					_, _ = prober.probeVersions(ctx, []*types.Project{p}, func(sp, tool string) bool {
						return sp == name && slices.Contains(tools, tool)
					}, nil)
				}()
			}
		}
	}
}

// installStep keys one spell's install for p on what decides its result: the
// manifest, the live lock, the tool's settings files, the tools the install names, and
// the platform, which is forced on because a dependency tree can hold native binaries.
// The tool's completion stamps gate the replay; with none declared the install always
// runs, since nothing else could notice a deleted tree.
func (m *Magus) installStep(p *types.Project, spellName, target string, choice spells.InstallChoice, toolVersions, charms []string) cache.Step {
	base := m.baseStep(p)
	step := cache.Step{
		ProjectPath:     p.Path,
		Target:          target,
		Spell:           spellName,
		Sources:         []types.Glob{{Pattern: m.workspaceRel(choice.Manifest)}, {Pattern: m.workspaceRel(choice.Lock)}},
		IgnoreDirs:      base.IgnoreDirs,
		WorkspaceRoot:   m.ws.Root,
		SpellDefVersion: base.SpellDefVersion,
		Label:           base.Label,
		IncludeOS:       true,
		IncludeArch:     true,
	}
	for _, in := range choice.Install.Inputs {
		step.Sources = append(step.Sources, types.Glob{Pattern: in}.Root(p.Path))
	}
	for _, s := range choice.Install.Stamps {
		step.Stamps = append(step.Stamps, types.RootGlob(p.Path, s))
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

// probeAbsence reports whether err, from probing a tool in dir, says the tool is not
// there, and if so the cause and its fix. Anything else is a tool that is present and
// failed, which probeOne refuses to key.
func (m *Magus) probeAbsence(s *spells.Spell, probe spells.Command, dir string, err error) (string, bool) {
	if errors.Is(err, types.ToolNotOnPath) {
		return probe.Bin + " is not on PATH", true
	}
	// mise's shim is on PATH whether or not this directory selects a version of the tool.
	// The cause names no directory: directories that see the same mise config share one
	// probe key, so one recorded cause answers for all of them.
	if strings.Contains(err.Error(), "No version is set for shim") {
		return fmt.Sprintf("mise selects no version of %s here: declare one in mise.toml", probe.Bin), true
	}
	spec, _ := lookupProbeSpec(probe)
	if spec.execs == "" || execPresent(spec.execs, dir) {
		return "", false
	}
	return m.missingInstall(s, spec.execs, dir), true
}

// missingInstall names why bin, which a spell's install provides, is missing from dir,
// and the command that installs it.
func (m *Magus) missingInstall(s *spells.Spell, bin, dir string) string {
	shown := m.displayDir(dir)
	stop := dir
	if m.ws != nil {
		stop = m.ws.Root
	}
	for _, name := range s.Targets() {
		op, ok := s.Op(name)
		if !ok || op.Kind != spells.OpKindInstall {
			continue
		}
		choice, found, err := spell.ResolveInstall(op.Install, dir, stop)
		if err != nil || !found {
			continue
		}
		fix := m.installCommand(dir, name)
		if _, err := os.Stat(filepath.Join(dir, choice.Install.Dir)); err != nil {
			return fmt.Sprintf("no %s in %s: run `%s`", choice.Install.Dir, shown, fix)
		}
		for _, stamp := range choice.Install.Stamps {
			if _, err := os.Stat(filepath.Join(dir, stamp)); err != nil {
				return fmt.Sprintf("the install in %s never finished (%s is missing): run `%s`", shown, stamp, fix)
			}
		}
		return fmt.Sprintf("the install in %s provides no %s: add the package that ships it to %s",
			shown, bin, filepath.Base(choice.Manifest))
	}
	return fmt.Sprintf("nothing in %s installs %s", shown, bin)
}

// installCommand is how a person installs dir's dependencies: the project's own install
// target when it has one, else the spell's install op, which runs bare.
func (m *Magus) installCommand(dir, op string) string {
	var p *types.Project
	if m.ws != nil {
		p = m.projectByDir(dir)
	}
	switch {
	case p == nil:
		return "magus run " + op
	case slices.Contains(projectTargets(p), "install"):
		return "magus run install " + p.Path
	default:
		return "magus run " + op + " " + p.Path
	}
}

// displayDir is dir as a person reads it: workspace-relative when it is inside.
func (m *Magus) displayDir(dir string) string {
	if m.ws == nil {
		return dir
	}
	if rel := m.workspaceRel(dir); !strings.HasPrefix(rel, "..") {
		return rel
	}
	return dir
}
