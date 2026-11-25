package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// validateMerge holds a merge into job id, against the plan rows, to every rule a stored
// job meets, in one pass that never stops at the first problem, except that a new row a live
// job contends with is refused before the expensive checks run (see contendedFork). The
// result carries the row as the merge would leave it, every problem left once the row's
// deviations apply, the problems they permitted, each check that could not run and why, and
// the write proof the row
// records.
//
// A merge that creates the row, or brings an ended one back, meets every rule. The record
// holds its own shape, a boundary and a grade (JobUngraded), the jobs limits
// (JobForkLimitExceeded) and unambiguous symbol postconditions (JobPostconditionInvalid). Its write paths
// name no directory (WritePathIsDirectory), no ungradable claim (WritePathClaimUngradable),
// no project configuration held whole (WritePathConfigWholeFile), no file the workspace
// loads while another job works in this checkout (CheckoutConfigHeld), no unordered shared
// file (WritePathFileShared) and nothing broader than the workspace allows
// (WritePathTooBroad). Its checks skip no test of the write paths' packages, are ones the
// guard lets the holder run, and name a project and target the workspace has
// (JobCheckUnrunnable). No regenerated output sits outside the write paths
// (OutputOutsideWritePaths), and no ci target over them goes unrun (GateNotRun).
//
// A merge onto a row that has not ended meets the same rules for what it ADDS: the postconditions,
// checks, write paths and deny paths it did not carry, the outputs and gates those bring
// in, and the shape problems the row did not already have, so a path the row already held
// never blocks an unrelated update. A path an entry the row held already covers
// ([job.PathWithin]) adds nothing, so a holder narrowing a glob to its files adds no path.
// Its proof is recomputed when its write paths change, and is empty otherwise, which keeps
// the one it holds.
//
// An overlap with a live job is recorded in the proof, never refused (ADR 0010). The
// workspace checks read store's [Workspace] (see [Store.InWorkspace]); with none, each is
// named in Unchecked instead. read may be nil where no graph is at hand, which skips the
// ambiguity check. The guard grades the checks against rows rather than sweep the store
// again.
func validateMerge(ctx context.Context, store *Store, rows []job.Job, id string, merge func(*job.Job), limits config.Jobs, read SymbolReader) types.JobValidation {
	// A new row is not one HardenWritten changes, so its base reads the same before it runs.
	prev, updates := mergeBase(rows, id, merge)
	if !updates {
		if v, refused := contendedFork(ctx, store, rows, id, mergeOnto(prev, merge), limits); refused {
			return v
		}
	}
	rows = HardenWritten(ctx, store, rows)
	var tries []job.Attempt
	var triesErr error
	if store != nil {
		tries, triesErr = store.Attempts(ctx)
	}
	graded := WithSnapshot(ctx, Snapshot{Rows: rows, Attempts: tries, Err: triesErr})
	prev, updates = mergeBase(rows, id, merge)
	merged := mergeOnto(prev, merge)
	v := types.JobValidation{Job: merged}
	var ps types.JobProblems
	// judged is the row the workspace checks read: merged whole for a new row, its added
	// write paths with every check it runs for an update.
	judged := merged
	if updates {
		ps = append(ps, newProblems(declarationProblems(rowDeclaration(prev)), declarationProblems(rowDeclaration(merged)))...)
		ps = append(ps, ungradedProblems(merged)...)
		ps = append(ps, droppedProblems(prev, merged)...)
		var postconditions []job.Postcondition
		for _, gate := range merged.Postconditions {
			if !slices.ContainsFunc(prev.Postconditions, func(g job.Postcondition) bool { return g.ID == gate.ID }) {
				postconditions = append(postconditions, gate)
			}
		}
		ps = append(ps, onRow(ambiguousSymbolProblems(ctx, postconditions, read), job.Job{Postconditions: postconditions}, merged)...)
		added := slices.DeleteFunc(slices.Clone(merged.Paths.Write), func(p string) bool {
			return slices.ContainsFunc(prev.Paths.Write, func(held string) bool { return job.PathWithin(p, held) })
		})
		addedRow := job.Job{ID: id, Paths: job.Paths{Write: added, Soft: merged.Paths.Soft}, Parent: merged.Parent, DependsOn: merged.DependsOn}
		ps = append(ps, onRow(addedWritePathProblems(ctx, store, addedRow, merged.Integrates), addedRow, merged)...)
		addedDenies := job.Job{ID: id, Paths: job.Paths{Deny: slices.DeleteFunc(slices.Clone(merged.Paths.Deny), func(p string) bool { return slices.Contains(prev.Paths.Deny, p) })}}
		ps = append(ps, onRow(ungradableClaimProblems(ctx, store, addedDenies), addedDenies, merged)...)
		ps = append(ps, onRow(unorderedFileShareProblems(ctx, store, claimingRows(rows), claiming(addedRow)), addedRow, merged)...)
		// Judged on what the update changes, so a check the row already held never blocks an
		// unrelated update: a new check or postcondition is graded against every write path, an old
		// one only against the paths this update adds.
		checkChanged := !sameStoredCheck(prev.Check, merged.Check)
		newChecks := job.Job{ID: id, Paths: job.Paths{Write: merged.Paths.Write}, Postconditions: postconditions}
		if checkChanged {
			newChecks.Check = merged.Check
		}
		ps = append(ps, onRow(narrowedTestProblems(store, newChecks), newChecks, merged)...)
		if len(added) > 0 {
			oldChecks := job.Job{ID: id, Paths: job.Paths{Write: added}, Check: merged.Check, Postconditions: merged.Postconditions}
			ps = append(ps, onRow(narrowedTestProblems(store, oldChecks), oldChecks, merged)...)
		}
		checks := addedChecks(prev, merged, checkChanged)
		ps = append(ps, deniedCheckProblems(graded, store, checks)...)
		judged = job.Job{ID: id, Paths: job.Paths{Write: added}, Check: checks.Check, Postconditions: checks.Postconditions, ReadOnly: merged.ReadOnly}
		if !slices.Equal(prev.Paths.Write, merged.Paths.Write) {
			v.WriteProof = store.WriteProof(rows, merged)
		}
	} else {
		ps = append(ps, declarationProblems(rowDeclaration(merged))...)
		ps = append(ps, unscopedProblems(merged)...)
		ps = append(ps, ungradedProblems(merged)...)
		ps = append(ps, forkLimitProblems(rows, id, merged.Parent, limits)...)
		ps = append(ps, claimCeilingProblems(merged, limits)...)
		ps = append(ps, ambiguousSymbolProblems(ctx, merged.Postconditions, read)...)
		ps = append(ps, mergedDirectoryProblems(ctx, store, merged, merged.Integrates)...)
		ps = append(ps, ungradableClaimProblems(ctx, store, merged)...)
		ps = append(ps, wholeFileConfigClaimProblems(ctx, store, merged)...)
		ps = append(ps, sharedCheckoutProblems(store, rows, merged)...)
		ps = append(ps, unorderedFileShareProblems(ctx, store, claimingRows(rows), claiming(merged))...)
		ps = append(ps, narrowedTestProblems(store, merged)...)
		ps = append(ps, deniedCheckProblems(graded, store, merged)...)
		v.WriteProof = store.WriteProof(rows, merged)
	}
	found, unchecked := workspaceProblems(ctx, store, rows, merged, judged, updates, read)
	ps = append(ps, found...)
	v.Unchecked = unchecked
	v.Problems, v.Permitted = permitBut(ps, merged.Deviations)
	return v
}

