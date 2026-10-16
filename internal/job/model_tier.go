package job

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// newModelTierRequest is what a matrix function reads to choose row's tier: the row's
// declared record, its parent's assigned rank, and the recorded outcomes in history.
//
// rows is the plan the row joins, which is where the parent's rank is read. WriteProjects
// stays empty: a project is a graph fact, which [Store] fills from Location.Owners.
func newModelTierRequest(row types.Job, rows []types.Job, set *types.ModelTierSet, history []types.ModelTierRecord) types.ModelTierRequest {
	req := types.ModelTierRequest{
		ID:            row.ID,
		Parent:        row.Parent,
		Criteria:      row.Criteria,
		ReadOnly:      row.ReadOnly,
		Goals:         slices.Clone(row.Goals),
		Kind:          modelTierKind(row),
		WritePaths:    slices.Clone(row.WritePaths),
		ReadPaths:     slices.Clone(row.ReadPaths),
		DependsOn:     slices.Clone(row.DependsOn),
		WriteFiles:    writeFileCount(row.WritePaths),
		DeclaredModel: row.Model,
		History:       slices.Clone(history),
	}
	if row.Check != nil {
		check := *row.Check
		check.Args = slices.Clone(check.Args)
		req.Check = &check
	}
	if row.Parent != "" {
		if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == row.Parent }); i >= 0 && rows[i].ModelTier != nil {
			rank := rows[i].ModelTier.Rank
			req.ParentRank = &rank
		}
	}
	if tier, _, ok, _ := declaredModelTier(set, row.Model); ok {
		rank := tier.Rank()
		req.DeclaredRank = &rank
	}
	return req
}

// modelTierKind is what row is judged by, the first that applies: a script check, symbol
// goals, path goals, a target check, else a reader of its prose.
func modelTierKind(row types.Job) types.JobKind {
	var symbol, paths, target bool
	for _, g := range row.EffectiveGoals() {
		g = g.Resolve()
		switch g.Kind {
		case types.GoalKindCheck:
			if g.Check.Script != "" {
				return types.JobKindScript
			}
			target = target || g.Check.Target != ""
		case types.GoalKindSymbol:
			symbol = true
		case types.GoalKindPaths:
			paths = true
		}
	}
	switch {
	case symbol:
		return types.JobKindSymbol
	case paths:
		return types.JobKindPaths
	case target:
		return types.JobKindCheck
	}
	return types.JobKindProse
}

// writeFileCount counts files, so a glob is one and a claim on a declaration counts as the
// file it names, once however many of its declarations the row claims.
func writeFileCount(writePaths []string) int {
	files := make(map[string]struct{}, len(writePaths))
	for _, p := range writePaths {
		file, _ := types.SplitClaim(p)
		files[file] = struct{}{}
	}
	return len(files)
}

// declaredModelTier is the tier a row's Model names, and false when Model is empty or names
// none. A model naming no tier of set is looked up in each host's alias list; host is the
// harness whose alias it is, empty when Model is a tier name. An alias that two hosts map
// to different tiers is an error, since the row would name no one tier.
func declaredModelTier(set *types.ModelTierSet, model string) (tier types.ModelTier, host string, ok bool, err error) {
	if set == nil || model == "" {
		return types.ModelTier{}, "", false, nil
	}
	if tier, perr := set.Parse(model); perr == nil {
		return tier, "", true, nil
	}
	for _, h := range set.Hosts() {
		t, found := set.Resolve(h, model)
		switch {
		case !found:
		case !ok:
			tier, host, ok = t, h.ID, true
		case t.Rank() != tier.Rank():
			return types.ModelTier{}, "", false, fmt.Errorf("the model %s is the %s tier of harness %s and the %s tier of harness %s: name a tier instead",
				model, tier.Name(), host, t.Name(), h.ID)
		}
	}
	return tier, host, ok, nil
}

