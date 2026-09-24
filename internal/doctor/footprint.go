package doctor

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// checkWriteWithoutRWCharm is MGS1035: a target with an `if (ctx.hasCharm("rw"))` branch that
// calls fs\writeFile OUTSIDE it.
//
// Branching on rw means the target runs two ways, and the run given no rw charm must not
// touch the tree. That is the whole reason `--no-default-charms` is a verdict rather than an
// edit. A write outside the branch breaks it in the way hardest to see: the file is written,
// then compared, then reported as different, and the written file stays behind for a later
// gate to blame on a target that did nothing.
//
// The shape is easy to reach honestly. Rendering the expected content and writing it are one
// line apart, and moving the write inside the branch looks like a refactor, not a fix.
func (r *runner) checkWriteWithoutRWCharm(projects []*types.Project) types.Check {
	const name = "writes-without-rw-charm"
	var details []string
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, w := range describe.WritesOutsideRWCharm(string(data)) {
				details = append(details, fmt.Sprintf("%s: %s writes the tree outside its rw branch (%s:%d)",
					types.ProjectDisplayName(p.Path, p.Name, p.Dir), w.Fn, r.relPath(f), w.Line))
			}
		}
	}
	if len(details) == 0 {
		return types.Check{Name: name, Status: types.CheckOK, Message: "no target writes the tree when it was given no rw charm"}
	}
	slices.Sort(details)
	return types.Check{
		Name:   name,
		Status: types.CheckFail,
		Message: fmt.Sprintf(
			"%d fs\\writeFile call(s) sit outside the target's own rw branch, so a run given no rw charm edits the tree "+
				"it was asked to judge and then reports its own edit (see %s)",
			len(details), types.CodeURL(types.WriteWithoutRWCharm)),
		Details: details,
	}
}

// checkSourceIsAlsoOutput is MGS1034: one target names the same path in both
// ctx.readsFiles and ctx.writesFiles.
//
// An output is a file magus owns end to end, so a cache hit restores it wholesale from the
// snapshot. Naming it as an input too means the bytes the key is computed from are the bytes
// the cache itself just wrote: an edit to that file cannot miss the cache, and the target
// that reads it never sees the edited content. A target comparing such a file against what
// it would generate is comparing the cache with itself, and passes whatever the tree holds.
//
// It reads as a reasonable thing to write, which is what makes it worth a code: declaring
// the file you verify looks like honesty about the footprint. The fix is to drop it from
// ctx.readsFiles. A derived file is a FUNCTION of the real inputs, so keying on those is
// what regenerates it; keying on the derived file buys nothing and hides a hand edit.
//
// Only DECLARED refs are visible. A target that writes a file without ctx.writesFiles is
// undetectable here, and that omission is its own defect.
func (*runner) checkSourceIsAlsoOutput(projects []*types.Project) types.Check {
	const name = "source-is-also-output"
	var details []string
	for _, p := range projects {
		for target, inputs := range p.TargetInputs {
			outputs := p.TargetOutputs[target]
			if len(outputs) == 0 {
				continue
			}
			var seen []string
			for _, in := range inputs {
				if slices.Contains(seen, in.Glob) {
					continue
				}
				if !slices.ContainsFunc(outputs, func(out types.OutputRef) bool {
					return out.Glob == in.Glob && out.Project == in.Project
				}) {
					continue
				}
				seen = append(seen, in.Glob)
				details = append(details, fmt.Sprintf("%s: %s declares %q as both a source and an output",
					types.ProjectDisplayName(p.Path, p.Name, p.Dir), target, in.Glob))
			}
		}
	}
	if len(details) == 0 {
		return types.Check{Name: name, Status: types.CheckOK, Message: "no target reads a file it declares as its own output"}
	}
	slices.Sort(details)
	return types.Check{
		Name:   name,
		Status: types.CheckFail,
		Message: fmt.Sprintf(
			"%d path(s) declared as both a source and an output of one target; the cache restores the file before the target reads it, "+
				"so an edit to it can neither miss the cache nor be seen (see %s)",
			len(details), types.CodeURL(types.SourceIsAlsoOutput)),
		Details: details,
	}
}