// mergeOnto is merge applied to prev as the write stores it: an ended prev the merge brings
// back is [job.Job.HandedOutAfresh], so a refork is graded without its last holder's checkout.
func mergeOnto(prev job.Job, merge func(*job.Job)) job.Job {
	merged := prev.Clone()
	merge(&merged)
	if prev.State.Terminal() && merged.State.Live() {
		return merged.HandedOutAfresh()
	}
	return merged
}

// droppedProblems is each declared field of the live row prev that merged leaves empty:
// the instructions, the write paths, the check and the postconditions. A record that re-declares a
// live job without them erases what its holder was given, so it is refused unless a
// deviation on the field's "drops <field>" subject says the erasure is meant.
func droppedProblems(prev, merged job.Job) types.JobProblems {
	var ps types.JobProblems
	dropped := func(field string, held, kept bool) {
		if !held || kept {
			return
		}
		subject := types.DropsSubject + field
		reason := fmt.Sprintf("the record leaves %s empty and %s declared it", field, prev.ID)
		ps = append(ps, types.JobProblem{
			Code: types.JobRecordInvalid, Field: field, Subject: subject,
			Message: fmt.Sprintf("this record would erase %s's %s", prev.ID, field),
			Hint: fmt.Sprintf("send the %s the job holds, or name the erasure: {\"code\":%q,\"subject\":%q,\"reason\":%q}",
				field, types.JobRecordInvalid, subject, reason),
		})
	}
	dropped("agent.instructions", prev.Instructions() != "", merged.Instructions() != "")
	dropped("paths.write", len(prev.Paths.Write) > 0, len(merged.Paths.Write) > 0)
	dropped("check", !prev.Check.IsZero(), !merged.Check.IsZero())
	dropped("postconditions", len(prev.Postconditions) > 0, len(merged.Postconditions) > 0)
	return ps
}

// cheapRefusalSkip is the Unchecked entry of a new row contendedFork refuses.
const cheapRefusalSkip = "the symbol, claim, test, check and workspace checks and the write proof: not run," +
	" since this fork already contends with a live job; fork again once it does not"

// contendedFork is the refusal of a new row the jobs limits or a live job's hold already
// refuse, with every other check that reads only the record, before the footprint reads and
// workspace checks that make a fork under load take minutes. Rows are not hardened yet: that
// only adds hard paths, so a refusal here stands, and a pass is judged again in full.
func contendedFork(ctx context.Context, store *Store, rows []job.Job, id string, merged job.Job, limits config.Jobs) (types.JobValidation, bool) {
	contended := slices.Concat(forkLimitProblems(rows, id, merged.Parent, limits), sharedCheckoutProblems(store, rows, merged),
		unorderedFileShareProblems(ctx, store, claimingRows(rows), claiming(merged)))
	if kept, _ := permitBut(contended, merged.Deviations); len(kept) == 0 {
		return types.JobValidation{}, false
	}
	ps := slices.Concat(declarationProblems(rowDeclaration(merged)), unscopedProblems(merged), ungradedProblems(merged),
		claimCeilingProblems(merged, limits), directoryWritePathProblems(store, merged), contended)
	v := types.JobValidation{Job: merged, Unchecked: []string{cheapRefusalSkip}}
	v.Problems, v.Permitted = permitBut(ps, merged.Deviations)
	return v, true
}

// lintTarget is the ci target whose findings a worker must meet itself: the root meets them
// otherwise, one integration late.
const lintTarget = "lint"

// permitBut is ps.Permit with lint exempted from globs: a gate on lint is permitted only by a
// deviation naming its exact subject, so `**` cannot hide the findings a go-test check never
// shows.
func permitBut(ps types.JobProblems, deviations []job.Deviation) (kept, permitted types.JobProblems) {
	isLint := func(p types.JobProblem) bool {
		return p.Code == types.GateNotRun && strings.HasPrefix(p.Subject, lintTarget+" ")
	}
	var lint, rest types.JobProblems
	for _, p := range ps {
		if isLint(p) {
			lint = append(lint, p)
		} else {
			rest = append(rest, p)
		}
	}
	exact := slices.DeleteFunc(slices.Clone(deviations), func(w job.Deviation) bool { return types.IsGlobMeta(w.Subject) })
	kept, permitted = rest.Permit(deviations)
	lintKept, lintPermitted := lint.Permit(exact)
	return append(kept, lintKept...), append(permitted, lintPermitted...)
}

