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
// What `memory_mb` still does after machine-wide admission was removed: it converts
// to concurrency slots on the in-process limiter (see slotsForPolicy), so a heavy
// target throttles its peers WITHIN a run. A declaration written at 2GB for a target
// that now reaches 9GB throttles nothing, and magus already records what each target
// reached, which makes the disagreement a fact rather than a question.
//
// Advice, never a failure: the magusfile figure stays a human's to write, and a
// deliberate ceiling above the measured peak is a legitimate thing to declare.
func (r *runner) checkMemoryDeclarations(projects []*types.Project) types.DoctorCheck {
	const name = "memory-declarations"

	var h forecast.History
	if err := h.Load(r.runCtx(), r.opts.cfg.HistoryPath); err != nil {
		// Advice, not OK: the check could not RUN, which is a different answer from
		// "everything agrees" and the reader needs to know which one they got.
		return types.DoctorCheck{
			Name: name, Status: types.DoctorAdvice,
			Message: "run history is unreadable, so declarations could not be checked against what magus measured",
			Details: []string{err.Error()},
		}
	}
	// A missing history file loads as an empty one, so this is the ordinary state of a
	// fresh clone rather than an error. Saying so beats "everything agrees", which
	// claims a comparison that never happened.
	if len(h.Projects) == 0 {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
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
		for target, peakMB := range measuredPeaksMB(&h, p.Path) {
			declaredMB, declaredBy := types.ChainMemoryMB(p, target, lookup)
			if d := gradeDeclaration(label, target, declaredBy, declaredMB, peakMB); d != "" {
				details = append(details, d)
			}
		}
	}

	if len(details) == 0 {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
			Message: "every measured target agrees with what it declares",
		}
	}
	slices.Sort(details)
	details = slices.Compact(details)
	return types.DoctorCheck{
		Name:   name,
		Status: types.DoctorAdvice,
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
func gradeDeclaration(project, target, declaredBy string, declaredMB, peakMB int) string {
	declares := "declares"
	if declaredBy != "" && declaredBy != target {
		declares = "runs " + declaredBy + ", which declares"
	}
	switch {
	case declaredMB <= 0:
		if peakMB < undeclaredFloorMB {
			return ""
		}
		return fmt.Sprintf(
			"%s %s declares no memory_mb anywhere in what it runs and reached at least %dMB; an undeclared target takes one slot like any other, so magus starts its peers alongside it",
			project, target, peakMB)
	case float64(peakMB) > float64(declaredMB)*underDeclaredRatio && peakMB-declaredMB >= materialGapMB:
		return fmt.Sprintf(
			"%s %s %s %dMB and reached at least %dMB; admission seats it against a figure it exceeds",
			project, target, declares, declaredMB, peakMB)
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

// measuredPeaksMB returns the highest peak recorded for each of a project's targets,
// keyed by the bare target name.
//
// The history keys a target as "<spell>/<target>", because the unit that runs is a
// spell's implementation of a target, while a magusfile declares memory against the
// bare name. One target served by two spells therefore has two histories, and the
// figure that has to fit on a machine is the larger of them.
func measuredPeaksMB(h *forecast.History, project string) map[string]int {
	targets, ok := h.Projects[project]
	if !ok {
		return nil
	}
	out := map[string]int{}
	for key := range targets {
		peak, ok := h.PredictPeakRSS(project, key)
		if !ok {
			continue
		}
		name := key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			name = key[i+1:]
		}
		if mb := int(peak >> 20); mb > out[name] {
			out[name] = mb
		}
	}
	return out
}
