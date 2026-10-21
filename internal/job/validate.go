package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// validateMerge holds a merge into job id, against the plan rows, to every rule a stored
// job meets, in one pass that never stops at the first problem. The result carries the row
// as the merge would leave it, every problem left once the row's waivers apply, the
// problems they waived, each check that could not run and why, and the write proof the row
// records.
//
// A merge that creates the row, or brings an ended one back, meets every rule. The record
// holds its own shape, a boundary and a grade (JobUngraded), the jobs limits
// (JobForkLimitExceeded) and unambiguous symbol goals (JobGoalInvalid). Its write paths
// name no directory (WritePathIsDirectory), no ungradable claim (WritePathClaimUngradable),
// no project configuration held whole (WritePathConfigWholeFile), no file the workspace
// loads while another job works in this checkout (CheckoutConfigHeld), no unordered shared
// file (WritePathFileShared) and nothing broader than the workspace allows
// (WritePathTooBroad). Its checks skip no test of the write paths' packages, are ones the
// guard lets the holder run, and name a project and target the workspace has
// (JobCheckUnrunnable). No regenerated output sits outside the write paths
// (OutputOutsideWritePaths), and no ci target over them goes unrun (GateNotRun).
//
// A merge onto a row that has not ended meets the same rules for what it ADDS: the goals,
// checks, write paths and deny paths it did not carry, the outputs and gates those bring
// in, and the shape problems the row did not already have, so a path the row already held
// never blocks an unrelated update. A path an entry the row held already covers
// ([types.PathWithin]) adds nothing, so a holder narrowing a glob to its files adds no path.
// Its proof is recomputed when its write paths change, and is empty otherwise, which keeps
// the one it holds.
//
// An overlap with a live job is recorded in the proof, never refused (ADR 0010). The
// workspace checks read store's [Workspace] (see [Store.InWorkspace]); with none, each is
// named in Unchecked instead. read may be nil where no graph is at hand, which skips the
// ambiguity check. The guard grades the checks against rows rather than sweep the store
// again.
func validateMerge(ctx context.Context, store *Store, rows []types.Job, id string, merge func(*types.Job), limits config.Jobs, read SymbolReader) types.JobValidation {
	graded := WithSnapshot(ctx, Snapshot{Rows: rows})
	prev, updates := mergeBase(rows, id, merge)
	merged := prev.Clone()
	merge(&merged)
	v := types.JobValidation{Job: merged}
	var ps types.JobProblems
	// judged is the row the workspace checks read: merged whole for a new row, its added
	// write paths with every check it runs for an update.
	judged := merged
	if updates {
		ps = append(ps, newProblems(rowDeclaration(prev).Problems(), rowDeclaration(merged).Problems())...)
		ps = append(ps, ungradedProblems(merged)...)
		var goals []types.Goal
		for _, gate := range merged.Goals {
			if !slices.ContainsFunc(prev.Goals, func(g types.Goal) bool { return g.ID == gate.ID }) {
				goals = append(goals, gate)
			}
		}
		ps = append(ps, onRow(ambiguousSymbolProblems(ctx, goals, read), types.Job{Goals: goals}, merged)...)
		added := slices.DeleteFunc(slices.Clone(merged.WritePaths), func(p string) bool {
			return slices.ContainsFunc(prev.WritePaths, func(held string) bool { return types.PathWithin(p, held) })
		})
		addedRow := types.Job{ID: id, WritePaths: added, Parent: merged.Parent, DependsOn: merged.DependsOn}
		ps = append(ps, onRow(addedWritePathProblems(ctx, store, addedRow), addedRow, merged)...)
		addedDenies := types.Job{ID: id, DenyPaths: slices.DeleteFunc(slices.Clone(merged.DenyPaths), func(p string) bool { return slices.Contains(prev.DenyPaths, p) })}
		ps = append(ps, onRow(ungradableClaimProblems(ctx, store, addedDenies), addedDenies, merged)...)
		ps = append(ps, onRow(unorderedFileShareProblems(ctx, store, rows, addedRow), addedRow, merged)...)
		// Judged on what the update changes, so a check the row already held never blocks an
		// unrelated update: a new check or goal is graded against every write path, an old
		// one only against the paths this update adds.
		checkChanged := !types.SameLeaseCheck(prev.Check, merged.Check)
		newChecks := types.Job{ID: id, WritePaths: merged.WritePaths, Goals: goals}
		if checkChanged {
			newChecks.Check = merged.Check
		}
		ps = append(ps, onRow(narrowedTestProblems(store, newChecks), newChecks, merged)...)
		if len(added) > 0 {
			oldChecks := types.Job{ID: id, WritePaths: added, Check: merged.Check, Goals: merged.Goals}
			ps = append(ps, onRow(narrowedTestProblems(store, oldChecks), oldChecks, merged)...)
		}
		checks := addedChecks(prev, merged, checkChanged)
		ps = append(ps, deniedCheckProblems(graded, store, checks)...)
		judged = types.Job{ID: id, WritePaths: added, Check: checks.Check, Goals: checks.Goals, ReadOnly: merged.ReadOnly}
		if !slices.Equal(prev.WritePaths, merged.WritePaths) {
			v.WriteProof = store.WriteProof(rows, merged)
		}
	} else {
		ps = append(ps, rowDeclaration(merged).Problems()...)
		ps = append(ps, unscopedProblems(merged)...)
		ps = append(ps, ungradedProblems(merged)...)
		ps = append(ps, forkLimitProblems(rows, id, merged.Parent, limits)...)
		ps = append(ps, ambiguousSymbolProblems(ctx, merged.Goals, read)...)
		ps = append(ps, directoryWritePathProblems(store, merged)...)
		ps = append(ps, ungradableClaimProblems(ctx, store, merged)...)
		ps = append(ps, wholeFileConfigClaimProblems(ctx, store, merged)...)
		ps = append(ps, sharedCheckoutProblems(store, rows, merged)...)
		ps = append(ps, unorderedFileShareProblems(ctx, store, rows, merged)...)
		ps = append(ps, narrowedTestProblems(store, merged)...)
		ps = append(ps, deniedCheckProblems(graded, store, merged)...)
		v.WriteProof = store.WriteProof(rows, merged)
	}
	found, unchecked := workspaceProblems(ctx, store, merged, judged, updates, read)
	ps = append(ps, found...)
	v.Unchecked = unchecked
	v.Problems, v.Waived = ps.Waive(merged.Waivers)
	return v
}