// lintPostconditions lints each unlinted project over the written files present at fork: a
// path never created would fail the tool. Exit grades the changed files ([ranOver]), so
// created files still count. A project with none present keeps all.
func lintPostconditions(ctx context.Context, root string, row job.Job, ws Workspace, grader CheckGrader) (postconditions []types.Postcondition, notes []string) {
	if row.ReadOnly || len(row.Paths.Write) == 0 || ws.Unavailable != "" {
		return nil, nil
	}
	written := slices.DeleteFunc(writtenFiles(root, row), func(f string) bool { return slices.Contains(row.Paths.Soft, f) })
	slices.Sort(written)
	gates, _ := unrunGates(root, row, ws.Projects, RegeneratedOutside(root, row, ws.Projects, ws.Ignored), gradeWith(grader, root, row))
	var lint []UnrunGate
	for _, g := range gates {
		if g.Target == lintTarget && !g.IntegratorGates {
			lint = append(lint, g)
		}
	}
	if len(lint) == 0 {
		return nil, nil
	}
	written, note := withoutIgnored(ctx, root, ws, written)
	if note != "" {
		notes = append(notes, note)
	}
	absent := map[string]bool{}
	for _, f := range written {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil || !info.Mode().IsRegular() {
			absent[f] = true
		}
	}
	for _, g := range lint {
		in := filesIn(g.Project, written)
		present := slices.DeleteFunc(slices.Clone(in), func(f string) bool { return absent[path.Join(g.Project, f)] })
		switch left := len(in) - len(present); {
		case left > 0 && len(present) == 0:
			notes = append(notes, fmt.Sprintf("none of the %d file(s) the lint postcondition for %s reads exists yet, so it names every one: drop from the run any the job does not create", len(in), g.Project))
			present = in
		case left > 0:
			notes = append(notes, fmt.Sprintf("left %d written file(s) the tree does not hold yet out of the lint postcondition for %s: exit grades the run over every file the job changed, so name each one it creates", left, g.Project))
		}
		postcondition := g.Postcondition
		postcondition.Check.NoDefaultCharms = true
		postcondition.Check.Args = present
		postconditions = append(postconditions, postcondition)
	}
	return postconditions, notes
}

// withoutIgnored is files less those the VCS ignores, and a note when it left some out or
// could not be asked. A tracked file stays even when an ignore rule covers it: IgnoredFiles
// answers from the rules alone, so a force-added file is asked of TrackedFiles too.
func withoutIgnored(ctx context.Context, root string, ws Workspace, files []string) ([]string, string) {
	if len(files) == 0 {
		return files, ""
	}
	if ws.Ignored == nil {
		return files, "no version control could be asked which written files it ignores, so the lint postcondition keeps every one"
	}
	ignored, err := ws.Ignored(files)
	if err != nil {
		return files, fmt.Sprintf("the lint postcondition keeps every written file: %v", err)
	}
	if len(ignored) == 0 {
		return files, ""
	}
	drop := make(map[string]bool, len(ignored))
	for _, f := range ignored {
		drop[f] = true
	}
	if res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{}); err == nil && res.VCS != nil {
		tracked, err := res.VCS.TrackedFiles(ctx, root, ignored)
		if err != nil {
			return files, fmt.Sprintf("%s could not say which ignored files it tracks (%v), so the lint postcondition keeps every written file", res.Name, err)
		}
		for _, f := range tracked {
			delete(drop, f)
		}
	}
	kept := slices.DeleteFunc(slices.Clone(files), func(f string) bool { return drop[f] })
	if left := len(files) - len(kept); left > 0 {
		return kept, fmt.Sprintf("left %d ignored file(s) out of the lint postcondition", left)
	}
	return kept, ""
}

// filesIn is the files under project, relative to it. The root project holds every file.
func filesIn(project string, files []string) []string {
	if project == "." {
		return slices.Clone(files)
	}
	var in []string
	for _, f := range files {
		if rel, ok := strings.CutPrefix(f, project+"/"); ok {
			in = append(in, rel)
		}
	}
	return in
}

// ExtendFootprint is the write paths row's change reaches that it did not declare, and a
// note saying why for each: the test files beside a written file that reference what the
// write paths define ([ReferencedOutside]). Only a non-test write path extends: a written
// test file would pull in every test that merely shares its helpers. A read-only row reaches
// none.
//
// The caller skips it for a holder, whose own fork may not widen its boundary, and records
// the paths it returns as the row's SoftPaths: they overlap nothing until the holder writes.
func ExtendFootprint(root string, row job.Job, projects []*types.Project, referrers func(file, declaration string) map[string]int) (paths, notes []string) {
	if row.ReadOnly || len(row.Paths.Write) == 0 {
		return nil, nil
	}
	beside := map[string]bool{}
	for _, f := range writtenFiles(root, row) {
		beside[path.Dir(f)] = true
	}
	var fromSource func(file, declaration string) map[string]int
	if referrers != nil {
		fromSource = func(file, declaration string) map[string]int {
			if types.IsTestPath(file) {
				return nil
			}
			return referrers(file, declaration)
		}
	}
	for _, f := range ReferencedOutside(root, row, projects, fromSource) {
		if types.IsTestPath(f.File) && beside[path.Dir(f.File)] {
			paths = append(paths, f.File)
			notes = append(notes, fmt.Sprintf("%s added to the write paths: it tests what %s defines", f.File, strings.Join(f.Uses, ", ")))
		}
	}
	return paths, notes
}

// FootprintMayExtend reports whether [ExtendFootprint] could add a path to row, so a caller
// loads the symbol graph its referrers come from only when it could: a written file is not
// a test, and a directory holding a written file holds a test file. It reads the
// directories and not the index, so the answer does not move with the index's temperature.
// A directory it cannot read counts as holding a test.
func FootprintMayExtend(root string, row job.Job) bool {
	if row.ReadOnly || len(row.Paths.Write) == 0 {
		return false
	}
	written := writtenFiles(root, row)
	if !slices.ContainsFunc(written, func(f string) bool { return !types.IsTestPath(f) }) {
		return false
	}
	dirs := map[string]bool{}
	for _, f := range written {
		dirs[path.Dir(f)] = true
	}
	for dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return true
		}
		if slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return !e.IsDir() && types.IsTestPath(path.Join(dir, e.Name())) }) {
			return true
		}
	}
	return false
}

// integratorRuns is the reason given for a gate an
// integrating ancestor runs.
const integratorRuns = "the integrator runs it"

// gateOwner is who meets the gates and outputs a writing
// row leaves out.
type gateOwner struct {
	// runs is the reason a GateNotRun deviation gives,
	// empty when no owner is declared.
	runs string
}