// checkFootprintDropsOpGlobs is MGS1036: a target that declares its own footprint with
// ctx.readsFiles and then composes a spell op reading file kinds that footprint never names.
//
// buildStep treats an explicit footprint as an ownership boundary and resets step.Sources to
// the magusfiles, then folds back only the refs the body declared and the spell sources
// specific to THAT target. A spell whose globs are project-wide (the go spell's
// mgs_listRequiredGlobs takes no target, so every glob it has is project-wide) therefore
// loses all of them, and a target calling go-fmt or go-test keys on no Go file at all.
//
// The failure is green, which is what earns it a check. The op is skipped rather than
// failed, and a sibling target that kept the baseline still re-runs, so a gate composing
// both passes while the formatter never saw the edit. It reached this workspace four times.
//
// Total omission only. Naming one *.go path is the narrowing its author meant and says
// nothing about the rest; naming no Go file under a target that runs the Go toolchain is
// the mistake, and the two are told apart without knowing what an op reads.
func (*runner) checkFootprintDropsOpGlobs(projects []*types.Project) types.Check {
	const name = "footprint-drops-op-globs"
	var details []string
	for _, p := range projects {
		for target, inputs := range p.TargetInputs {
			if len(inputs) == 0 {
				continue
			}
			declared, unbounded := declaredFileKinds(p, target)
			if unbounded {
				continue
			}
			for _, use := range p.TargetSpellOps[target] {
				sp := resolvedSpell(p, use.Spell)
				if sp == nil {
					continue
				}
				kinds := globFileKinds(sp.Sources())
				if len(kinds) == 0 {
					continue
				}
				// Globs the spell declares for THIS target survive the reset, so they
				// count as part of the footprint rather than as something it dropped.
				have := append(slices.Clone(declared), globFileKinds(sp.TargetSources()[target])...)
				if kindsOverlap(have, kinds) {
					continue
				}
				details = append(details, fmt.Sprintf(
					"%s: %s declares its own footprint and calls %s, whose spell reads %s; no declared glob names any of them",
					types.ProjectDisplayName(p.Path, p.Name, p.Dir), target,
					strings.Join(qualifiedOps(use), ", "), strings.Join(sp.Sources(), " ")))
			}
		}
	}
	if len(details) == 0 {
		return types.Check{Name: name, Status: types.CheckOK, Message: "every narrowed footprint still names the files its ops read"}
	}
	slices.Sort(details)
	return types.Check{
		Name:   name,
		Status: types.CheckFail,
		Message: fmt.Sprintf(
			"%d target(s) replace their cache footprint and then run an op over files that footprint never names, so an edit to those files replays "+
				"the op instead of running it (see %s)",
			len(details), types.CodeURL(types.FootprintDropsOpGlobs)),
		Details: details,
	}
}

// qualifiedOps names a spell use as the reader sees it in the magusfile body.
func qualifiedOps(use types.TargetSpellUse) []string {
	if len(use.Ops) == 0 {
		return []string{use.Spell}
	}
	out := make([]string, 0, len(use.Ops))
	for _, op := range use.Ops {
		out = append(out, use.Spell+"["+op+"]")
	}
	return out
}

// resolvedSpell finds a project's bound spell by name.
func resolvedSpell(p *types.Project, name string) *spells.Spell {
	for _, s := range p.ResolvedSpells {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// declaredFileKinds is every file kind a target's own declarations name, plus whether one of
// them is broad enough that asking the question is meaningless.
//
// ctx.modifiesExistingFiles counts: buildStep folds those refs into Sources exactly as
// inputs, so a target that declares what it EDITS has keyed those files whether or not it
// also calls them inputs. That is the shape gofmt and dprint have, and reporting it would
// be reporting the correct declaration.
func declaredFileKinds(p *types.Project, target string) (kinds []string, unbounded bool) {
	globs := make([]string, 0, len(p.TargetInputs[target])+len(p.TargetUpdates[target]))
	for _, ref := range p.TargetInputs[target] {
		globs = append(globs, ref.Glob)
	}
	for _, ref := range p.TargetUpdates[target] {
		globs = append(globs, ref.Glob)
	}
	for _, g := range globs {
		// A footprint that takes everything under a tree drops nothing, and it carries no
		// extension to compare, so it would otherwise read as naming no kind at all.
		if base := path.Base(g); base == "*" || base == "**" {
			return nil, true
		}
	}
	return globFileKinds(globs), false
}

// globFileKinds reduces globs to the file kinds they can match: the extension, plus the
// exact basename when the glob names one literally, so `go.mod` and `**/*.mod` compare equal
// and a literal `tapes/demo.txtar` still answers for `**/*.txtar`.
//
// A glob with neither (a bare directory tree such as `internal/agent/skills/**`) yields
// nothing rather than a wildcard: it must not manufacture an overlap it cannot prove.
func globFileKinds(globs []string) []string {
	var out []string
	add := func(k string) {
		if k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, g := range globs {
		base := path.Base(g)
		add(path.Ext(base))
		if !strings.ContainsAny(base, "*?[") {
			add(base)
		}
	}
	return out
}

// kindsOverlap reports whether the two sets share a file kind. Any overlap at all is the
// author narrowing on purpose; none is the footprint having lost the spell entirely.
func kindsOverlap(a, b []string) bool {
	return slices.ContainsFunc(a, func(k string) bool { return slices.Contains(b, k) })
}
