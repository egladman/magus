package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
)

// newModelTierRequest is what a matrix function reads to choose row's tier: the row's
// declared record, its parent's assigned rank, and the recorded outcomes in history.
//
// rows is the plan the row joins, which is where the parent's rank is read. WriteProjects
// stays empty: a project is a graph fact, which [Store] fills from Location.Owners.
func newModelTierRequest(row job.Job, rows []job.Job, set *tier.Set, models []model.Row, history []types.ModelTierRecord) (types.ModelTierRequest, error) {
	view, err := types.NewJob(row)
	if err != nil {
		return types.ModelTierRequest{}, err
	}
	req := types.ModelTierRequest{
		ID:             row.ID,
		Parent:         row.Parent,
		Instructions:   row.Instructions(),
		ReadOnly:       row.ReadOnly,
		Check:          view.Check,
		Postconditions: view.Postconditions,
		Kind:           modelTierKind(row),
		WritePaths:     slices.Clone(row.Paths.Write),
		ReadPaths:      slices.Clone(row.Paths.Read),
		DependsOn:      slices.Clone(row.DependsOn),
		WriteFiles:     writeFileCount(row.Paths.Write),
		Model:          row.Agent.RequestedModel(),
		History:        slices.Clone(history),
	}
	if row.Parent != "" {
		if i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == row.Parent }); i >= 0 && rows[i].ModelTier != nil {
			rank := rows[i].ModelTier.Rank
			req.ParentRank = &rank
		}
	}
	if declared, _, ok, _ := declaredModelTier(set, models, row.Agent.RequestedModel()); ok {
		rank := declared.Rank()
		req.DeclaredRank = &rank
	}
	return req, nil
}