// declaredGateOwner is the owner the workspace declares for
// row's gates. ci.remote_gate runs ci on every pushed commit.
// Failing that, an integrating ancestor gates what it merges.
// An integration job is that owner itself, so it has none.
func declaredGateOwner(root string, rows []job.Job, row job.Job) gateOwner {
	if row.Integrating() {
		return gateOwner{}
	}
	cfg, err := config.LoadWorkspaceOnly(root)
	switch {
	case err != nil:
		// [broadClaimProblems] reports the config that did not load; no owner is guessed.
		return gateOwner{}
	case cfg.CI.RemoteGate != "":
		return gateOwner{runs: cfg.CI.RemoteGate + " runs it"}
	case row.Parent == "":
		return gateOwner{}
	}
	lineage := job.Ancestors(rows, row.Parent)
	if i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == row.Parent }); i >= 0 {
		lineage = append(lineage, rows[i])
	}
	if slices.ContainsFunc(lineage, job.Job.Integrating) {
		return gateOwner{runs: integratorRuns}
	}
	return gateOwner{}
}

// IntegratorDeviations is one deviation per output a target regenerates from row's write paths
// that sits outside them: the integrator regenerates it after the job. An output a deny
// path touches stays its owner's, and one a deviation of row accepts already needs no new one.
func IntegratorDeviations(root string, row job.Job, projects []*types.Project, ignored func([]string) ([]string, error)) []job.Deviation {
	var deviations []job.Deviation
	for _, o := range RegeneratedOutside(root, row, projects, ignored) {
		glob, _, _ := strings.Cut(o.Output, " !")
		problem := types.JobProblem{Code: types.OutputOutsideWritePaths, Subject: o.Output}
		accepted := slices.ContainsFunc(row.Deviations, func(w job.Deviation) bool { return types.DeviationPermits(w, problem) })
		if accepted || slices.ContainsFunc(row.Paths.Deny, func(d string) bool { return job.PathsIntersect(d, glob) }) {
			continue
		}
		deviations = append(deviations, job.Deviation{Code: job.Code(types.OutputOutsideWritePaths), Subject: o.Output, Reason: job.ReasonIntegratorRegenerates})
	}
	return deviations
}

// validateDryFork is the dry run of a fork: the plan as store reads it, the merge's checkpoint
// and symbol postconditions settled as a fork settles them, and [validateMerge] over a dry view of
// store, so nothing is written, no release is requested, no sweep runs and no worktree is
// made. The error is only a store that could not be read.
func validateDryFork(ctx context.Context, store *Store, id string, merge func(*job.Job)) (types.JobValidation, error) {
	plan, err := PlanFork(ctx, store, id, merge, config.Jobs{}, nil, true)
	return plan.Validation, err
}

// ForkPlan is a fork's merge settled and validated once by
// [PlanFork]. A dry run prints its Validation, and [WriteFork]
// writes its merge, so the two cannot differ.
type ForkPlan struct {
	Validation types.JobValidation
	id         string
	merge      func(*job.Job)
	read       planRead
}

// PlanFork settles the merge into job id and validates it once:
// its checkpoint expanded, its symbol postconditions resolved. With dry it
// reads store's dry view and writes nothing. Without, it reads as
// a real fork: the sweep writes, and a refusal asks a holder to
// release a file the fork wanted. The error is only a store that
// could not be read.
func PlanFork(ctx context.Context, store *Store, id string, merge func(*job.Job), limits config.Jobs, read SymbolReader, dry bool) (ForkPlan, error) {
	if dry {
		store = store.dry()
	}
	rows, err := store.List(ctx)
	if err != nil {
		return ForkPlan{}, err
	}
	v, settled := validateFork(ctx, store, rows, id, merge, limits, read)
	return ForkPlan{Validation: v, id: id, merge: settled, read: readPlan(rows)}, nil
}

// WriteFork stores plan's merge and then amend, which may be nil.
// amend sets only what the fork learned after validating, such as
// the checkout it made. Nothing is written while a problem
// remains, or once the plan the fork was judged against moved.
func WriteFork(ctx context.Context, store *Store, plan ForkPlan, amend func(*job.Job), limits config.Jobs) (job.Job, error) {
	if err := plan.Validation.Error(); err != nil {
		return job.Job{}, err
	}
	rows, err := store.List(ctx)
	if err != nil {
		return job.Job{}, err
	}
	if moved := plan.read.movedIn(readPlan(rows), plan.id); moved != "" {
		return job.Job{}, fmt.Errorf("job: %s changed after %s was validated, so the plan it was judged against no longer stands: fork it again", moved, plan.id)
	}
	merge := plan.merge
	if amend != nil {
		merge = func(u *job.Job) {
			plan.merge(u)
			amend(u)
		}
	}
	return writeMerge(ctx, store, plan.id, merge, limits, plan.Validation.WriteProof)
}

// planRead is what a fork's validation read of each live row of
// the plan, by id. A heartbeat or a result moves none of it.
type planRead map[string]planEntry

// planEntry is one live row as a fork's validation reads it.
type planEntry struct {
	// rest is the row's parent, integrations, deny paths and
	// checkout, encoded so a change to any of them compares.
	rest  string
	write []string
}

func readPlan(rows []job.Job) planRead {
	read := planRead{}
	for _, r := range rows {
		if r.State.Terminal() {
			continue
		}
		rest, _ := codec.Marshal([]any{r.Parent, r.Integrates, r.Paths.Deny, WorkingCheckout(r.CheckoutRoot, r)})
		read[r.ID] = planEntry{rest: string(rest), write: r.Paths.Write}
	}
	return read
}

// movedIn is the id of a row whose entry in now r did not read,
// or "". A row that ended or narrowed its write paths only lifts a
// constraint. The forked row id ending makes its update a new row,
// judged whole.
func (r planRead) movedIn(now planRead, id string) string {
	if _, live := r[id]; live {
		if _, still := now[id]; !still {
			return id
		}
	}
	for _, at := range slices.Sorted(maps.Keys(now)) {
		was, read := r[at]
		is := now[at]
		narrowed := !slices.ContainsFunc(is.write, func(p string) bool {
			return !slices.ContainsFunc(was.write, func(held string) bool { return job.PathWithin(p, held) })
		})
		if !read || was.rest != is.rest || !narrowed {
			return at
		}
	}
	return ""
}

