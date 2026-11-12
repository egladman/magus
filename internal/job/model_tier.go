package job

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

// newModelTierRequest is what a matrix function reads to choose row's tier: the row's
// declared record, its parent's assigned rank, and the recorded outcomes in history.
//
// rows is the plan the row joins, which is where the parent's rank is read. WriteProjects
// stays empty: a project is a graph fact, which [Store] fills from Location.Owners.
func newModelTierRequest(row types.Job, rows []types.Job, set *agent.ModelTierSet, models []types.ModelPricing, history []types.ModelTierRecord) types.ModelTierRequest {
	req := types.ModelTierRequest{
		ID:           row.ID,
		Parent:       row.Parent,
		Instructions: row.Instructions(),
		ReadOnly:     row.ReadOnly,
		Goals:        slices.Clone(row.Goals),
		Kind:         modelTierKind(row),
		WritePaths:   slices.Clone(row.WritePaths),
		ReadPaths:    slices.Clone(row.ReadPaths),
		DependsOn:    slices.Clone(row.DependsOn),
		WriteFiles:   writeFileCount(row.WritePaths),
		Model:        row.Agent.RequestedModel(),
		History:      slices.Clone(history),
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
	if tier, _, ok, _ := declaredModelTier(set, models, row.Agent.RequestedModel()); ok {
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

// declaredModelTier is the tier a row's requested model names, false when it
// names none. A requested tier must be one of set, and an id beside it must
// map to no other tier. host is empty when the tier decides.
func declaredModelTier(set *agent.ModelTierSet, models []types.ModelPricing, m agent.Model) (agent.ModelTier, string, bool, error) {
	if set == nil || m.IsZero() {
		return agent.ModelTier{}, "", false, nil
	}
	if m.Tier == "" {
		return modelIDTier(set, models, m.ID)
	}
	tier, err := set.Parse(m.Tier)
	if err != nil {
		return agent.ModelTier{}, "", false, fmt.Errorf("the job requests the tier %s: %w", m.Tier, err)
	}
	if m.ID == "" {
		return tier, "", true, nil
	}
	idTier, host, ok, err := modelIDTier(set, models, m.ID)
	if err != nil {
		return agent.ModelTier{}, "", false, err
	}
	if ok && idTier.Rank() != tier.Rank() {
		return agent.ModelTier{}, "", false, fmt.Errorf("the job requests the tier %s and the model %s, which %s maps to the tier %s: drop one",
			m.Tier, m.ID, mapper(host), idTier.Name())
	}
	return tier, "", true, nil
}

// modelIDTier is the tier a host model id maps to, through each host's
// alias list and then the harness model rows ([modelRowsHost]). Two hosts
// mapping it to different tiers is an error.
func modelIDTier(set *agent.ModelTierSet, models []types.ModelPricing, model string) (tier agent.ModelTier, host string, ok bool, err error) {
	for _, h := range set.Hosts() {
		t, found := set.Resolve(h, model)
		switch {
		case !found:
		case !ok:
			tier, host, ok = t, h, true
		case t.Rank() != tier.Rank():
			return agent.ModelTier{}, "", false, fmt.Errorf("the model %s is the %s tier of harness %s and the %s tier of harness %s: name a tier instead",
				model, tier.Name(), host, t.Name(), h)
		}
	}
	if !ok {
		if t, found := modelRowTier(set, models, model); found {
			return t, modelRowsHost, true, nil
		}
	}
	return tier, host, ok, nil
}

// ModelRequest reads one model word, as `job fork --model` takes it: a tier
// the workspace declares is the tier, and any other word a host model id.
// A store opened with no model tiers reads every word as an id.
func (s *Store) ModelRequest(word string) agent.Model {
	word = strings.TrimSpace(word)
	if s.modelTiers != nil {
		if _, err := s.modelTiers.Parse(word); err == nil {
			return agent.Model{Tier: word}
		}
	}
	return agent.Model{ID: word}
}

// modelRowsHost stands in for the host of a model the harness model rows map, since no one
// host's declaration does.
const modelRowsHost = "the harness model rows"

func mapper(host string) string {
	if host == modelRowsHost {
		return host
	}
	return "harness " + host
}

// modelRowTier is the tier of the models row whose id or alias is exactly model, the lookup
// the guard makes in modelTierByExactID. False for any other model.
func modelRowTier(set *agent.ModelTierSet, models []types.ModelPricing, model string) (agent.ModelTier, bool) {
	i := slices.IndexFunc(models, func(r types.ModelPricing) bool {
		return r.Tier != "" && (r.ID == model || r.Alias != "" && r.Alias == model)
	})
	if i < 0 {
		return agent.ModelTier{}, false
	}
	t, err := set.Parse(models[i].Tier)
	return t, err == nil
}

// resolveModelTier decides row's model tier and says why. It runs once, when the row is
// created or its model changes, and nothing re-runs it: a tier moves only when someone
// edits the row, the matrix or the declaration.
//
// The first step that answers decides, and the result records which:
//
//   - row: the agent section requests a tier of set, or a model id.
//     An id no host maps takes the default's rank, since a refusal
//     would force a tier onto a model that fits none.
//   - matrix: matrix answered. It may decline with nil. An error from it, or an answer
//     naming a tier outside set, a skill variant that is not one, or no reason, fails the
//     assignment, so a matrix that is wrong never silently falls through to the default.
//   - default: the declaration's default, with the skills the declaration gives it.
//
// A nil matrix declines. set must not be nil.
func resolveModelTier(ctx context.Context, set *agent.ModelTierSet, models []types.ModelPricing, matrix func(context.Context, types.ModelTierRequest) (*agent.JobModelTier, error), req types.ModelTierRequest, row types.Job) (agent.JobModelTier, error) {
	if set == nil {
		return agent.JobModelTier{}, errors.New("job: no model tiers to assign from")
	}
	requested := row.Agent.RequestedModel()
	tier, host, ok, err := declaredModelTier(set, models, requested)
	if err != nil {
		return agent.JobModelTier{}, fmt.Errorf("%s: %w", row.ID, err)
	}
	switch {
	case ok && host == "":
		return agent.JobModelTier{
			Name:   tier.Name(),
			Rank:   tier.Rank(),
			Skills: set.Skills(tier),
			Source: agent.ModelTierSourceRow,
			Reason: fmt.Sprintf("the job names the tier %s", tier.Name()),
			Model:  requested.ID,
		}, nil
	case ok:
		return agent.JobModelTier{
			Name:   tier.Name(),
			Rank:   tier.Rank(),
			Skills: set.Skills(tier),
			Source: agent.ModelTierSourceRow,
			Reason: fmt.Sprintf("the job names the model %s, which %s maps to the tier %s", requested.ID, mapper(host), tier.Name()),
			Model:  requested.ID,
		}, nil
	case requested.ID != "":
		def := set.Default()
		return agent.JobModelTier{
			Name:   def.Name(),
			Rank:   def.Rank(),
			Skills: set.Skills(def),
			Source: agent.ModelTierSourceRow,
			Reason: fmt.Sprintf("the job names the model %s, which no model tier maps to", requested.ID),
			Model:  requested.ID,
		}, nil
	}
	if matrix != nil {
		answer, err := matrix(ctx, req)
		if err != nil {
			return agent.JobModelTier{}, fmt.Errorf("job: choosing the model tier of %s: %w", row.ID, err)
		}
		if answer != nil {
			return checkMatrixAnswer(set, row.ID, *answer)
		}
	}
	def := set.Default()
	return agent.JobModelTier{
		Name:   def.Name(),
		Rank:   def.Rank(),
		Skills: set.Skills(def),
		Source: agent.ModelTierSourceDefault,
		Reason: "the declared default",
		Site:   set.Site(),
	}, nil
}

// assignModelTier is the tier for row, written into plan. It runs under [Store.mutate]'s
// lock with plan as it stood before the write, so the bound is read where it is spent.
func (s *Store) assignModelTier(ctx context.Context, row types.Job, plan []types.Job) (agent.JobModelTier, error) {
	var models []types.ModelPricing
	if s.modelRows != nil {
		models = s.modelRows()
	}
	req := newModelTierRequest(row, plan, s.modelTiers, models, nil)
	if s.modelTierMatrix != nil && s.owners != nil && row.Agent.RequestedModel().IsZero() && len(row.WritePaths) > 0 {
		projects, err := s.writeProjects(ctx, row)
		if err != nil {
			return agent.JobModelTier{}, fmt.Errorf("finding the projects %s writes in: %w", row.ID, err)
		}
		req.WriteProjects = projects
	}
	assigned, err := resolveModelTier(ctx, s.modelTiers, models, s.modelTierMatrix, req, row)
	if err != nil {
		return agent.JobModelTier{}, err
	}
	others := slices.DeleteFunc(slices.Clone(plan), func(r types.Job) bool { return r.ID == row.ID })
	if err := refuseModelTierLimits(others, assigned, s.modelTiers); err != nil {
		return agent.JobModelTier{}, err
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
func refuseAboveParent(set *agent.ModelTierSet, rule func(parent, child agent.ModelTier) error, row types.Job, plan []types.Job) error {
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
	if rule == nil {
		return fmt.Errorf("job: %s and its parent %s both carry a model tier, and this store was opened with no spawn rule to judge them", row.ID, row.Parent)
	}
	refused := rule(parent, child)
	if refused == nil {
		return nil
	}
	err := fmt.Errorf("job: %s would run at model tier %s, and its parent %s was forked at %s,"+
		" so fork it at %s or lower (`--model %s`), or a person forks it at %s as a row of its own: %w",
		row.ID, row.ModelTier, row.Parent, plan[i].ModelTier, parent.Name(), parent.Name(), child.Name(), refused)
	return types.NonRetryable(err, types.RetryAfterPerson, "a person forks the higher-tier row")
}

// declaredTier is the tier record names in set, false when record is nil or set has no
// case of that name at that rank.
func declaredTier(set *agent.ModelTierSet, record *agent.JobModelTier) (agent.ModelTier, bool) {
	if record == nil {
		return agent.ModelTier{}, false
	}
	t, ok := set.At(record.Rank)
	if !ok || t.Name() != record.Name {
		return agent.ModelTier{}, false
	}
	return t, true
}

// checkMatrixAnswer holds a matrix's answer to the declaration it chose from. The answer
// is stored as given once it agrees with set. Its source is the matrix's,
// or the user matrix's when the answer names that.
func checkMatrixAnswer(set *agent.ModelTierSet, id string, answer agent.JobModelTier) (agent.JobModelTier, error) {
	tier, err := set.Parse(answer.Name)
	if err != nil {
		return agent.JobModelTier{}, fmt.Errorf("job: the model tier matrix chose for %s: %w", id, err)
	}
	if answer.Rank != tier.Rank() {
		return agent.JobModelTier{}, fmt.Errorf("job: the model tier matrix chose %s for %s at rank %d, and the declaration ranks it %d", answer.Name, id, answer.Rank, tier.Rank())
	}
	if answer.Skills == "" || !answer.Skills.Valid() {
		return agent.JobModelTier{}, fmt.Errorf("job: the model tier matrix chose skills %q for %s, and the variants are %s", answer.Skills, id, strings.Join(answer.Skills.Values(), ", "))
	}
	if answer.Reason == "" {
		return agent.JobModelTier{}, fmt.Errorf("job: the model tier matrix gave no reason for %s, and every assignment records why", id)
	}
	if answer.Source != agent.ModelTierSourceUserMatrix {
		answer.Source = agent.ModelTierSourceMatrix
	}
	return answer, nil
}

// refuseModelTierLimits refuses a fork that would leave more jobs live at assigned's tier
// than that tier's MaxLive bound allows. rows is the plan without the job being assigned;
// a zero bound is unlimited, and a tier the set does not declare is an error.
//
// Read inside the store's write, so unlike [forkLimitProblems] two concurrent forks cannot
// both pass a bound the pair exceeds.
func refuseModelTierLimits(rows []types.Job, assigned agent.JobModelTier, set *agent.ModelTierSet) error {
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