// resolveModelTier decides row's model tier and says why. It runs once, when the row is
// created or its Model changes, and nothing re-runs it: a tier moves only when someone
// edits the row, the matrix or the declaration.
//
// The first step that answers decides, and the result records which:
//
//   - row: the row's Model names a tier of set, or a model some host maps to one. A Model
//     naming neither is kept as the row's word and takes the declared default's rank,
//     since hosts name models differently and a refusal would force a tier onto a model
//     that fits none.
//   - matrix: matrix answered. It may decline with nil. An error from it, or an answer
//     naming a tier outside set, a skill variant that is not one, or no reason, fails the
//     assignment, so a matrix that is wrong never silently falls through to the default.
//   - default: the declaration's default, with the skills the declaration gives it.
//
// A nil matrix declines. set must not be nil.
func resolveModelTier(ctx context.Context, set *types.ModelTierSet, matrix func(context.Context, types.ModelTierRequest) (*types.JobModelTier, error), req types.ModelTierRequest, row types.Job) (types.JobModelTier, error) {
	if set == nil {
		return types.JobModelTier{}, errors.New("job: no model tiers to assign from")
	}
	tier, host, ok, err := declaredModelTier(set, row.Model)
	if err != nil {
		return types.JobModelTier{}, fmt.Errorf("%s: %w", row.ID, err)
	}
	switch {
	case ok && host == "":
		return types.JobModelTier{
			Name:   tier.Name(),
			Rank:   tier.Rank(),
			Skills: set.Skills(tier),
			Source: types.ModelTierSourceRow,
			Reason: fmt.Sprintf("the job names the tier %s", tier.Name()),
		}, nil
	case ok:
		return types.JobModelTier{
			Name:   tier.Name(),
			Rank:   tier.Rank(),
			Skills: set.Skills(tier),
			Source: types.ModelTierSourceRow,
			Reason: fmt.Sprintf("the job names the model %s, which harness %s maps to the tier %s", row.Model, host, tier.Name()),
			Model:  row.Model,
		}, nil
	case row.Model != "":
		def := set.Default()
		return types.JobModelTier{
			Name:   def.Name(),
			Rank:   def.Rank(),
			Skills: set.Skills(def),
			Source: types.ModelTierSourceRow,
			Reason: fmt.Sprintf("the job names the model %s, which no model tier maps to", row.Model),
			Model:  row.Model,
		}, nil
	}
	if matrix != nil {
		answer, err := matrix(ctx, req)
		if err != nil {
			return types.JobModelTier{}, fmt.Errorf("job: choosing the model tier of %s: %w", row.ID, err)
		}
		if answer != nil {
			return checkMatrixAnswer(set, row.ID, *answer)
		}
	}
	def := set.Default()
	return types.JobModelTier{
		Name:   def.Name(),
		Rank:   def.Rank(),
		Skills: set.Skills(def),
		Source: types.ModelTierSourceDefault,
		Reason: "the declared default",
		Site:   set.Site(),
	}, nil
}

// assignModelTier is the tier for row, written into plan. It runs under [Store.mutate]'s
// lock with plan as it stood before the write, so the bound is read where it is spent.
func (s *Store) assignModelTier(ctx context.Context, row types.Job, plan []types.Job) (types.JobModelTier, error) {
	req := newModelTierRequest(row, plan, s.modelTiers, nil)
	if s.modelTierMatrix != nil && s.owners != nil && row.Model == "" && len(row.WritePaths) > 0 {
		projects, err := s.writeProjects(ctx, row)
		if err != nil {
			return types.JobModelTier{}, fmt.Errorf("finding the projects %s writes in: %w", row.ID, err)
		}
		req.WriteProjects = projects
	}
	assigned, err := resolveModelTier(ctx, s.modelTiers, s.modelTierMatrix, req, row)
	if err != nil {
		return types.JobModelTier{}, err
	}
	others := slices.DeleteFunc(slices.Clone(plan), func(r types.Job) bool { return r.ID == row.ID })
	if err := refuseModelTierLimits(others, assigned, s.modelTiers); err != nil {
		return types.JobModelTier{}, err
	}
	return assigned, nil
}

// writeProjects is the projects row's write paths fall in, through Location.Owners, which
// the caller has checked is set.
func (s *Store) writeProjects(ctx context.Context, row types.Job) ([]string, error) {
	files := make([]string, len(row.WritePaths))
	for i, p := range row.WritePaths {
		files[i], _ = types.SplitClaim(p)
	}
	return s.owners(ctx, files)
}