// validateFork settles a fork's merge (its checkpoint expanded, its check postconditions' accept
// resolved, its symbol postconditions resolved) and validates it, returning the merge a fork
// writes. A merge that cannot be settled is a problem in the same pass, and the rest is
// judged on the merge as given. A check postcondition naming an unknown basis stays as declared,
// so validation reports it as a JobPostconditionInvalid problem.
func validateFork(ctx context.Context, store *Store, rows []job.Job, id string, merge func(*job.Job), limits config.Jobs, read SymbolReader) (types.JobValidation, func(*job.Job)) {
	declared := merge
	merge = func(u *job.Job) {
		declared(u)
		u.Postconditions, _ = resolveAccept(u.Postconditions)
	}
	merge, ps := store.declaredMerge(ctx, rows, id, merge)
	if resolved, err := resolveMerge(ctx, store, rows, id, merge, read); err != nil {
		ps = append(ps, resolveProblem(err))
	} else {
		merge = resolved
	}
	v := validateMerge(ctx, store, rows, id, merge, limits, read)
	v.Problems = append(ps, v.Problems...)
	return v, merge
}

// InWorkspace returns a Store over s's file whose validation reads ws: its projects for a
// check's project and target, the outputs a write path regenerates and the ci targets over
// it, and its ignore filter. The view takes its own mutex, as [Store.As] does.
func (s *Store) InWorkspace(ws Workspace) *Store {
	v := s.view()
	v.workspace = &ws
	return v
}

// loadedWorkspace is the workspace store validates against, or why it has none.
func (s *Store) loadedWorkspace() (Workspace, string) {
	switch {
	case s == nil || s.workspace == nil:
		return Workspace{}, "this door opened the job store without a workspace"
	case s.workspace.Unavailable != "":
		return Workspace{}, "the workspace did not load: " + s.workspace.Unavailable
	}
	return *s.workspace, ""
}

// workspaceProblems is every problem of judged that only the loaded workspace shows: a
// check naming a project or target it lacks, an output regenerated from judged's write
// paths that sits outside every write path of merged, a ci target over judged's write paths
// that no check or postcondition of merged runs, and a write path broader than it allows. Each check
// that could not run is named, with why, in the second result.
// rows is the plan [declaredGateOwner] reads.
func workspaceProblems(ctx context.Context, store *Store, rows []job.Job, merged, judged job.Job, updates bool, read SymbolReader) (types.JobProblems, []string) {
	// Soft paths come from the symbol index, so a list judged on them would
	// change with the index's temperature.
	judged.Paths.Write = slices.DeleteFunc(slices.Clone(judged.Paths.Write), func(p string) bool {
		return slices.Contains(merged.Paths.Soft, p)
	})
	ws, why := store.loadedWorkspace()
	writes := !judged.ReadOnly && len(judged.Paths.Write) > 0
	if why != "" {
		var unchecked []string
		if slices.ContainsFunc(judged.EffectivePostconditions(), func(g job.Postcondition) bool { return g.Kind == job.PostconditionKindCheck && !g.Check.IsZero() }) {
			unchecked = append(unchecked, "the check's project, target and test-selection semantics: "+why)
		}
		if writes {
			unchecked = append(unchecked,
				"outputs regenerated outside the write paths: "+why,
				"ci targets over the write paths that nothing runs: "+why,
				"write path breadth: "+why)
		}
		return nil, unchecked
	}
	ps, unchecked := checkTargetProblems(ws, judged)
	unchecked = append(unchecked, uncheckedTestSelection(ws, judged, ps)...)
	ps = onRow(ps, judged, merged)
	if !writes {
		return ps, unchecked
	}
	// The sources are judged's paths, and the checks that run gates are all of merged's.
	gated := job.Job{ID: merged.ID, Paths: job.Paths{Write: judged.Paths.Write, Deny: merged.Paths.Deny}, Check: merged.Check, Postconditions: merged.Postconditions}
	regenerated := RegeneratedOutside(store.root, gated, ws.Projects, ws.Ignored)
	if updates {
		// An output a path the row already held covers is inside its write paths.
		whole := RegeneratedOutside(store.root, merged, ws.Projects, ws.Ignored)
		regenerated = slices.DeleteFunc(regenerated, func(o RegeneratedOutput) bool {
			return !slices.ContainsFunc(whole, func(w RegeneratedOutput) bool { return w.Output == o.Output && w.Target == o.Target })
		})
	}
	owner := declaredGateOwner(store.root, rows, merged)
	ps = append(ps, regeneratedProblems(regenerated)...)
	gates, undecided := unrunGates(store.root, gated, ws.Projects, regenerated, gradeWith(store.checkGrader(), store.root, merged))
	ps = append(ps, unrunGateProblems(gates, owner)...)
	unchecked = append(unchecked, undecided...)
	ps = append(ps, onRow(broadClaimProblems(ctx, store.root, judged, ws, read), judged, merged)...)
	return ps, unchecked
}

