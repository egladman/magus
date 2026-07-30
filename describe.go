package magus

import (
	"cmp"
	"context"
	"fmt"
	"maps"
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
			Name:         p.Name(),
			Sources:      p.Sources(),
			Outputs:      p.Outputs(),
			Claims:       p.Claims(),
			Targets:      p.Targets(),
			Opaque:       p.Opaque(),
			Language:     p.Language(),
			VersionProbe: p.HasVersionProbe(),
			TargetDocs:   docs,
			OpCommands:   opCommands,
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
// run without a :suffix. It is the transpose of DescribeTarget: one charm and
// every target that declares it.
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
// reason to shell out, and all it needs is the directory to render a project's path
// relative to. The `.git` entry is a directory in a normal clone
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

// collectTargetNodes returns src's target graph nodes, read statically from the
// magusfile source by describe.Extract - the sole graph source. Both the cache-footprint
// path (applyTargetDepsAndFootprint) and the graph render (DescribeGraph) read through
// here so a target's inputs/outputs/cross-deps reach the cache key and affected-tracking,
// not only MAGUS.md. The static read is deterministic and side-effect free (it never runs
// a target body) and sees both arms of a runtime branch, which a trace could not.
func collectTargetNodes(src *interp.Source) []types.TargetGraphNode {
	return describe.Extract(concatSource(src))
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
// matching the static extractor's never-error contract). One deliberate exception: a
// ctx.inputs/outputs call with a non-literal argument is a hard load error, because a
// computed footprint is invisible to this static read and silently under-declaring it
// risks a stale cache hit.
func (m *Magus) applyTargetDepsAndFootprint(ctx context.Context) error {
	// Cross-project OUTPUT declarations, collected across the whole walk and applied
	// after it. Deferred because the owner may not be walked yet when the writer declares
	// it. Named rather than a positional pair because the direction is counterintuitive
	// and reads backwards at a glance: the OWNER (whose tree is written) gains the
	// dependency on the WRITER, so the writer runs first and the owner's cache key is
	// computed against the finished bytes.
	var crossOut []crossOutput
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
			nodes := collectTargetNodes(src)
			for _, n := range nodes {
				for _, ref := range n.CrossDependencies {
					r, rerr := file.ResolveImport(ref.Project, p.Path)
					if rerr != nil {
						// A LOAD ERROR, matching the output side below rather than the
						// input side's best-effort drop. A dropped dependency edge takes
						// this project out of the changed project's affected set, so an
						// edit upstream stops marking this project dirty and the next run
						// replays a stale cache hit. That is the output-side consequence,
						// not the input-side one, so it gets the output-side treatment.
						return types.DiagnosticErrorf(types.CrossDepOwnerUnknown,
							"%s: target %q: ctx.needs names a target in %q, which does not resolve to a path in this workspace",
							types.ProjectDisplayName(p.Path, p.Name, p.Dir), n.Name, ref.Project)
					}
					// Skip a self-resolving import (r == p.Path): a self-edge is both
					// unnecessary and rejected by the depgraph as a self-loop - same guard
					// the input loop below applies.
					if r != p.Path {
						extra = append(extra, r)
					}
				}
				// A skip_cache target has no cache key, so there is no stale hit to
				// prevent and the literal-argument rule has nothing to protect. Enforcing
				// it there made a genuinely dynamic target (release-build derives GOOS,
				// GOARCH and a cgo toolchain from its arguments and the environment)
				// inexpressible: the value cannot be a literal, and moving it back to an
				// options table is rejected too. The rule exists for the key, so it ends
				// where the key does.
				if n.DynamicIO && !p.TargetPolicies[n.Name].SkipCache {
					return fmt.Errorf("%s: target %q: ctx.inputs/outputs/updates/withEnv/withCwd take literal arguments on the target's OWN ctx; a computed value, or one reached through an alias (final e = ctx.withEnv(...); e.withCwd(..)), is invisible to the static read and would risk a stale hit", types.ProjectDisplayName(p.Path, p.Name, p.Dir), n.Name)
				}
				// Every input, same-project or cross, flows through one loop. Resolve each
				// to its owning project's workspace-relative path (a bare-literal glob's
				// owner is this project; a <alias>.file cross ref's owner is file.ResolveImport
				// of the raw import path), then store the resolved InputRef. buildStep folds it
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
					} else if r, rerr := file.ResolveImport(ref.Project, p.Path); rerr == nil {
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
				for _, ref := range n.Outputs {
					owner := ref.Project
					if owner == "" {
						owner = p.Path // same-project output: owned by this project
					} else if r, rerr := file.ResolveImport(ref.Project, p.Path); rerr == nil {
						owner = r
					} else {
						// A LOAD ERROR, not a best-effort drop as on the input side. The
						// consequences invert: dropping an unresolvable input only
						// under-declares the cache key, but dropping an output takes the
						// glob out of the snapshot set, so the file is never recorded and
						// every later cache hit replays a build that leaves it missing -
						// the exact stale-hit failure this footprint exists to prevent.
						return types.DiagnosticErrorf(types.CrossOutputOwnerUnknown,
							"%s: target %q: ctx.outputs declares an output into %q, which does not resolve to a path in this workspace",
							types.ProjectDisplayName(p.Path, p.Name, p.Dir), n.Name, ref.Project)
					}
					if p.TargetOutputs == nil {
						p.TargetOutputs = map[string][]types.OutputRef{}
					}
					resolved := types.OutputRef{Project: owner, Glob: ref.Glob}
					if !slices.Contains(p.TargetOutputs[n.Name], resolved) {
						p.TargetOutputs[n.Name] = append(p.TargetOutputs[n.Name], resolved)
					}
					// The edge runs the OTHER WAY from an input's. Writing another
					// project's tree means that project must run AFTER this one, so the
					// OWNER gains the dependency - not this project. Recorded here and
					// applied once every project is known, because the owner may not have
					// been walked yet.
					if owner != p.Path {
						// The glob half needs the same hygiene the project half just got.
						// Nothing checked it before: ctx.outputs(site.file("../../etc/x"))
						// survived extraction and resolution and surfaced only once the target
						// had already run - or, worse, made `magus clean` abort the WHOLE
						// workspace, since clean expands globs for every project in one loop
						// and doublestar rejects the pattern. The rule is the cache's own
						// (internal/cache/snapshot.go): owner-relative, no "..".
						if filepath.IsAbs(ref.Glob) || strings.Contains(ref.Glob, "..") {
							return types.DiagnosticErrorf(types.CrossOutputGlobEscapes,
								"%s: target %q: ctx.outputs glob %q must be relative to %q and must not contain ..",
								types.ProjectDisplayName(p.Path, p.Name, p.Dir), n.Name, ref.Glob, owner)
						}
						crossOut = append(crossOut, crossOutput{owner: owner, writer: p.Path, glob: ref.Glob})
					}
				}
				if len(n.EnvAllow) > 0 {
					if p.TargetEnvAllow == nil {
						p.TargetEnvAllow = map[string][]string{}
					}
					for _, e := range n.EnvAllow {
						if !slices.Contains(p.TargetEnvAllow[n.Name], e) {
							p.TargetEnvAllow[n.Name] = append(p.TargetEnvAllow[n.Name], e)
						}
					}
				}
				if len(n.ExecOverrides) > 0 {
					if p.TargetExecOverrides == nil {
						p.TargetExecOverrides = map[string][]string{}
					}
					// Append-with-dedup like the three sibling loops, not assign. A project
					// can load several sources (magusfile.buzz plus magusfiles/*.buzz), and
					// assigning meant the last source's overrides silently replaced an
					// earlier source's - dropping them from the key rather than merging.
					for _, o := range n.ExecOverrides {
						if !slices.Contains(p.TargetExecOverrides[n.Name], o) {
							p.TargetExecOverrides[n.Name] = append(p.TargetExecOverrides[n.Name], o)
						}
					}
				}
				for _, ref := range n.Updates {
					owner := ref.Project
					if owner == "" {
						owner = p.Path // same-project update: owned by this project
					} else if r, rerr := file.ResolveImport(ref.Project, p.Path); rerr == nil {
						owner = r
					} else {
						continue // unresolvable cross ref: drop, as on the input side
					}
					if p.TargetUpdates == nil {
						p.TargetUpdates = map[string][]types.UpdateRef{}
					}
					// Same hygiene the outputs loop applies, and for a live reason: an
					// update folds into step.Sources, which is glob-expanded exactly as
					// outputs are, and an escaping pattern there once made magus clean
					// abort the WHOLE workspace (doublestar rejects it mid-loop).
					if filepath.IsAbs(ref.Glob) || strings.Contains(ref.Glob, "..") {
						return types.DiagnosticErrorf(types.CrossOutputGlobEscapes,
							"%s: target %q: ctx.updates glob %q must be relative to %q and must not contain ..",
							types.ProjectDisplayName(p.Path, p.Name, p.Dir), n.Name, ref.Glob, owner)
					}
					resolved := types.UpdateRef{Project: owner, Glob: ref.Glob}
					if !slices.Contains(p.TargetUpdates[n.Name], resolved) {
						p.TargetUpdates[n.Name] = append(p.TargetUpdates[n.Name], resolved)
					}
					// No ordering edge either way, unlike inputs and outputs. Both of those
					// infer one from ownership - "I read you, so I run after" and "I write
					// your tree, so you run after me" - and neither inference holds for a
					// file magus does not own: the target rewrites one region of a file
					// whose other regions someone else authored, which says nothing about
					// build order. A target that needs ordering declares ctx.needs, where a
					// reader can see it.
				}
			}
		}
		if len(extra) > 0 {
			p.DependsOn = append(p.DependsOn, extra...)
			slices.Sort(p.DependsOn)
			p.DependsOn = slices.Compact(p.DependsOn)
		}
	}
	for _, co := range crossOut {
		op := m.ws.Get(co.owner)
		if op == nil {
			// Also a load error, and for the same reason the unresolvable case above is:
			// file.ResolveImport is path hygiene, not a registration check, so an import
			// naming a directory with no project marker resolves cleanly and lands here. The ref
			// still reaches step.Outputs and is still snapshotted, but it gains no
			// ordering edge and no owner records it, so nothing cleans it and the merge
			// driver cannot regenerate it. The cross-INPUT side already fails loudly on
			// the same typo (an unregistered dependency); outputs must not be quieter.
			return types.DiagnosticErrorf(types.CrossOutputOwnerUnknown,
				"%s: ctx.outputs declares an output into %q, which magus did not discover as a project; it needs a project marker and must not sit under an ignored directory",
				co.writer, co.owner)
		}
		// The two cross-project edges run opposite ways, so declaring both against one
		// project closes a 2-cycle: reading b.file(...) makes this project depend on b,
		// writing b.file(...) makes b depend on this project. That is the most natural
		// shape to reach for - read a project's sources, write its generated file back -
		// so it has to fail here, naming both halves, while both are still in hand.
		//
		// Left to the depgraph it surfaces as a bare "graph: dependency cycle" that names
		// no project and no file, takes down `magus graph` and the whole affected
		// pipeline rather than the two projects involved, and is scope-dependent: a run
		// selecting only the writer never builds the full graph, so the same workspace
		// passes or fails depending on what was asked for.
		if wp := m.ws.Get(co.writer); wp != nil && slices.Contains(wp.DependsOn, co.owner) {
			return types.DiagnosticErrorf(types.CrossOutputCycle,
				"%s: ctx.outputs writes %q into %q, so %s must run after %s, but %s already depends on %s; a target cannot both read from and write into the same project",
				co.writer, co.glob, co.owner, co.owner, co.writer, co.writer, co.owner)
		}
		if !slices.Contains(op.DependsOn, co.writer) {
			op.DependsOn = append(op.DependsOn, co.writer)
			slices.Sort(op.DependsOn)
			op.DependsOn = slices.Compact(op.DependsOn)
		}
		// File the glob on the OWNER, keyed by the writer. This is what makes the file
		// visible to everything that asks a project what lands in its tree - clean,
		// watch's rebuild-loop guard, ownership lookup, the merge driver - none of which
		// can see the writer's declaration, and all of which resolve globs against the
		// project's own root, which is exactly what this glob is relative to.
		if op.InboundOutputs == nil {
			op.InboundOutputs = map[string][]string{}
		}
		// Two projects writing one path is not a conflict magus can order its way out of.
		// Both snapshot the file, so whichever loses the race still records the winner's
		// bytes as its own output, and a later cache hit for the loser replays content it
		// never produced - cross-project cache poisoning, silent and durable.
		//
		// Checked here rather than left to the run-scoped MGS4002 advisory, which only
		// fires when both writers land in ONE dispatch and only for the first stage per
		// project, so two separate invocations never see it at all. Exact-path collisions
		// only, the same comparison MGS4002 makes: glob-vs-glob overlap is not decidable
		// in general.
		if claimant := outputClaimant(op, co); claimant != "" {
			first, second := min(claimant, co.writer), max(claimant, co.writer)
			return types.DiagnosticErrorf(types.OutputOverlapDetected,
				"%s: %q is declared as an output by two projects (%s and %s); they cannot be ordered against each other, and each would cache the other's bytes as its own output",
				co.owner, co.glob, first, second)
		}
		op.InboundOutputs[co.writer] = append(op.InboundOutputs[co.writer], co.glob)
	}
	return nil
}