// ExtendFootprint is the write paths row's change reaches that it did not declare, and a
// note saying why for each: the test files beside a written file that reference what the
// write paths define ([ReferencedOutside]). A read-only row reaches none.
//
// The caller skips it for a holder, whose own fork may not widen its boundary.
func ExtendFootprint(root string, row types.Job, projects []*types.Project, referrers func(file, declaration string) map[string]int) (paths, notes []string) {
	if row.ReadOnly || len(row.WritePaths) == 0 {
		return nil, nil
	}
	beside := map[string]bool{}
	for _, f := range writtenFiles(root, row) {
		beside[path.Dir(f)] = true
	}
	for _, f := range ReferencedOutside(root, row, projects, referrers) {
		if types.IsTestPath(f.File) && beside[path.Dir(f.File)] {
			paths = append(paths, f.File)
			notes = append(notes, fmt.Sprintf("%s added to the write paths: it tests what %s defines", f.File, strings.Join(f.Uses, ", ")))
		}
	}
	return paths, notes
}

// IntegratorRegenerates is the reason a record gives for an output the integrator owns.
const IntegratorRegenerates = "the integrator regenerates it"

// IntegratorWaivers is one waiver per output a target regenerates from row's write paths
// that sits outside them: the integrator regenerates it after the job. An output a deny
// path touches stays its owner's, and one a waiver of row accepts already needs no new one.
func IntegratorWaivers(root string, row types.Job, projects []*types.Project, ignored func([]string) []string) []types.JobWaiver {
	var waivers []types.JobWaiver
	for _, o := range RegeneratedOutside(root, row, projects, ignored) {
		glob, _, _ := strings.Cut(o.Output, " !")
		problem := types.JobProblem{Code: types.OutputOutsideWritePaths, Subject: o.Output}
		accepted := slices.ContainsFunc(row.Waivers, func(w types.JobWaiver) bool { return w.Waives(problem) })
		if accepted || slices.ContainsFunc(row.DenyPaths, func(d string) bool { return types.PathsIntersect(d, glob) }) {
			continue
		}
		waivers = append(waivers, types.JobWaiver{Code: types.OutputOutsideWritePaths, Subject: o.Output, Reason: IntegratorRegenerates})
	}
	return waivers
}