// refuseAboveParent refuses row's tier when it ranks above the tier row's parent in plan
// was forked at. Only a proven pair is judged: a parent missing from plan, recording no
// tier, or recording one set no longer declares at that rank bounds nothing. The way up is
// a person's: a child never outranks the work that handed it out.
func refuseAboveParent(set *types.ModelTierSet, row types.Job, plan []types.Job) error {
	if set == nil || row.Parent == "" || row.ModelTier == nil {
		return nil
	}
	i := slices.IndexFunc(plan, func(r types.Job) bool { return r.ID == row.Parent })
	if i < 0 {
		return nil
	}
	parent, ok := declaredTier(set, plan[i].ModelTier)
	if !ok {
		return nil
	}
	child, ok := declaredTier(set, row.ModelTier)
	if !ok {
		return nil
	}
	if child.Within(parent) == nil {
		return nil
	}
	err := fmt.Errorf("job: %s would run at model tier %s, and its parent %s was forked at %s, and a child never outranks its parent,"+
		" so fork it at %s or lower (`--model %s`), or a person forks it at %s as a row of its own",
		row.ID, row.ModelTier, row.Parent, plan[i].ModelTier, parent.Name(), parent.Name(), child.Name())
	return types.NonRetryable(err, types.RetryAfterPerson, "a person forks the higher-tier row")
}

// declaredTier is the tier record names in set, false when record is nil or set has no
// case of that name at that rank.
func declaredTier(set *types.ModelTierSet, record *types.JobModelTier) (types.ModelTier, bool) {
	if record == nil {
		return types.ModelTier{}, false
	}
	t, ok := set.At(record.Rank)
	if !ok || t.Name() != record.Name {
		return types.ModelTier{}, false
	}
	return t, true
}

// checkMatrixAnswer holds a matrix's answer to the declaration it chose from. The answer
// is stored as given once it agrees with set, with the source made the matrix's whatever
// the function wrote there.
func checkMatrixAnswer(set *types.ModelTierSet, id string, answer types.JobModelTier) (types.JobModelTier, error) {
	tier, err := set.Parse(answer.Name)
	if err != nil {
		return types.JobModelTier{}, fmt.Errorf("job: the model tier matrix chose for %s: %w", id, err)
	}
	if answer.Rank != tier.Rank() {
		return types.JobModelTier{}, fmt.Errorf("job: the model tier matrix chose %s for %s at rank %d, and the declaration ranks it %d", answer.Name, id, answer.Rank, tier.Rank())
	}
	if answer.Skills == "" || !answer.Skills.Valid() {
		return types.JobModelTier{}, fmt.Errorf("job: the model tier matrix chose skills %q for %s, and the variants are %s", answer.Skills, id, strings.Join(answer.Skills.Values(), ", "))
	}
	if answer.Reason == "" {
		return types.JobModelTier{}, fmt.Errorf("job: the model tier matrix gave no reason for %s, and every assignment records why", id)
	}
	answer.Source = types.ModelTierSourceMatrix
	return answer, nil
}

// refuseModelTierLimits refuses a fork that would leave more jobs live at assigned's tier
// than that tier's MaxLive bound allows. rows is the plan without the job being assigned;
// a zero bound is unlimited, and a tier the set does not declare is an error.
//
// Read inside the store's write, so unlike [forkLimitProblems] two concurrent forks cannot
// both pass a bound the pair exceeds.
func refuseModelTierLimits(rows []types.Job, assigned types.JobModelTier, set *types.ModelTierSet) error {
	tier, err := set.Parse(assigned.Name)
	if err != nil {
		return fmt.Errorf("job: %w", err)
	}
	limit := set.Bounds(tier).MaxLive
	if limit <= 0 {
		return nil
	}
	live := 1
	for _, r := range rows {
		if r.State.Live() && r.ModelTier != nil && r.ModelTier.Name == assigned.Name {
			live++
		}
	}
	if live > limit {
		return fmt.Errorf("job: this would make %d live jobs at model tier %s, and its MaxLive bound declared at %s allows %d,"+
			" end one with `%s` first or fork at another tier", live, assigned.Name, set.Site(), limit, hint.JobExit.With("<job>"))
	}
	return nil
}
