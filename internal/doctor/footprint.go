package doctor

import (
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/internal/describe"
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
func (r *runner) checkWriteWithoutRWCharm(projects []*types.Project) types.DoctorCheck {
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
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no target writes the tree when it was given no rw charm"}
	}
	slices.Sort(details)
	return types.DoctorCheck{
		Name:   name,
		Status: types.DoctorFail,
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
func (*runner) checkSourceIsAlsoOutput(projects []*types.Project) types.DoctorCheck {
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
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no target reads a file it declares as its own output"}
	}
	slices.Sort(details)
	return types.DoctorCheck{
		Name:   name,
		Status: types.DoctorFail,
		Message: fmt.Sprintf(
			"%d path(s) declared as both a source and an output of one target; the cache restores the file before the target reads it, "+
				"so an edit to it can neither miss the cache nor be seen (see %s)",
			len(details), types.CodeURL(types.SourceIsAlsoOutput)),
		Details: details,
	}
}