// crossOutput is one target's declaration that it writes into ANOTHER project's tree:
// the owner whose tree receives the file, the writer producing it, and the glob relative
// to the OWNER's root. Collected during the walk and applied once every project is known.
type crossOutput struct{ owner, writer, glob string }

// outputClaimant returns the project already claiming co.glob in the owner's tree, or ""
// when the path is unclaimed. A claim is either another writer that declared the same
// inbound path or the owner declaring it for itself - the second matters just as much,
// since the owner's own build would then produce and clean a file the writer also owns.
// The writer re-declaring its own glob is not a claim; that is idempotent.
func outputClaimant(owner *types.Project, co crossOutput) string {
	for _, writer := range slices.Sorted(maps.Keys(owner.InboundOutputs)) {
		if writer != co.writer && slices.Contains(owner.InboundOutputs[writer], co.glob) {
			return writer
		}
	}
	if slices.Contains(owner.Outputs, co.glob) {
		return owner.Path
	}
	for _, refs := range owner.TargetOutputs {
		for _, ref := range refs {
			if (ref.Project == "" || ref.Project == owner.Path) && ref.Glob == co.glob {
				return owner.Path
			}
		}
	}
	return ""
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

// DescribeGraph returns the target dependency graph of each project, read statically
// from the magusfile source (describe.Extract) - deterministic and side-effect free, so
// introspection never runs a target body. Buzz magusfiles are supported; a project on
// any other engine yields an engine-tagged entry with no nodes until that extractor
// lands. ctx bounds the walk so a large workspace's introspection stays cancellable.
func (m *Magus) DescribeGraph(ctx context.Context) types.TargetGraphOutput {
	out := types.TargetGraphOutput{Definition: types.TargetGraphDefinition}
	repoRoot := gitRoot(m.ws.Root) // "" outside a repo; drives the repo-relative MAGUS.md heading
	for _, p := range m.ws.All() {
		if ctx.Err() != nil {
			break
		}
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			continue // best-effort introspection: a project we can't read just omits its graph
		}
		for _, src := range srcs {
			entry := types.TargetGraphProject{Path: p.Path, Name: types.ProjectDisplayName(p.Path, p.Name, p.Dir), Engine: src.Engine, DependsOn: p.DependsOn}
			if repoRoot != "" {
				if rel, err := filepath.Rel(repoRoot, p.Dir); err == nil {
					entry.RelPath = filepath.ToSlash(rel)
				}
			}
			// The workspace-root project's path is ".", which would render as the
			// ambiguous "## Project: ." heading. A DECLARED name wins outright - it is
			// the only label that survives being checked out under a different
			// directory name (a worktree, a renamed clone, a CI checkout), each of
			// which otherwise renamed the root project and rewrote every generated
			// index naming it. Without one, fall back to the directory-derived label.
			// A non-root RelPath is kept as-is.
			entry.RelPath = types.ProjectDisplayName(entry.RelPath, p.Name, p.Dir)
			entry.Name = entry.RelPath
			if src.Engine == "buzz" {
				nodes := collectTargetNodes(src)
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
// workspace-relative path the rest of the graph keys projects by. NOT the same resolution
// WithDependsOn does: a hand-written depends_on entry may be repo-relative OR dot-relative
// (file.Resolve), while an import path is ALWAYS dot-relative to the importing magusfile
// (file.ResolveImport). Reading a bare import as repo-relative is what silently mis-anchored
// every descendant import and broke graph builds, so the two must stay split.
// For inputs: a same-project entry (empty Project) takes this project's path; a
// cross-project entry resolves its raw import path.
// Resolving here lets assembleIO link every consumes edge by path.Join(Project, Rel)
// against the file node in the owning project directly, without re-anchoring. An
// unresolvable path is dropped (best-effort, matching the static extractor's never-error
// contract).
func resolveNodeRefs(nodes []types.TargetGraphNode, projectPath string) {
	for i := range nodes {
		if len(nodes[i].CrossDependencies) > 0 {
			resolved := make([]types.CrossTargetRef, 0, len(nodes[i].CrossDependencies))
			for _, ref := range nodes[i].CrossDependencies {
				r, err := file.ResolveImport(ref.Project, projectPath)
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
				r, err := file.ResolveImport(ref.Project, projectPath)
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
			Name:      p.Name,
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
		// buildStep, not baseStep: this entry describes ONE target, and baseStep
		// carries only the project-wide globs. A target's own ctx.outputs (and
		// ctx.inputs) were therefore missing from its own description - `magus
		// describe target md-generate` reported no outputs while the target
		// declares MAGUS.md - so the described plan disagreed with the plan the
		// cache actually keys and snapshots.
		step := m.buildStep(p, et.Name)

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
		// AllOutputs, not step.Outputs: the cache view is scoped to one target's
		// project-wide globs, so a file declared only by a per-target ctx.outputs
		// (the root MAGUS.md) or written in by another project would report as a
		// hand-editable source. This is the "what lands in this tree" question, the
		// same one clean and the merge driver ask.
		declared := p.AllOutputs()
		outputs := make([]string, 0, len(declared))
		for _, o := range declared {
			outputs = append(outputs, joinGlob(p.Path, o))
		}
		if matchAnyGlob(outputs, path) {
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