// checkTargetProblems is every check of row naming a project ws does not have, or a target
// its project does not run, as JobCheckUnrunnable. A project that binds spells it carries
// unresolved cannot tell an op from a typo, so a name its magusfile does not export is
// named unchecked instead.
func checkTargetProblems(ws Workspace, row job.Job) (types.JobProblems, []string) {
	var (
		ps        types.JobProblems
		unchecked []string
	)
	checks, err := effectiveChecks(row)
	if err != nil {
		return types.JobProblems{unreadableCheckProblem(err)}, nil
	}
	for _, gc := range checks {
		g, check := gc.Postcondition, gc.Check
		if g.Kind != job.PostconditionKindCheck || check.Script != "" || check.Target == "" {
			continue
		}
		field := postconditionCheckField(row, g)
		project := cmp.Or(strings.TrimSpace(check.Project), ".")
		i := slices.IndexFunc(ws.Projects, func(p *types.Project) bool { return p.Path == project })
		if i < 0 {
			paths := make([]string, len(ws.Projects))
			for k, p := range ws.Projects {
				paths[k] = p.Path
			}
			slices.Sort(paths)
			ps = append(ps, types.JobProblem{
				Code: types.JobCheckUnrunnable, Field: field + ".project",
				Message: fmt.Sprintf("`%s` names project %q, which this workspace does not have", CheckCommand(check), project),
				Hint:    "name one of its projects: " + listNames(paths),
			})
			continue
		}
		p := ws.Projects[i]
		spell, name, qualified := strings.Cut(check.Target, "::")
		if !qualified {
			spell, name = "", check.Target
		}
		name, _, _ = strings.Cut(name, ":")
		name = types.Normalize(name)
		if spell == "" && slices.Contains(p.MagusfileTargets, name) {
			continue
		}
		if spellsUnresolved(p) {
			unchecked = append(unchecked, fmt.Sprintf("the target of `%s`: project %q binds spells it carries unresolved, so their ops cannot be listed", CheckCommand(check), project))
			continue
		}
		var targets []string
		if spell == "" {
			targets = slices.Clone(p.MagusfileTargets)
		}
		bound := false
		for _, s := range p.ResolvedSpells {
			if spell != "" && s.Name() != spell {
				continue
			}
			bound = true
			for _, op := range s.Targets() {
				targets = append(targets, types.Normalize(op))
			}
		}
		if spell != "" && !bound {
			ps = append(ps, types.JobProblem{
				Code: types.JobCheckUnrunnable, Field: field + ".target",
				Message: fmt.Sprintf("`%s` names spell %q, which project %q does not bind", CheckCommand(check), spell, project),
				Hint:    "name a spell it binds: " + listNames(p.Spells),
			})
			continue
		}
		if slices.Contains(targets, name) {
			continue
		}
		slices.Sort(targets)
		ps = append(ps, types.JobProblem{
			Code: types.JobCheckUnrunnable, Field: field + ".target",
			Message: fmt.Sprintf("`%s` names target %q, which project %q does not run", CheckCommand(check), name, project),
			Hint:    "name one of its targets: " + listNames(slices.Compact(targets)),
		})
	}
	return ps, unchecked
}

// maxListedNames bounds the names a fix lists: a dozen show the vocabulary.
const maxListedNames = 12

// listNames renders names for a fix, cut to maxListedNames.
func listNames(names []string) string {
	if len(names) > maxListedNames {
		return fmt.Sprintf("%s and %d more", strings.Join(names[:maxListedNames], ", "), len(names)-maxListedNames)
	}
	return strings.Join(names, ", ")
}

// regeneratedProblems is each output as OutputOutsideWritePaths, its Subject the output.
func regeneratedProblems(outputs []RegeneratedOutput) types.JobProblems {
	ps := make(types.JobProblems, 0, len(outputs))
	for _, o := range outputs {
		verb := "regenerates " + o.Output
		if o.InPlace {
			verb = fmt.Sprintf("rewrites %s in place", o.Output)
		}
		ps = append(ps, types.JobProblem{
			Code: types.OutputOutsideWritePaths, Field: "paths.write", Subject: o.Output,
			Message: fmt.Sprintf("%s %s from %s, inside the write paths, and %s sits outside them", o.Target, verb, o.Source, o.Output),
			Hint: fmt.Sprintf(`add %q to paths.write, or permit it in deviations: {"code":%q,"subject":%q,"reason":"the root regenerates it"}`,
				o.Output, types.OutputOutsideWritePaths, o.Output),
			Deviation: &job.Deviation{Code: job.Code(types.OutputOutsideWritePaths), Subject: o.Output, Reason: "the root regenerates it"},
		})
	}
	return ps
}

// unrunGateProblems is each gate as GateNotRun, its Subject `<target> <project>`. A gate no
// worker may run is the integrator's and raises nothing. With a declared owner, the fix is
// the deviation naming it.
func unrunGateProblems(gates []UnrunGate, owner gateOwner) types.JobProblems {
	ps := make(types.JobProblems, 0, len(gates))
	for _, g := range gates {
		if g.IntegratorGates {
			continue
		}
		subject := fmt.Sprintf("%s %s", g.Target, g.Project)
		postcondition, _ := codec.Marshal(g.Postcondition)
		p := types.JobProblem{
			Code: types.GateNotRun, Field: "postconditions", Subject: subject,
			Message: fmt.Sprintf("ci runs `%s` over the write paths (it reads %s), and neither the check nor a postcondition runs it", subject, g.Source),
			Hint: fmt.Sprintf(`add the postcondition %s to postconditions, or permit it in deviations: {"code":%q,"subject":%q,"reason":"the root runs it"}`,
				postcondition, types.GateNotRun, subject),
			Postcondition: &g.Postcondition,
		}
		if g.Target != lintTarget && owner.runs != "" {
			p.Postcondition = nil
			p.Deviation = &job.Deviation{Code: job.Code(types.GateNotRun), Subject: subject, Reason: owner.runs}
			p.Hint = fmt.Sprintf(`permit it in deviations, since %s: {"code":%q,"subject":%q,"reason":%q}`, owner.runs, types.GateNotRun, subject, owner.runs)
		}
		ps = append(ps, p)
	}
	return ps
}

// rowDeclaration is row as the record that would declare it, for the shape checks
// [job.Declaration.Problems] makes.
func rowDeclaration(row job.Job) job.Declaration {
	rec := job.Declaration{
		ID: row.ID, Parent: row.Parent, Agent: row.Agent.Clone(), Checkpoint: row.Checkpoint,
		Paths: job.Paths{Write: row.Paths.Write, Read: row.Paths.Read, Deny: row.Paths.Deny}, DependsOn: row.DependsOn,
		Breaks: row.Breaks, Postconditions: row.Postconditions, State: row.State, ReadOnly: row.ReadOnly,
		MaxAttempts: row.MaxAttempts, MaxClaims: row.MaxClaims, Deviations: row.Deviations,
		Delivery: row.Delivery, ParentHead: row.ParentHead,
	}
	// A row stored before the check record carries only the rendered line; one it could not
	// parse was refused at the door, so a line that fails here declares no check.
	rec.Check = row.Check
	if row.Check.IsZero() && strings.TrimSpace(row.Validation) != "" {
		if c, err := ParseCheckCommand(row.Validation); err == nil {
			if raw, err := types.EncodeLeaseCheck(c); err == nil {
				rec.Check = raw
			}
		}
	}
	return rec
}