// modelTierKind is what row is judged by, the first that applies: a script check, symbol
// postconditions, path postconditions, a target check, else a reader of its prose. A check that does not read
// as a lease check, which no store keeps, is judged as a target check.
func modelTierKind(row job.Job) types.JobKind {
	var symbol, paths, target bool
	for _, g := range row.EffectivePostconditions() {
		g = g.Resolve()
		switch g.Kind {
		case job.PostconditionKindCheck:
			if g.Check.IsZero() {
				continue
			}
			c, err := PostconditionCheck(g)
			if err == nil && c.Script != "" {
				return types.JobKindScript
			}
			target = target || err != nil || c.Target != ""
		case job.PostconditionKindSymbol:
			symbol = true
		case job.PostconditionKindPaths:
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
		file, _ := job.SplitClaim(p)
		files[file] = struct{}{}
	}
	return len(files)
}

// declaredModelTier is the tier a row's requested model names, false when it
// names none. A requested tier must be one of set, and an id beside it must
// map to no other tier. host is empty when the tier decides.
func declaredModelTier(set *tier.Set, models []model.Row, m job.Model) (tier.Tier, string, bool, error) {
	if set == nil || m.IsZero() {
		return tier.Tier{}, "", false, nil
	}
	if m.Tier == "" {
		return modelIDTier(set, models, m.ID)
	}
	named, err := set.Parse(m.Tier)
	if err != nil {
		return tier.Tier{}, "", false, fmt.Errorf("the job requests the tier %s: %w", m.Tier, err)
	}
	if m.ID == "" {
		return named, "", true, nil
	}
	idTier, host, ok, err := modelIDTier(set, models, m.ID)
	if err != nil {
		return tier.Tier{}, "", false, err
	}
	if ok && idTier.Rank() != named.Rank() {
		return tier.Tier{}, "", false, fmt.Errorf("the job requests the tier %s and the model %s, which %s maps to the tier %s: drop one",
			m.Tier, m.ID, mapper(host), idTier.Name())
	}
	return named, "", true, nil
}

// modelIDTier is the tier a host model id maps to, through each host's
// alias list and then the harness model rows ([modelRowsHost]). Two hosts
// mapping it to different tiers is an error.
func modelIDTier(set *tier.Set, models []model.Row, model string) (resolved tier.Tier, host string, ok bool, err error) {
	for _, h := range set.Hosts() {
		t, found := set.Resolve(h, model)
		switch {
		case !found:
		case !ok:
			resolved, host, ok = t, h, true
		case t.Rank() != resolved.Rank():
			return tier.Tier{}, "", false, fmt.Errorf("the model %s is the %s tier of harness %s and the %s tier of harness %s: name a tier instead",
				model, resolved.Name(), host, t.Name(), h)
		}
	}
	if !ok {
		if t, found := modelRowTier(set, models, model); found {
			return t, modelRowsHost, true, nil
		}
	}
	return resolved, host, ok, nil
}

// ModelRequest reads one model word, as `job fork --model` takes it: a tier
// the workspace declares is the tier, and any other word a host model id.
// A store opened with no model tiers reads every word as an id.
func (s *Store) ModelRequest(word string) job.Model {
	word = strings.TrimSpace(word)
	if s.modelTiers != nil {
		if t, err := s.modelTiers.Parse(word); err == nil {
			return job.Model{Tier: t.Name()}
		}
	}
	return job.Model{ID: word}
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

// modelRowTier is the tier of the models row whose id or alias is exactly id, the lookup
// the guard makes in modelTierByExactID. False for any other model.
func modelRowTier(set *tier.Set, models []model.Row, id string) (tier.Tier, bool) {
	i := slices.IndexFunc(models, func(r model.Row) bool {
		return r.Tier != "" && (r.ID == id || r.Alias != "" && r.Alias == id)
	})
	if i < 0 {
		return tier.Tier{}, false
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
func resolveModelTier(ctx context.Context, set *tier.Set, models []model.Row, matrix func(context.Context, types.ModelTierRequest) (*tier.Assignment, error), req types.ModelTierRequest, row job.Job) (tier.Assignment, error) {
	if set == nil {
		return tier.Assignment{}, errors.New("job: no model tiers to assign from")
	}
	requested := row.Agent.RequestedModel()
	declared, host, ok, err := declaredModelTier(set, models, requested)
	if err != nil {
		return tier.Assignment{}, fmt.Errorf("%s: %w", row.ID, err)
	}
	switch {
	case ok && host == "":
		skills, err := set.Skills(declared)
		if err != nil {
			return tier.Assignment{}, fmt.Errorf("job: %s: %w", row.ID, err)
		}
		return tier.Assignment{
			Name:   declared.Name(),
			Rank:   declared.Rank(),
			Skills: skills,
			Source: tier.SourceJob,
			Reason: fmt.Sprintf("the job names the tier %s", declared.Name()),
			Model:  requested.ID,
		}, nil
	case ok:
		skills, err := set.Skills(declared)
		if err != nil {
			return tier.Assignment{}, fmt.Errorf("job: %s: %w", row.ID, err)
		}
		return tier.Assignment{
			Name:   declared.Name(),
			Rank:   declared.Rank(),
			Skills: skills,
			Source: tier.SourceJob,
			Reason: fmt.Sprintf("the job names the model %s, which %s maps to the tier %s", requested.ID, mapper(host), declared.Name()),
			Model:  requested.ID,
		}, nil
	case requested.ID != "":
		def := set.Default()
		skills, err := set.Skills(def)
		if err != nil {
			return tier.Assignment{}, fmt.Errorf("job: %s: %w", row.ID, err)
		}
		return tier.Assignment{
			Name:   def.Name(),
			Rank:   def.Rank(),
			Skills: skills,
			Source: tier.SourceJob,
			Reason: fmt.Sprintf("the job names the model %s, which no model tier maps to", requested.ID),
			Model:  requested.ID,
		}, nil
	}
	if matrix != nil {
		answer, err := matrix(ctx, req)
		if err != nil {
			return tier.Assignment{}, fmt.Errorf("job: choosing the model tier of %s: %w", row.ID, err)
		}
		if answer != nil {
			return checkMatrixAnswer(set, row.ID, *answer)
		}
	}
	def := set.Default()
	skills, err := set.Skills(def)
	if err != nil {
		return tier.Assignment{}, fmt.Errorf("job: %s: %w", row.ID, err)
	}
	return tier.Assignment{
		Name:   def.Name(),
		Rank:   def.Rank(),
		Skills: skills,
		Source: tier.SourceDefault,
		Reason: "the declared default",
		Site:   set.Site(),
	}, nil
}

// assignModelTier is the tier for row, written into plan. It runs under [job.Store.Mutate]'s
// lock with plan as it stood before the write, so the bound is read where it is spent.
func (s *Store) assignModelTier(ctx context.Context, row job.Job, plan []job.Job) (tier.Assignment, error) {
	var models []model.Row
	if s.modelRows != nil {
		models = s.modelRows()
	}
	req, err := newModelTierRequest(row, plan, s.modelTiers, models, nil)
	if err != nil {
		return tier.Assignment{}, err
	}
	if s.modelTierMatrix != nil && s.owners != nil && row.Agent.RequestedModel().IsZero() && len(row.Paths.Write) > 0 {
		projects, err := s.writeProjects(ctx, row)
		if err != nil {
			return tier.Assignment{}, fmt.Errorf("finding the projects %s writes in: %w", row.ID, err)
		}
		req.WriteProjects = projects
	}
	assigned, err := resolveModelTier(ctx, s.modelTiers, models, s.modelTierMatrix, req, row)
	if err != nil {
		return tier.Assignment{}, err
	}
	if t, ok := s.modelTiers.At(assigned.Rank); ok {
		assigned.Shared = s.modelTiers.SharedAliases(t)
	}
	others := slices.DeleteFunc(slices.Clone(plan), func(r job.Job) bool { return r.ID == row.ID })
	if err := refuseModelTierLimits(others, assigned, s.modelTiers); err != nil {
		return tier.Assignment{}, err
	}
	return assigned, nil
}

// writeProjects is the projects row's write paths fall in, through Location.Owners, which
// the caller has checked is set.
func (s *Store) writeProjects(ctx context.Context, row job.Job) ([]string, error) {
	files := make([]string, len(row.Paths.Write))
	for i, p := range row.Paths.Write {
		files[i], _ = job.SplitClaim(p)
	}
	return s.owners(ctx, files)
}

// spawnAboveParentTier is the guard rule whose setting a fork follows.
const spawnAboveParentTier = "spawn-above-parent-tier"

// tierPair is a row's proven tier beside its parent's. above is why the
// spawn rule refuses the pair, nil when it ranks at or below the parent.
type tierPair struct {
	parentRow     job.Job
	parent, child tier.Tier
	above         error
}

// aboveParent judges row's tier against its parent's in plan. Only a proven
// pair is judged. A parent missing from plan, or a tier set no longer
// declares, gives a zero pair. A proven pair with no rule is an error.
func aboveParent(set *tier.Set, rule func(parent, child tier.Tier) error, row job.Job, plan []job.Job) (tierPair, error) {
	if set == nil || row.Parent == "" || row.ModelTier == nil {
		return tierPair{}, nil
	}
	i := slices.IndexFunc(plan, func(r job.Job) bool { return r.ID == row.Parent })
	if i < 0 {
		return tierPair{}, nil
	}
	parent, ok := declaredTier(set, plan[i].ModelTier)
	if !ok {
		return tierPair{}, nil
	}
	child, ok := declaredTier(set, row.ModelTier)
	if !ok {
		return tierPair{}, nil
	}
	if rule == nil {
		return tierPair{}, fmt.Errorf("job: %s and its parent %s both carry a model tier, and this store was opened with no spawn rule to judge them", row.ID, row.Parent)
	}
	return tierPair{parentRow: plan[i], parent: parent, child: child, above: spawnRefusal(rule(parent, child))}, nil
}

// spawnRefusal gives a [tier.Ceiling] refusal its MGS code, which the module cannot name.
// Any other error is the rule's own and passes through.
func spawnRefusal(err error) error {
	var above *tier.AboveParentError
	var apart *tier.IncomparableError
	switch {
	case errors.As(err, &above):
		return types.WrapDiagnostic(types.ModelTierAboveParent, err, "%v", err)
	case errors.As(err, &apart):
		return types.WrapDiagnostic(types.ModelTiersIncomparable, err, "%v", err)
	}
	return err
}

// refuseAboveParent refuses row's tier above its parent's only when the
// spawn-above-parent-tier setting is deny; advise and off let it through.
// A store with no setting reader, or one that fails, is an error.
func refuseAboveParent(ctx context.Context, set *tier.Set, rule func(parent, child tier.Tier) error, ceiling func(context.Context) (builtin.Setting, error), row job.Job, plan []job.Job) error {
	pair, err := aboveParent(set, rule, row, plan)
	if err != nil || pair.above == nil {
		return err
	}
	if ceiling == nil {
		return fmt.Errorf("job: %s ranks above its parent %s, and this store was opened with no %s setting to judge it", row.ID, row.Parent, spawnAboveParentTier)
	}
	s, err := ceiling(ctx)
	if err != nil {
		return fmt.Errorf("job: %s ranks above its parent %s: %w", row.ID, row.Parent, err)
	}
	if s.Decision != builtin.Deny {
		return nil
	}
	err = fmt.Errorf("job: %s would run at model tier %s, and its parent %s was forked at %s,"+
		" and this workspace sets %s to deny, so fork it at %s or lower (`--model %s`): %w",
		row.ID, row.ModelTier, row.Parent, pair.parentRow.ModelTier, spawnAboveParentTier, pair.parent.Name(), pair.parent.Name(), pair.above)
	return types.NonRetryable(err, job.RetryAfterPerson, "a person forks the higher-tier row or sets the rule to advise")
}

// aboveParentNote advises a fork above its parent's tier, "" when it is not
// or the setting is off. A setting that fails to read is said, not dropped.
func aboveParentNote(ctx context.Context, store *Store, row job.Job, plan []job.Job) string {
	pair, err := aboveParent(store.modelTiers, store.spawnRule, row, plan)
	if err != nil || pair.above == nil || store.spawnCeiling == nil {
		return ""
	}
	s, err := store.spawnCeiling(ctx)
	if err != nil {
		return fmt.Sprintf("%s ranks above its parent %s, and the %s setting did not read: %v", row.ID, row.Parent, spawnAboveParentTier, err)
	}
	if s.Decision == builtin.Off {
		return ""
	}
	var models []model.Row
	if store.modelRows != nil {
		models = store.modelRows()
	}
	return fmt.Sprintf("%s runs at model tier %s (rank %d), above its parent %s at %s (rank %d); %s."+
		" `--model %s` keeps it at the parent's tier, and setting %s off in magus\\guard.builtins stops this advice",
		row.ID, pair.child.Name(), pair.child.Rank(), row.Parent, pair.parent.Name(), pair.parent.Rank(),
		ModelCost(models, cmp.Or(row.ModelTier.Model, row.Agent.RequestedModel().ID, row.ModelTier.Name), cmp.Or(pair.parentRow.ModelTier.Model, pair.parentRow.Agent.RequestedModel().ID, pair.parentRow.ModelTier.Name)), pair.parent.Name(), spawnAboveParentTier)
}

// ModelCost says what model and parent each cost per million tokens, input
// and output, from the harness model rows (mgs_listModels) matched by exact
// id or alias. A price no row gives is said to be unknown, never zero.
func ModelCost(rows []model.Row, model, parent string) string {
	return fmt.Sprintf("per million tokens, %s, and %s", modelPrice(rows, model), modelPrice(rows, parent))
}

// modelPrice is one model's prices as ModelCost says them.
func modelPrice(rows []model.Row, id string) string {
	i := slices.IndexFunc(rows, func(r model.Row) bool {
		return r.ID == id || r.Alias != "" && r.Alias == id
	})
	if i < 0 {
		return id + "'s price is unknown, since no mgs_listModels row names it"
	}
	return fmt.Sprintf("%s costs %s and %s", id, usdPrice(rows[i].Input, "input"), usdPrice(rows[i].Output, "output"))
}

// usdPrice says p for what; a row leaves an unknown price 0.
func usdPrice(p budget.USDPerMTok, what string) string {
	if p <= 0 {
		return fmt.Sprintf("an unknown %s price", what)
	}
	return fmt.Sprintf("$%.2f %s", float64(p), what)
}

// declaredTier is the tier record names in set, false when record is nil or set has no
// case of that name at that rank.
func declaredTier(set *tier.Set, record *tier.Assignment) (tier.Tier, bool) {
	if record == nil {
		return tier.Tier{}, false
	}
	t, ok := set.At(record.Rank)
	if !ok || t.Name() != record.Name {
		return tier.Tier{}, false
	}
	return t, true
}

// checkMatrixAnswer holds a matrix's answer to the declaration it chose from. The answer
// is stored as given once it agrees with set. Its source is the matrix's,
// or the user matrix's when the answer names that.
func checkMatrixAnswer(set *tier.Set, id string, answer tier.Assignment) (tier.Assignment, error) {
	named, err := set.Parse(answer.Name)
	if err != nil {
		return tier.Assignment{}, fmt.Errorf("job: the model tier matrix chose for %s: %w", id, err)
	}
	if answer.Rank != named.Rank() {
		return tier.Assignment{}, fmt.Errorf("job: the model tier matrix chose %s for %s at rank %d, and the declaration ranks it %d", answer.Name, id, answer.Rank, named.Rank())
	}
	if answer.Skills == "" || !answer.Skills.Valid() {
		return tier.Assignment{}, fmt.Errorf("job: the model tier matrix chose skills %q for %s, and the variants are %s", answer.Skills, id, strings.Join(answer.Skills.Values(), ", "))
	}
	if answer.Reason == "" {
		return tier.Assignment{}, fmt.Errorf("job: the model tier matrix gave no reason for %s, and every assignment records why", id)
	}
	if answer.Source != tier.SourceUserMatrix {
		answer.Source = tier.SourceMatrix
	}
	answer.Name = named.Name()
	return answer, nil
}

// refuseModelTierLimits refuses a fork that would leave more jobs live at assigned's tier
// than that tier's MaxLive bound allows. rows is the plan without the job being assigned;
// a zero bound is unlimited, and a tier the set does not declare is an error.
//
// Read inside the store's write, so unlike [forkLimitProblems] two concurrent forks cannot
// both pass a bound the pair exceeds.
func refuseModelTierLimits(rows []job.Job, assigned tier.Assignment, set *tier.Set) error {
	named, err := set.Parse(assigned.Name)
	if err != nil {
		return fmt.Errorf("job: %w", err)
	}
	bounds, err := set.Bounds(named)
	if err != nil {
		return fmt.Errorf("job: %w", err)
	}
	limit := bounds.MaxLive
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

// fourModelTiers respells a row's requested and assigned tier from the
// three shipped tiers to the four that replaced them. The map is its own,
// so it stays when the readers' shim for the old names goes.
func fourModelTiers(e migrate.Entry) (migrate.Entry, error) {
	renamed := map[string]string{"economy": "routine", "standard": "bounded", "principal": "design"}
	if err := respellMember(e, "agent", renamed, "model", "tier"); err != nil {
		return nil, err
	}
	if err := respellMember(e, "model_tier", renamed, "name"); err != nil {
		return nil, err
	}
	return e, nil
}

// respellMember replaces the string at path under e's key with its entry
// in renamed. A missing member, or a value renamed lacks, changes nothing.
func respellMember(e migrate.Entry, key string, renamed map[string]string, path ...string) error {
	raw, ok := e[key]
	if !ok {
		return nil
	}
	var root any
	if err := codec.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("reading %s: %w", key, err)
	}
	obj, ok := root.(map[string]any)
	for _, p := range path[:len(path)-1] {
		if !ok {
			return nil
		}
		obj, ok = obj[p].(map[string]any)
	}
	if !ok {
		return nil
	}
	last := path[len(path)-1]
	old, _ := obj[last].(string)
	to, ok := renamed[old]
	if !ok {
		return nil
	}
	obj[last] = to
	body, err := codec.Marshal(root)
	if err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	e[key] = body
	return nil
}