// ValidateFork is the dry run of a fork: the plan as store reads it, the merge's checkpoint
// and symbol goals settled as a fork settles them, and [validateMerge] over a dry view of
// store, so nothing is written, no release is requested, no sweep runs and no worktree is
// made. The error is only a store that could not be read.
func ValidateFork(ctx context.Context, store *Store, id string, merge func(*types.Job), limits config.Jobs, read SymbolReader) (types.JobValidation, error) {
	dry := store.dry()
	rows, err := dry.List()
	if err != nil {
		return types.JobValidation{}, err
	}
	v, _ := validateFork(ctx, dry, rows, id, merge, limits, read)
	return v, nil
}

// validateFork settles a fork's merge (its checkpoint expanded, its symbol goals resolved)
// and validates it, returning the merge a fork writes. A merge that cannot be settled is a
// problem in the same pass, and the rest is judged on the merge as given.
func validateFork(ctx context.Context, store *Store, rows []types.Job, id string, merge func(*types.Job), limits config.Jobs, read SymbolReader) (types.JobValidation, func(*types.Job)) {
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
	return &Store{
		path: s.path, err: s.err, root: s.root, actor: s.actor, cacheDir: s.cacheDir, stateBase: s.stateBase,
		clock: s.clock, staleAfter: s.staleAfter, notices: s.notices, retain: s.retain, outputs: s.outputs,
		trailBase: s.trailBase, landed: s.landed, grader: s.grader, dryRun: s.dryRun, build: s.build,
		modelTiers: s.modelTiers, modelTierMatrix: s.modelTierMatrix, owners: s.owners, priceContext: s.priceContext,
		workspace: &ws,
	}
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
// that no check or goal of merged runs, and a write path broader than it allows. Each check
// that could not run is named, with why, in the second result.
func workspaceProblems(ctx context.Context, store *Store, merged, judged types.Job, updates bool, read SymbolReader) (types.JobProblems, []string) {
	ws, why := store.loadedWorkspace()
	writes := !judged.ReadOnly && len(judged.WritePaths) > 0
	if why != "" {
		var unchecked []string
		if slices.ContainsFunc(judged.EffectiveGoals(), func(g types.Goal) bool { return g.Kind == types.GoalKindCheck && g.Check.Target != "" }) {
			unchecked = append(unchecked, "the check's project and target: "+why)
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
	ps = onRow(ps, judged, merged)
	if !writes {
		return ps, unchecked
	}
	// The sources are judged's paths, and the checks that run gates are all of merged's.
	gated := types.Job{ID: merged.ID, WritePaths: judged.WritePaths, DenyPaths: merged.DenyPaths, Check: merged.Check, Goals: merged.Goals}
	regenerated := RegeneratedOutside(store.root, gated, ws.Projects, ws.Ignored)
	if updates {
		// An output a path the row already held covers is inside its write paths.
		whole := RegeneratedOutside(store.root, merged, ws.Projects, ws.Ignored)
		regenerated = slices.DeleteFunc(regenerated, func(o RegeneratedOutput) bool {
			return !slices.ContainsFunc(whole, func(w RegeneratedOutput) bool { return w.Output == o.Output && w.Target == o.Target })
		})
	}
	ps = append(ps, regeneratedProblems(regenerated)...)
	ps = append(ps, unrunGateProblems(UnrunGates(store.root, gated, ws.Projects, regenerated))...)
	ps = append(ps, onRow(broadClaimProblems(ctx, store.root, judged, ws, read), judged, merged)...)
	return ps, unchecked
}

// checkTargetProblems is every check of row naming a project ws does not have, or a target
// its project does not run, as JobCheckUnrunnable. A project that binds spells it carries
// unresolved cannot tell an op from a typo, so a name its magusfile does not export is
// named unchecked instead.
func checkTargetProblems(ws Workspace, row types.Job) (types.JobProblems, []string) {
	var (
		ps        types.JobProblems
		unchecked []string
	)
	for _, g := range row.EffectiveGoals() {
		if g.Kind != types.GoalKindCheck || g.Check.Script != "" || g.Check.Target == "" {
			continue
		}
		field := goalCheckField(row, g)
		project := cmp.Or(strings.TrimSpace(g.Check.Project), ".")
		i := slices.IndexFunc(ws.Projects, func(p *types.Project) bool { return p.Path == project })
		if i < 0 {
			paths := make([]string, len(ws.Projects))
			for k, p := range ws.Projects {
				paths[k] = p.Path
			}
			slices.Sort(paths)
			ps = append(ps, types.JobProblem{
				Code: types.JobCheckUnrunnable, Field: field + ".project",
				Message: fmt.Sprintf("`%s` names project %q, which this workspace does not have", g.Check.String(), project),
				Fix:     "name one of its projects: " + listNames(paths),
			})
			continue
		}
		p := ws.Projects[i]
		spell, name, qualified := strings.Cut(g.Check.Target, "::")
		if !qualified {
			spell, name = "", g.Check.Target
		}
		name, _, _ = strings.Cut(name, ":")
		name = types.Normalize(name)
		if spell == "" && slices.Contains(p.MagusfileTargets, name) {
			continue
		}
		if len(p.ResolvedSpells) == 0 && len(p.Spells) > 0 {
			unchecked = append(unchecked, fmt.Sprintf("the target of `%s`: project %q binds spells it carries unresolved, so their ops cannot be listed", g.Check.String(), project))
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
				Message: fmt.Sprintf("`%s` names spell %q, which project %q does not bind", g.Check.String(), spell, project),
				Fix:     "name a spell it binds: " + listNames(p.Spells),
			})
			continue
		}
		if slices.Contains(targets, name) {
			continue
		}
		slices.Sort(targets)
		ps = append(ps, types.JobProblem{
			Code: types.JobCheckUnrunnable, Field: field + ".target",
			Message: fmt.Sprintf("`%s` names target %q, which project %q does not run", g.Check.String(), name, project),
			Fix:     "name one of its targets: " + listNames(slices.Compact(targets)),
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
		ps = append(ps, types.JobProblem{
			Code: types.OutputOutsideWritePaths, Field: "write_paths", Subject: o.Output,
			Message: fmt.Sprintf("%s regenerates %s from %s, inside the write paths, and %s sits outside them", o.Target, o.Output, o.Source, o.Output),
			Fix: fmt.Sprintf(`add %q to write_paths, or waive it in waivers: {"code":%q,"subject":%q,"reason":"the root regenerates it"}`,
				o.Output, types.OutputOutsideWritePaths, o.Output),
		})
	}
	return ps
}

// unrunGateProblems is each gate as GateNotRun, its Subject `<target> <project>`.
func unrunGateProblems(gates []UnrunGate) types.JobProblems {
	ps := make(types.JobProblems, 0, len(gates))
	for _, g := range gates {
		subject := g.Target + " " + g.Project
		goal, _ := json.Marshal(g.Goal)
		ps = append(ps, types.JobProblem{
			Code: types.GateNotRun, Field: "goals", Subject: subject,
			Message: fmt.Sprintf("ci runs `%s` over the write paths (it reads %s), and neither the check nor a goal runs it", subject, g.Source),
			Fix: fmt.Sprintf(`add the goal %s to goals, or waive it in waivers: {"code":%q,"subject":%q,"reason":"the root runs it"}`,
				goal, types.GateNotRun, subject),
		})
	}
	return ps
}

// rowDeclaration is row as the record that would declare it, for the shape checks
// [types.Declaration.Problems] makes.
func rowDeclaration(row types.Job) types.Declaration {
	rec := types.Declaration{
		ID: row.ID, Parent: row.Parent, Criteria: row.Criteria, Checkpoint: row.Checkpoint,
		WritePaths: row.WritePaths, DenyPaths: row.DenyPaths, ReadPaths: row.ReadPaths, DependsOn: row.DependsOn,
		Breaks: row.Breaks, Model: row.Model, Goals: row.Goals, State: row.State, ReadOnly: row.ReadOnly,
		MaxAttempts: row.MaxAttempts, Waivers: row.Waivers,
	}
	// A stored row renders its check into Validation too, and a record carries one spelling.
	if row.Check != nil {
		rec.Check = row.Check
	} else {
		rec.Validation = row.Validation
	}
	return rec
}

// newProblems is the problems of after that before does not show.
func newProblems(before, after types.JobProblems) types.JobProblems {
	return slices.DeleteFunc(after, func(p types.JobProblem) bool { return slices.Contains(before, p) })
}

// onRow moves each field of ps found on part, a row cut to what a merge adds, to the index
// the same entry has in whole: a write or deny path by its value, a goal by its id.
func onRow(ps types.JobProblems, part, whole types.Job) types.JobProblems {
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
		case "write_paths":
			if k < len(part.WritePaths) {
				at = slices.Index(whole.WritePaths, part.WritePaths[k])
			}
		case "deny_paths":
			if k < len(part.DenyPaths) {
				at = slices.Index(whole.DenyPaths, part.DenyPaths[k])
			}
		case "goals":
			if k < len(part.Goals) {
				at = slices.IndexFunc(whole.Goals, func(g types.Goal) bool { return g.ID == part.Goals[k].ID })
			}
		}
		if at >= 0 {
			ps[i].Field = fmt.Sprintf("%s[%d]%s", list, at, tail)
		}
	}
	return ps
}

// goalCheckField is the field of row that declares goal's check: `check` for the row's own,
// `goals[i].check` for a goal's.
func goalCheckField(row types.Job, goal types.Goal) string {
	if goal.ID == types.PrimaryGoalID && row.Check != nil {
		return "check"
	}
	if i := slices.IndexFunc(row.Goals, func(g types.Goal) bool { return g.ID == goal.ID }); i >= 0 {
		return fmt.Sprintf("goals[%d].check", i)
	}
	return "check"
}

// resolveProblem is a merge's symbol goals that could not be settled (see
// [ResolveSymbolGoals] and [PlaceDerivedClaims]) as a problem, under the code the failure
// carries, else JobGoalInvalid.
func resolveProblem(err error) types.JobProblem {
	p := types.JobProblem{Code: types.JobGoalInvalid, Field: "goals", Message: strings.TrimPrefix(err.Error(), "job: "),
		Fix: fmt.Sprintf("declare each signature goal's signature, or write the symbol, run `%s` and fork again", hint.GraphBuild)}
	var coded *types.DiagnosticError
	if errors.As(err, &coded) {
		p.Code, p.Message = coded.Code, coded.Msg
		if p.Code == types.WritePathClaimUngradable {
			p.Field, p.Fix = "write_paths", "claim the declaration in write_paths yourself"
		}
	}
	return p
}

// addedWritePathProblems holds the write paths a write adds to a job to the rules a fork's
// are held to: no directory, no claim a footprint cannot grade, and no project
// configuration held whole. A rule that held only at creation would depend on which call
// wrote the path. added is the job's row carrying only the write paths the write adds, so a
// path the row already held never blocks an unrelated write.
func addedWritePathProblems(ctx context.Context, store *Store, added types.Job) types.JobProblems {
	if len(added.WritePaths) == 0 {
		return nil
	}
	ps := directoryWritePathProblems(store, added)
	ps = append(ps, ungradableClaimProblems(ctx, store, added)...)
	return append(ps, wholeFileConfigClaimProblems(ctx, store, added)...)
}

// ungradedProblems finds a writing job that declares neither a check nor a goal, as
// JobUngraded: `job wait` could never pass it, so the row would end by `job exit` on the
// holder's word. A read-only job writes nothing to grade and is exempt.
func ungradedProblems(row types.Job) types.JobProblems {
	if row.ReadOnly || len(row.WritePaths) == 0 || row.Check != nil || strings.TrimSpace(row.Validation) != "" || len(row.Goals) > 0 {
		return nil
	}
	return types.JobProblems{{
		Code: types.JobUngraded, Field: "check",
		Message: fmt.Sprintf("%s writes %s and declares neither a check nor a goal, so `%s` has nothing to grade it by",
			row.ID, strings.Join(row.WritePaths, ", "), hint.JobWait.With(row.ID)),
		Fix: fmt.Sprintf(`add "check" (the target that proves it, as "<target> <project>") or "goals" to the record, such as`+
			` {"id":"done","kind":"paths","expect":"changed","paths":["<glob>"]} (`+"`%s`"+` prints every field),`+
			` or mark a job that writes nothing "read_only"`, hint.JobFork.With("--schema")),
	}}
}

// unscopedProblems finds a job that is neither read-only nor handed a write path, as
// JobUngraded. The guard scopes nothing by an empty write set, so its holder could write
// anywhere, and `job wait` refuses a result that changed nothing on a row that is not
// read-only: a scout declared this way can only end no_return.
func unscopedProblems(row types.Job) types.JobProblems {
	if row.ReadOnly || len(row.WritePaths) > 0 {
		return nil
	}
	return types.JobProblems{{
		Code: types.JobUngraded, Field: "write_paths",
		Message: fmt.Sprintf("%s names no write paths and is not read-only, so nothing bounds what its holder writes", row.ID),
		Fix: fmt.Sprintf(`add the paths it writes to write_paths (--write-paths), or mark a job that only reads "read_only" (--read-only); a read-only job passes only with a`+
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
func forkLimitProblems(rows []types.Job, id, parent string, limits config.Jobs) types.JobProblems {
	var ps types.JobProblems
	depth, root := 0, id
	if parent != "" {
		ancestors := types.JobAncestors(rows, parent)
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
			Fix: "name a parent nearer the root job, or raise jobs.max_depth in magus.yaml",
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
		if ancestors := types.JobAncestors(rows, r.ID); len(ancestors) > 0 {
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
			Fix: fmt.Sprintf("end one with `%s` first, or raise jobs.max_live in magus.yaml", hint.JobExit.With("<job>")),
		})
	}
	return ps
}

// namesExistingSymbol reports whether a symbol goal of expect grades a symbol that exists
// when the job is declared: one a job is to create (present) is the exception, and a name
// that resolves to nothing under any other expectation is a typo that grades vacuously.
func namesExistingSymbol(expect types.GoalExpect) bool {
	return slices.Contains([]types.GoalExpect{types.ExpectChanged, types.ExpectAbsent, types.ExpectUnreferenced}, expect)
}

// ambiguousSymbolProblems finds each symbol a goal names whose bare name resolves to more
// than one definition, or a `<package>.<Name>` that resolves to none when the goal grades an
// existing symbol, as JobGoalInvalid, naming each. A name the reader cannot answer for is no
// problem: a cold graph must not block a declaration, and the goal refuses to certify later.
func ambiguousSymbolProblems(ctx context.Context, goals []types.Goal, read SymbolReader) types.JobProblems {
	if read == nil {
		return nil
	}
	var ps types.JobProblems
	for i, goal := range goals {
		if goal.Resolve().Kind != types.GoalKindSymbol {
			continue
		}
		for j, name := range goal.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			fact, ok := read(ctx, name, "")
			if _, _, qualified := packageQualified(name); qualified && ok && len(fact.DefinedIn) == 0 && namesExistingSymbol(goal.Resolve().Expect) {
				ps = append(ps, types.JobProblem{
					Code: types.JobGoalInvalid, Field: fmt.Sprintf("goals[%d].symbols[%d]", i, j),
					Message: fmt.Sprintf("goal %q expects %q to be %s, and the graph resolves it to no definition, so it would grade a name nothing defines",
						goal.ID, name, goal.Resolve().Expect),
					Fix: fmt.Sprintf("name a definition `%s` finds, as `<package>.<Name>` or a symbol id; with no symbol index built, `%s` builds one", hint.Refs.With(name), hint.GraphBuild),
				})
				continue
			}
			if !ok || len(fact.SameNameDefinitions) < 2 {
				continue
			}
			ps = append(ps, types.JobProblem{
				Code: types.JobGoalInvalid, Field: fmt.Sprintf("goals[%d].symbols[%d]", i, j),
				Message: fmt.Sprintf("goal %q names %q, which the graph resolves to %d definitions (%s), so it would grade whichever ranks first",
					goal.ID, name, len(fact.SameNameDefinitions), strings.Join(fact.SameNameDefinitions, ", ")),
				Fix: fmt.Sprintf("name one by the symbol id `%s` prints", hint.Refs.With(name)),
			})
		}
	}
	return ps
}