// RetryDeclaration is the spec row was declared with, for forking it again by id alone: every
// field [job.Declaration.Apply] writes, with no state, so the retry is a new attempt.
func RetryDeclaration(row job.Job) job.Declaration {
	rec := rowDeclaration(row)
	rec.Schema = job.Schema{Version: job.APIVersion}
	rec.State = ""
	rec.Paths.Soft, rec.Integrates = row.Paths.Soft, row.Integrates
	rec.BriefFormat, rec.Follows = row.BriefFormat, row.Follows
	if row.Weight != 0 {
		rec.Weight = &row.Weight
	}
	return rec
}

// newProblems is the problems of after that before does not show.
func newProblems(before, after types.JobProblems) types.JobProblems {
	return slices.DeleteFunc(after, func(p types.JobProblem) bool { return slices.Contains(before, p) })
}

// onRow moves each field of ps found on part, a row cut to what a merge adds, to the index
// the same entry has in whole: a write or deny path by its value, a postcondition by its id.
func onRow(ps types.JobProblems, part, whole job.Job) types.JobProblems {
	for i, p := range ps {
		list, rest, ok := strings.Cut(p.Field, "[")
		if !ok {
			continue
		}
		num, tail, ok := strings.Cut(rest, "]")
		k, err := strconv.Atoi(num)
		if !ok || err != nil || k < 0 {
			continue
		}
		at := -1
		switch list {
		case "paths.write":
			if k < len(part.Paths.Write) {
				at = slices.Index(whole.Paths.Write, part.Paths.Write[k])
			}
		case "paths.deny":
			if k < len(part.Paths.Deny) {
				at = slices.Index(whole.Paths.Deny, part.Paths.Deny[k])
			}
		case "postconditions":
			if k < len(part.Postconditions) {
				at = slices.IndexFunc(whole.Postconditions, func(g job.Postcondition) bool { return g.ID == part.Postconditions[k].ID })
			}
		}
		if at >= 0 {
			ps[i].Field = fmt.Sprintf("%s[%d]%s", list, at, tail)
		}
	}
	return ps
}

// postconditionCheckField is the field of row that declares postcondition's check: `check` for the row's own,
// `postconditions[i].check` for a postcondition's.
func postconditionCheckField(row job.Job, postcondition job.Postcondition) string {
	if postcondition.ID == job.PrimaryPostconditionID && !row.Check.IsZero() {
		return "check"
	}
	if i := slices.IndexFunc(row.Postconditions, func(g job.Postcondition) bool { return g.ID == postcondition.ID }); i >= 0 {
		return fmt.Sprintf("postconditions[%d].check", i)
	}
	return "check"
}

// resolveProblem is a merge's symbol postconditions that could not be settled (see
// [ResolveSymbolPostconditions] and [PlaceDerivedClaims]) as a problem, under the code the failure
// carries, else JobPostconditionInvalid.
func resolveProblem(err error) types.JobProblem {
	p := types.JobProblem{Code: types.JobPostconditionInvalid, Field: "postconditions", Message: strings.TrimPrefix(err.Error(), "job: "),
		Hint: fmt.Sprintf("declare each signature postcondition's signature, or write the symbol, run `%s` and fork again", hint.GraphBuild)}
	var coded *types.DiagnosticError
	if errors.As(err, &coded) {
		p.Code, p.Message = coded.Code, coded.Msg
		if p.Code == types.WritePathClaimUngradable {
			p.Field, p.Hint = "paths.write", "claim the declaration in paths.write yourself"
		}
	}
	return p
}

// addedWritePathProblems holds the write paths a write adds to a job to the rules a fork's
// are held to: no directory, no claim a footprint cannot grade, and no project
// configuration held whole. A rule that held only at creation would depend on which call
// wrote the path. added is the job's row carrying only the write paths the write adds, so a
// path the row already held never blocks an unrelated write.
func addedWritePathProblems(ctx context.Context, store *Store, added job.Job, integrates []string) types.JobProblems {
	if len(added.Paths.Write) == 0 {
		return nil
	}
	ps := mergedDirectoryProblems(ctx, store, added, integrates)
	ps = append(ps, ungradableClaimProblems(ctx, store, added)...)
	return append(ps, wholeFileConfigClaimProblems(ctx, store, added)...)
}

// mergedDirectoryProblems is [directoryWritePathProblems] for the tree that
// merging integrates leaves. A directory here that a branch replaces with a
// file or a symlink names one file. A branch the VCS cannot compare keeps the
// refusal.
func mergedDirectoryProblems(ctx context.Context, store *Store, row job.Job, integrates []string) types.JobProblems {
	ps := directoryWritePathProblems(store, row)
	if len(ps) == 0 || len(integrates) == 0 {
		return ps
	}
	leaves := branchLeaves(ctx, store.root, integrates)
	row.Paths.Write = slices.Clone(row.Paths.Write)
	for i, decl := range row.Paths.Write {
		if p, _ := job.SplitClaim(decl); leaves[path.Clean(strings.TrimSpace(p))] {
			// Blank, not removed: a problem's field is the entry's index.
			row.Paths.Write[i] = ""
		}
	}
	return directoryWritePathProblems(store, row)
}

// branchLeaves is every path integrates change against the current revision.
// Such a path is no directory there: a diff lists no trees.
func branchLeaves(ctx context.Context, root string, integrates []string) map[string]bool {
	driver, _ := resolveDriver(ctx, root)
	if driver == nil {
		return nil
	}
	head, err := driver.FindCommit(ctx, root, "")
	if err != nil {
		return nil
	}
	leaves := map[string]bool{}
	for _, branch := range integrates {
		changed, err := driver.DiffTrees(ctx, root, head.ID, branch)
		if err != nil {
			continue
		}
		for _, p := range changed {
			leaves[p] = true
		}
	}
	return leaves
}

