package doctor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

const (
	// underDeclaredRatio and materialGapMB decide when a disagreement is worth
	// reporting. The absolute floor keeps a 40MB target that peaked at 60MB out, where
	// the ratio alone would put it in.
	//
	// Only under-declaration is reported. See gradeDeclaration for why the measured
	// peak cannot support the symmetric finding.
	underDeclaredRatio = 1.25
	materialGapMB      = 512

	// undeclaredFloorMB is the peak at which an undeclared target is worth declaring.
	// Below it the contribution is smaller than the variation in measuring it.
	undeclaredFloorMB = 2048
)

// checkMemoryDeclarations is MGS1030: a target whose declared memory_mb disagrees
// with the peak resident memory magus has measured for it.
//
// This is what keeps MGS3009 honest. A declared-budget gate is only as good as its
// declarations, and declarations rot silently: a target written at 2GB grows to 9GB
// over a year and the machine budget quietly stops protecting anything, while a target
// declared far above what it uses queues peers that would have fit. magus already
// records what each target reached, which makes the disagreement a fact rather than a
// question.
//
// Advice, never a failure: the magusfile figure stays a human's to write, and a
// deliberate ceiling above the measured peak is a legitimate thing to declare.
func (r *runner) checkMemoryDeclarations(projects []*types.Project) types.Check {
	const name = "memory-declarations"

	var h forecast.History
	if err := h.Load(r.runCtx(), r.opts.cfg.HistoryPath); err != nil {
		// Advice, not OK: the check could not RUN, which is a different answer from
		// "everything agrees" and the reader needs to know which one they got.
		return types.Check{
			Name: name, Status: types.CheckAdvice,
			Message: "run history is unreadable, so declarations could not be checked against what magus measured",
			Details: []string{err.Error()},
		}
	}
	// A missing history file loads as an empty one, so this is the ordinary state of a
	// fresh clone rather than an error. Saying so beats "everything agrees", which
	// claims a comparison that never happened.
	if len(h.Projects) == 0 {
		return types.Check{
			Name: name, Status: types.CheckOK,
			Message: "no targets have run yet, so there is nothing to compare declarations against",
		}
	}

	// The SAME fold admission gates on, or doctor tells an author `ci` declares
	// nothing while the machine holds ten gigabytes for it.
	byPath := make(map[string]*types.Project, len(projects))
	for _, p := range projects {
		byPath[p.Path] = p
	}
	lookup := func(path string) *types.Project { return byPath[path] }

	var details []string
	for _, p := range projects {
		label := types.ProjectDisplayName(p.Path, p.Name, p.Dir)
		for target, peaks := range measuredPeaksMB(&h, p.Path) {
			declaredMB, declaredBy := types.ChainMemoryMB(p, target, lookup)
			if d := gradeDeclaration(label, target, declaredBy, declaredMB, peaks); d != "" {
				details = append(details, d)
			}
		}
	}

	if len(details) == 0 {
		return types.Check{
			Name: name, Status: types.CheckOK,
			Message: "every measured target agrees with what it declares",
		}
	}
	slices.Sort(details)
	details = slices.Compact(details)
	return types.Check{
		Name:   name,
		Status: types.CheckAdvice,
		Message: fmt.Sprintf(
			"%d target(s) declare memory that disagrees with what magus measured; admission is only as good as the declarations it arbitrates (see %s)",
			len(details), types.CodeURL(types.MemoryDeclarationDrift)),
		Details: details,
	}
}