// ungradedProblems finds a writing job that declares neither a check nor a postcondition, as
// JobUngraded: `job wait` could never pass it, so the row would end by `job exit` on the
// holder's word. A read-only job writes nothing to grade and is exempt.
func ungradedProblems(row job.Job) types.JobProblems {
	if row.ReadOnly || len(row.Paths.Write) == 0 || !row.Check.IsZero() || strings.TrimSpace(row.Validation) != "" || len(row.Postconditions) > 0 {
		return nil
	}
	return types.JobProblems{{
		Code: types.JobUngraded, Field: "check",
		Message: fmt.Sprintf("%s writes %s and declares neither a check nor a postcondition, so `%s` has nothing to grade it by",
			row.ID, strings.Join(row.Paths.Write, ", "), hint.JobWait.With(row.ID)),
		Hint: fmt.Sprintf(`add "check" (the target that proves it, as "<target> <project>") or "postconditions" to the record, such as`+
			` {"id":"done","kind":"paths","expect":"changed","paths":["<glob>"]} (`+"`%s`"+` prints every field),`+
			` or mark a job that writes nothing "read_only"`, hint.JobFork.With("--schema")),
	}}
}

// unscopedProblems finds a job that is neither read-only nor handed a write path, as
// JobUngraded. The guard scopes nothing by an empty write set, so its holder could write
// anywhere, and `job wait` refuses a result that changed nothing on a row that is not
// read-only: a scout declared this way can only end no_return.
func unscopedProblems(row job.Job) types.JobProblems {
	if row.ReadOnly || len(row.Paths.Write) > 0 {
		return nil
	}
	return types.JobProblems{{
		Code: types.JobUngraded, Field: "paths.write",
		Message: fmt.Sprintf("%s names no write paths and is not read-only, so nothing bounds what its holder writes", row.ID),
		Hint: fmt.Sprintf(`add the paths it writes to paths.write (--write-paths), or mark a job that only reads "read_only" (--read-only); a read-only job passes only with a`+
			` script check, {"check": {"script": "<probe>.buzz"}}, whose result cites the ref `+"`%s`"+` prints, or its holder ends it with `+"`%s`",
			hint.Buzz.With("--record", "<probe>.buzz"), hint.JobExit.With(row.ID)),
	}}
}

// forkLimitProblems finds a fork that would put the job deeper below its root, or leave its
// root's tree holding more live jobs, than the workspace's jobs section allows, as
// JobForkLimitExceeded. rows is the plan before the fork; a zero limit is unlimited.
//
// Read before the store's lock is taken, so two concurrent forks can each pass a max_live
// the pair of them exceeds. A limit here is a brake on a runaway fan-out, not a quota.
func forkLimitProblems(rows []job.Job, id, parent string, limits config.Jobs) types.JobProblems {
	var ps types.JobProblems
	depth, root := 0, id
	if parent != "" {
		ancestors := job.Ancestors(rows, parent)
		depth = 1 + len(ancestors)
		root = parent
		if len(ancestors) > 0 {
			root = ancestors[len(ancestors)-1].ID
		}
	}
	if limits.MaxDepth > 0 && depth > limits.MaxDepth {
		ps = append(ps, types.JobProblem{
			Code: types.JobForkLimitExceeded, Field: "parent",
			Message: fmt.Sprintf("%s would sit %d level(s) below its root job %s, and jobs.max_depth in magus.yaml allows %d",
				id, depth, root, limits.MaxDepth),
			Hint: "name a parent nearer the root job, or raise jobs.max_depth in magus.yaml",
		})
	}
	if limits.MaxLive <= 0 {
		return ps
	}
	live := 1
	for _, r := range rows {
		if r.ID == id || !r.State.Live() {
			continue
		}
		rowRoot := r.ID
		if ancestors := job.Ancestors(rows, r.ID); len(ancestors) > 0 {
			rowRoot = ancestors[len(ancestors)-1].ID
		}
		if rowRoot == root {
			live++
		}
	}
	if live > limits.MaxLive {
		ps = append(ps, types.JobProblem{
			Code: types.JobForkLimitExceeded, Field: "parent",
			Message: fmt.Sprintf("forking %s would make %d live jobs under root job %s, and jobs.max_live in magus.yaml allows %d",
				id, live, root, limits.MaxLive),
			Hint: fmt.Sprintf("end one with `%s` first, or raise jobs.max_live in magus.yaml", hint.JobExit.With("<job>")),
		})
	}
	return ps
}

// ambiguousSymbolProblems finds each symbol a postcondition names whose bare name resolves to more
// than one definition, or a `<package>.<Name>` that resolves to none when the postcondition grades an
// existing symbol, as JobPostconditionInvalid, naming each. A name the reader cannot answer for is no
// problem: a cold graph must not block a declaration, and the postcondition refuses to certify later.
func ambiguousSymbolProblems(ctx context.Context, postconditions []job.Postcondition, read SymbolReader) types.JobProblems {
	if read == nil {
		return nil
	}
	var ps types.JobProblems
	for i, postcondition := range postconditions {
		if postcondition.Resolve().Kind != job.PostconditionKindSymbol {
			continue
		}
		for j, name := range postcondition.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			fact, ok := read(ctx, name, "")
			if _, _, qualified := packageQualified(name); qualified && ok && len(fact.DefinedIn) == 0 && slices.Contains([]job.PostconditionExpect{job.ExpectChanged, job.ExpectAbsent, job.ExpectUnreferenced}, postcondition.Resolve().Expect) {
				ps = append(ps, types.JobProblem{
					Code: types.JobPostconditionInvalid, Field: fmt.Sprintf("postconditions[%d].symbols[%d]", i, j),
					Message: fmt.Sprintf("postcondition %q expects %q to be %s, and the graph resolves it to no definition, so it would grade a name nothing defines",
						postcondition.ID, name, postcondition.Resolve().Expect),
					Hint: fmt.Sprintf("name a definition `%s` finds, as `<package>.<Name>` or a symbol id; with no symbol index built, `%s` builds one", hint.Refs.With(name), hint.GraphBuild),
				})
				continue
			}
			if !ok || len(fact.SameNameDefinitions) < 2 {
				continue
			}
			ps = append(ps, types.JobProblem{
				Code: types.JobPostconditionInvalid, Field: fmt.Sprintf("postconditions[%d].symbols[%d]", i, j),
				Message: fmt.Sprintf("postcondition %q names %q, which the graph resolves to %d definitions (%s), so it would grade whichever ranks first",
					postcondition.ID, name, len(fact.SameNameDefinitions), strings.Join(fact.SameNameDefinitions, ", ")),
				Hint: fmt.Sprintf("name one by the symbol id `%s` prints", hint.Refs.With(name)),
			})
		}
	}
	return ps
}