// gradeDeclaration renders the finding for one target, or "" when the declaration
// and the measurement agree closely enough to leave alone. declaredBy names the
// target the figure came from, which is not this one when it was inherited from a
// composed target. The reader needs the line to edit, not the line they ran.
//
// An undeclared target claims 1.25 times its highest passing peak once three runs
// measured it, so it wants a declaration only when its passing peaks spread wider than
// that headroom: then the next run can outgrow a claim sized from the last ones.
func gradeDeclaration(project, target, declaredBy string, declaredMB int, peaks peakRange) string {
	declares := "declares"
	if declaredBy != "" && declaredBy != target {
		declares = "runs " + declaredBy + ", which declares"
	}
	switch {
	case declaredMB <= 0:
		if peaks.highMB < undeclaredFloorMB || !peaks.unstable() {
			return ""
		}
		return fmt.Sprintf(
			"%s %s declares no memory_mb anywhere in what it runs and its passing runs reached from %dMB to %dMB; a claim measured from them carries a quarter's headroom, which that spread outgrows",
			project, target, peaks.passLowMB, peaks.passHighMB)
	case float64(peaks.highMB) > float64(declaredMB)*underDeclaredRatio && peaks.highMB-declaredMB >= materialGapMB:
		return fmt.Sprintf(
			"%s %s %s %dMB and reached at least %dMB; admission seats it against a figure it exceeds",
			project, target, declares, declaredMB, peaks.highMB)
	}
	// No over-declared finding, deliberately. The symmetric case ("declares far
	// more than it has ever used, lower it") is the one a measured PEAK could
	// support and this measurement is not one: types.PeakRSS folds a target's
	// processes as a maximum, so a parallel suite records roughly its largest
	// single process rather than what its tree held. The arm shipped anyway and
	// told this repo that `test` declares 10240MB and "has never exceeded"
	// 3553MB, when one sampled instant of that suite held 3.11GiB across 17
	// processes and the declaration was written from a real 16GB runner death.
	//
	// The two arms above survive the same shortfall because a floor only ever
	// argues in the safe direction: an undeclared target that reached AT LEAST
	// 2GB still wants a declaration, and a declaration a floor already exceeds is
	// exceeded by more. Restore this arm when the fold measures a tree.
	return ""
}

// peakRange is what a target's recorded peaks say about its memory, in MiB.
type peakRange struct {
	// highMB is the highest peak of any run, a failed one included.
	highMB int
	// passLowMB and passHighMB bound the peaks of the passing runs, which are what a
	// measured claim is sized from; passRuns counts them.
	passLowMB, passHighMB, passRuns int
}

// unstable reports whether the passing peaks spread wider than the headroom a claim
// measured from them carries.
func (r peakRange) unstable() bool {
	return r.passRuns >= 2 && float64(r.passHighMB) > float64(r.passLowMB)*underDeclaredRatio
}

// measuredPeaksMB returns the recorded peaks of each of a project's targets, keyed by
// the bare target name.
//
// The history keys a target as "<spell>/<target>", because the unit that runs is a
// spell's implementation of a target, while a magusfile declares memory against the
// bare name. One target served by two spells therefore has two histories: the figure
// that has to fit on a machine is the larger of them, and the spread that matters is
// that of the spell whose passing peak sizes the claim.
func measuredPeaksMB(h *forecast.History, project string) map[string]peakRange {
	targets, ok := h.Projects[project]
	if !ok {
		return nil
	}
	out := map[string]peakRange{}
	for key, st := range targets {
		var r peakRange
		for _, o := range st.RecentOutcomes {
			if o.MaxRSSBytes <= 0 {
				continue
			}
			mb := int(o.MaxRSSBytes >> 20)
			r.highMB = max(r.highMB, mb)
			if o.Result == forecast.OutcomeFail {
				continue
			}
			if r.passRuns == 0 || mb < r.passLowMB {
				r.passLowMB = mb
			}
			r.passHighMB, r.passRuns = max(r.passHighMB, mb), r.passRuns+1
		}
		if r.highMB == 0 {
			continue
		}
		name := key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			name = key[i+1:]
		}
		prev := out[name]
		if r.passHighMB < prev.passHighMB {
			r.passLowMB, r.passHighMB, r.passRuns = prev.passLowMB, prev.passHighMB, prev.passRuns
		}
		r.highMB = max(r.highMB, prev.highMB)
		out[name] = r
	}
	return out
}
