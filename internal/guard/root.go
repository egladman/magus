package guard

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The cost rules judge the main thread of a session: the context every one of its calls
// re-sends, and the work it does that a subagent could do from a far smaller context. A
// session magus recorded handing its work out is judged by its role
// (orchestrator-groundwork), at any size. The root-* meters only advise: a level
// (magus\guard.profile) turns each on, and the figure it fires at comes from the level and
// the account's billing unless the workspace states one.
const (
	denyRuleOrchestratorGroundwork denyRuleName = "orchestrator-groundwork"
	denyRuleRootContextBudget      denyRuleName = "root-context-budget"
	denyRuleRootHeavyCommand       denyRuleName = "root-heavy-command"
	denyRuleRootResultCap          denyRuleName = "root-result-cap"
	denyRuleRootRepeat             denyRuleName = "root-repeat"
	denyRuleSpawnNamesModel        denyRuleName = "spawn-names-model"
	denyRuleSpawnAboveModelTier    denyRuleName = "spawn-above-model-tier"
)

// settingFigure is the figure s advises at: param when the workspace stated one, else the
// figure of the level s came from, standard's for a setting with no level. 0 is never,
// which every setting but advise is: the meters only advise.
func settingFigure(s builtin.Setting, param int, figures func(types.GuardProfile) int) int {
	switch {
	case s.Decision != builtin.Advise:
		return 0
	case param > 0:
		return param
	}
	level := s.Profile
	if level == "" || level == types.GuardProfileOff {
		level = types.GuardProfileStandard
	}
	return figures(level)
}

// budgetFigures are root-context-budget's sizes: half the window at light, and fixed sizes
// above it, higher on a quota that resets than on a per-token bill.
func budgetFigures(billing types.AgentBilling, window int64) func(types.GuardProfile) int {
	by := func(metered, quota, unknown int) int {
		switch billing {
		case types.AgentBillingMetered:
			return metered
		case types.AgentBillingQuota:
			return quota
		}
		return unknown
	}
	return func(level types.GuardProfile) int {
		switch level {
		case types.GuardProfileLight:
			return int(window / 2)
		case types.GuardProfileStandard:
			return by(250_000, 400_000, 300_000)
		case types.GuardProfileStrict:
			return by(150_000, 250_000, 200_000)
		}
		return 0
	}
}

func heavyFigures(level types.GuardProfile) int {
	if level == types.GuardProfileStrict {
		return 5
	}
	return 8
}

func repeatFigures(types.GuardProfile) int { return 2 }

func resultFigures(level types.GuardProfile) int {
	switch level {
	case types.GuardProfileLight:
		return 40_000
	case types.GuardProfileStrict:
		return 10_000
	}
	return 20_000
}

// mainCall is one main-thread call as the cost rules read it.
type mainCall struct {
	// from is what the caller's main thread rests on; inferred, every refusal is said as
	// advice.
	from    types.ThreadSource
	billing types.AgentBilling
	window  int64
	// class is the call's delegable class, "" for a call no subagent would make for it.
	class types.DelegableClass
	// free is whether the call is costFree: its output is a ref or one line, so it adds
	// nothing the budget rules exist to keep out of the context.
	free bool
	line string
	// orchestration is how surely the session hands its work out, "" with no evidence.
	orchestration types.Orchestration
	// work is the groundwork the call does as an orchestrator is judged for it, "" for
	// orchestration work.
	work types.DelegableClass
	// spawnBroken is why no worker spawn would pass the spawn rules, "" when one would.
	spawnBroken string
}

// costFinding is one cost rule matching a call: a refusal, or advice said once per
// session under key.
type costFinding struct {
	rule denyRuleName
	deny bool
	say  string
	key  hint.MarkerKind
}

// mainFindings is every cost rule matching c, given what magus recorded about its thread.
// It reads and spends nothing, so a level not in force can be asked the same question.
//
// orchestrator-groundwork judges a session by its role, and the meters only advise. The
// streak and repeat meters nudge a session toward delegating, so one already orchestrating
// skips them: its every repeat is groundwork, which the role rule judges.
func mainFindings(setting func(string) builtin.Setting, st costState, c mainCall) []costFinding {
	var out []costFinding
	if f, ok := groundworkFinding(setting(string(denyRuleOrchestratorGroundwork)), c); ok {
		out = append(out, f)
	}
	s := setting(string(denyRuleRootContextBudget))
	if at := settingFigure(s, s.Tokens, budgetFigures(c.billing, c.window)); at > 0 && st.Tokens >= int64(at) && !c.free {
		out = append(out, costFinding{rule: denyRuleRootContextBudget, key: hint.MarkerKind(fmt.Sprintf("%s-%d", denyRuleRootContextBudget, at)),
			say: fmt.Sprintf("magus workspace: this session's context is %s tokens and every call re-sends it: fork a job (`magus job fork`) and spawn a worker on it.", kTokens(st.Tokens))})
	}
	if c.class != "" && c.orchestration == "" {
		s = setting(string(denyRuleRootHeavyCommand))
		if at, n := settingFigure(s, s.Calls, heavyFigures), st.Streak+1; at > 0 && n >= at {
			out = append(out, costFinding{rule: denyRuleRootHeavyCommand, key: hint.MarkerKind(denyRuleRootHeavyCommand),
				say: fmt.Sprintf("magus workspace: %d searches, reads, diffs or runs in a row after the last spawn: a subagent does this work from a small context and returns only what matters.", n)})
		}
		s = setting(string(denyRuleRootRepeat))
		if at, n := settingFigure(s, s.Calls, repeatFigures), st.Repeats[lineDigest(c.line)]+1; at > 0 && n >= at {
			out = append(out, costFinding{rule: denyRuleRootRepeat, key: hint.MarkerKind(denyRuleRootRepeat),
				say: "magus workspace: this session ran this exact command before with no write since, and its output is still in the context."})
		}
	}
	s = setting(string(denyRuleRootResultCap))
	if at := settingFigure(s, s.Chars, resultFigures); at > 0 {
		for _, tool := range slices.Sorted(maps.Keys(st.Results)) {
			chars := st.Results[tool]
			if chars < at {
				continue
			}
			key := hint.MarkerKind(denyRuleRootResultCap)
			if s.Profile != types.GuardProfileLight {
				key += hint.MarkerKind("-" + lineDigest(tool))
			}
			out = append(out, costFinding{rule: denyRuleRootResultCap, key: key,
				say: fmt.Sprintf("magus workspace: one %s result put %d characters into this session's context, which every later call re-sends: a subagent reading it returns only what matters.", tool, chars)})
		}
	}
	return out
}

// groundworkWork names each groundwork class as orchestrator-groundwork says it.
var groundworkWork = map[types.DelegableClass]string{
	types.DelegableSearch: "a search",
	types.DelegableRead:   "a read of a file in the checkout",
	types.DelegableDiff:   "a diff read",
	types.DelegableRun:    "a run",
	types.DelegableFetch:  "a fetch",
	types.DelegableEdit:   "an edit in the checkout",
	delegableIntegrate:    "integration (a cherry-pick, a build of ./magus, a regeneration, staging or a commit)",
}

// groundworkFinding is orchestrator-groundwork's finding on c: groundwork by a session
// with a live row it handed out. Proof under a deny setting refuses; an inferred record,
// or an advise setting, advises once per class. A session with no live row is never
// judged. Where no spawn would pass, the remedy is holding a row, never a dead end.
func groundworkFinding(s builtin.Setting, c mainCall) (costFinding, bool) {
	if c.work == "" || c.orchestration == "" || (s.Decision != builtin.Advise && s.Decision != builtin.Deny) {
		return costFinding{}, false
	}
	work := cmp.Or(groundworkWork[c.work], string(c.work))
	say := fmt.Sprintf("magus workspace: this session hands out work, and %s is groundwork: fork a job for it (a read-only scout for a question) and spawn a worker on it.", work)
	if c.spawnBroken != "" {
		say = fmt.Sprintf("magus workspace: this session hands out work, and %s is groundwork, but spawning is broken: %s; take a row with `./magus job exec <row>` and do it as its holder.", work, c.spawnBroken)
	}
	return costFinding{
		rule: denyRuleOrchestratorGroundwork,
		deny: s.Decision == builtin.Deny && c.orchestration == types.OrchestrationProven,
		key:  hint.MarkerKind(fmt.Sprintf("%s-%s", denyRuleOrchestratorGroundwork, c.work)),
		say:  say,
	}, true
}

// spawnBroken is why a spawn by host would be refused whatever its brief: at a deny
// setting, spawn-names-model refuses every bound spawn from a harness the model-tier
// declaration maps no model for. Asked only for a finding, since the declaration opens
// the workspace.
func spawnBroken(deps Dependencies, host string) string {
	if deps.ModelTiers == nil || deps.settingLookup(string(denyRuleSpawnNamesModel)).Decision != builtin.Deny {
		return ""
	}
	set := deps.ModelTiers()
	if set == nil {
		return ""
	}
	if _, err := set.Alias(host, set.Default()); err != nil {
		return err.Error()
	}
	return ""
}

// kTokens renders a token count in thousands.
func kTokens(n int64) string { return fmt.Sprintf("%dk", n/1000) }

// applyFindings puts findings on verdict: the first refusal replaces it when the host
// reported the thread, and every other finding advises once per session, appended to
// advice already there. An inferred thread is told a refusal as advice.
func applyFindings(markers hint.Gate, verdict types.GuardJudgment, findings []costFinding, from types.ThreadSource) types.GuardJudgment {
	for _, f := range findings {
		if f.deny && from == types.ThreadReported {
			return types.GuardJudgment{SchemaVersion: verdict.SchemaVersion, Decision: decisionDeny, Reason: f.say, Rule: string(f.rule), Lease: verdict.Lease, LeaseFrom: verdict.LeaseFrom}
		}
	}
	if verdict.Decision != decisionPass && verdict.Decision != decisionAdvise {
		return verdict
	}
	for _, f := range findings {
		say, _, _ := strings.Cut(f.say, "\n")
		held := markers.Once(f.key, say)
		if held == "" {
			continue
		}
		if verdict.Decision == decisionAdvise {
			verdict.Context += "\n\n" + held
			continue
		}
		verdict.Decision, verdict.Context, verdict.Rule = decisionAdvise, held, string(f.rule)
	}
	return verdict
}

// settingLookup adapts deps.setting to the lookup mainFindings takes.
func (d Dependencies) settingLookup(rule string) builtin.Setting {
	s, _ := d.setting(rule)
	return s
}

// gradeMainCommand judges a shell command of a session's unleased main thread by the cost
// rules. A served next is magus's own suggestion and is left alone.
func gradeMainCommand(ctx context.Context, deps Dependencies, verdict types.GuardJudgment, in commandRuleInput, who hookAttribution, at location) types.GuardJudgment {
	if verdict.Decision == decisionDeny || in.preauth != "" || in.lease != "" || at.cacheDir == "" {
		return verdict
	}
	thread, from := callerThread(at.cacheDir, who, in.lease)
	if thread != types.AgentThreadMain {
		return verdict
	}
	gate := hint.NewGate(at.cacheDir, who.callerKey())
	if f, misdeclared := budgetPastEveryWindow(deps.settingLookup(string(denyRuleRootContextBudget)), at.modelTable); misdeclared {
		return applyFindings(gate, verdict, []costFinding{f}, from)
	}
	st := sessionCostState(at, who, liveRowLookup(ctx, at), time.Now())
	call := mainCall{from: from, billing: accountAt(at.workspace).billing, window: contextWindow(st, at), class: delegableClass(in.command), free: costFree(in.command), line: in.command,
		orchestration: orchestration(st, from)}
	if call.orchestration != "" {
		call.work = mainWork(ctx, at, st, in.command)
	}
	if call.work != "" {
		call.spawnBroken = spawnBroken(deps, who.Host)
	}
	return applyFindings(gate, verdict, mainFindings(deps.settingLookup, st, call), from)
}

// gradeMainWrite judges a write by a session's unleased main thread inside a checkout of
// the workspace's repository by orchestrator-groundwork. A write while the checkout is
// midway through a merge or a cherry-pick may resolve a conflict, which is integration, so
// it is advised and never refused there. A notes path is never judged.
func gradeMainWrite(deps Dependencies, verdict types.GuardJudgment, path string, lease string, who hookAttribution, at location) types.GuardJudgment {
	if verdict.Decision == decisionDeny || lease != "" || at.cacheDir == "" || notesPath(path, at) {
		return verdict
	}
	thread, from := callerThread(at.cacheDir, who, lease)
	if thread != types.AgentThreadMain {
		return verdict
	}
	st := sessionCostState(at, who, liveRowLookup(context.Background(), at), time.Now())
	call := mainCall{from: from, line: path, orchestration: orchestration(st, from), work: types.DelegableEdit}
	if call.orchestration != "" {
		call.spawnBroken = spawnBroken(deps, who.Host)
	}
	f, ok := groundworkFinding(deps.settingLookup(string(denyRuleOrchestratorGroundwork)), call)
	if !ok {
		return verdict
	}
	if f.deny && integrating(path, at) {
		f.deny = false
	}
	return applyFindings(hint.NewGate(at.cacheDir, who.callerKey()), verdict, []costFinding{f}, from)
}

// budgetPastEveryWindow is the refusal of a root-context-budget tokens figure above the
// largest window of the model table, which no context reaches, so the rule would never
// fire. The magusfile load cannot say so: the harness rows that make the table load after
// it. The table is read only for a stated figure, and one with no window has nothing to
// bound it by.
func budgetPastEveryWindow(s builtin.Setting, table func() model.Table) (costFinding, bool) {
	if s.Decision == builtin.Off || s.Tokens <= 0 {
		return costFinding{}, false
	}
	var largest int64
	for _, row := range table().Rows() {
		largest = max(largest, row.Window)
	}
	if largest == 0 || int64(s.Tokens) <= largest {
		return costFinding{}, false
	}
	msg := fmt.Sprintf("built-in rule %q: tokens is %d, past the largest context window in the model table (%d); give %d or less in magus\\guard.builtins, or leave it out",
		denyRuleRootContextBudget, s.Tokens, largest, largest)
	return costFinding{rule: denyRuleRootContextBudget, deny: true, key: hint.MarkerKind(denyRuleRootContextBudget) + "-misdeclared", say: "magus workspace: " + types.FormatDiagnostic(types.GuardRuleMisdeclared, msg)}, true
}

// levelSettings is every level's settings with nothing declared over them, which is what
// a shadow verdict asks each level.
var levelSettings = sync.OnceValue(func() map[types.GuardProfile]map[string]builtin.Setting {
	out := map[types.GuardProfile]map[string]builtin.Setting{}
	for _, name := range types.GuardProfile("").Values() {
		level := types.GuardProfile(name)
		expanded, err := builtin.ResolveProfile(level, nil)
		if err != nil {
			continue
		}
		if resolved, err := builtin.Resolve(expanded); err == nil {
			out[level] = resolved
		}
	}
	return out
})

// shadowVerdicts is what each level would decide for c by the main-thread cost rules,
// whichever is in force. It counts a match before the once-per-session hold, so an
// advice a session was already given still counts as one.
func shadowVerdicts(st costState, c mainCall) map[types.GuardProfile]types.GuardDecision {
	out := map[types.GuardProfile]types.GuardDecision{}
	for level, settings := range levelSettings() {
		decision := types.GuardAllow
		for _, f := range mainFindings(func(rule string) builtin.Setting { return settings[rule] }, st, c) {
			if f.deny && c.from == types.ThreadReported {
				decision = types.GuardDeny
				break
			}
			decision = types.GuardAdvise
		}
		out[level] = decision
	}
	return out
}

// gradeMainSpawn judges a spawn by the main thread under the spawn cost rules: whether it
// names a model, and whether that model is at the model tier its job was forked at. Tiers
// compare by rank in the workspace's declaration, never by name or price; a spawn bound to
// no row, or to a row forked with no declaration, has nothing to compare.
func gradeMainSpawn(deps Dependencies, verdict types.GuardJudgment, env hookRequest, who hookAttribution, at location, lease string, titled spawnTitle) types.GuardJudgment {
	if verdict.Decision == decisionDeny || !env.IsSpawn || lease != "" || at.cacheDir == "" {
		return verdict
	}
	thread, from := callerThread(at.cacheDir, who, lease)
	if thread != types.AgentThreadMain {
		return verdict
	}
	var forked forkedTier
	bound := false
	if row, ok := titled(); ok {
		forked, bound = boundModelTier(deps, row)
	}
	if bound && at.models != nil {
		forked.rows = at.models()
	}
	host := who.Host
	var findings []costFinding
	switch s := deps.settingLookup(string(denyRuleSpawnNamesModel)); {
	case s.Decision == builtin.Off:
	case env.DeclaredModel == "":
		findings = append(findings, costFinding{rule: denyRuleSpawnNamesModel, deny: s.Decision == builtin.Deny, key: hint.MarkerKind(denyRuleSpawnNamesModel),
			say: "magus workspace: this spawn names no model, so the subagent runs on this session's own: name the model the job's work needs."})
	case s.Decision == builtin.Deny && bound:
		if say := forked.refusal(host, env.DeclaredModel); say != "" {
			findings = append(findings, costFinding{rule: denyRuleSpawnNamesModel, deny: true, key: hint.MarkerKind(denyRuleSpawnNamesModel), say: say})
		}
	}
	if s := deps.settingLookup(string(denyRuleSpawnAboveModelTier)); s.Decision != builtin.Off && bound {
		if say := forked.rankedAbove(host, env.DeclaredModel); say != "" {
			findings = append(findings, costFinding{rule: denyRuleSpawnAboveModelTier, key: hint.MarkerKind(denyRuleSpawnAboveModelTier), say: say})
		}
	}
	return applyFindings(hint.NewGate(at.cacheDir, who.callerKey()), verdict, findings, from)
}

// forkedTier is the model tier a job row was forked at, resolved against the workspace's
// declaration as it stands.
type forkedTier struct {
	job    string
	record types.JobModelTier
	tier   types.ModelTier
	set    *types.ModelTierSet
	// rows are the mgs_listModels rows: their aliases name the short model a host's spawn
	// tool takes where the declaration maps only full ids.
	rows []types.ModelPricing
}

// boundModelTier is the tier row was forked at, as tierIn finds it in the workspace's
// declaration. The declaration is asked for only once a row carries a tier, since reading
// it opens the workspace.
func boundModelTier(deps Dependencies, row types.Job) (forkedTier, bool) {
	if row.ModelTier == nil || deps.ModelTiers == nil {
		return forkedTier{}, false
	}
	return tierIn(deps.ModelTiers(), row)
}

// tierIn is the tier row was forked at in set. It is false when the row records none, set
// is nil, or set no longer has a case of that name at that rank: a row forked before its
// enum changed is graded by nothing rather than by a guess.
func tierIn(set *types.ModelTierSet, row types.Job) (forkedTier, bool) {
	if set == nil || row.ModelTier == nil {
		return forkedTier{}, false
	}
	t, ok := set.At(row.ModelTier.Rank)
	if !ok || t.Name() != row.ModelTier.Name {
		return forkedTier{}, false
	}
	return forkedTier{job: row.ID, record: *row.ModelTier, tier: t, set: set}, true
}

// line is the assignment as a verdict names it, with the alias host spawns for it:
// `economy (rank 0) -> haiku, matrix: "two files" at hack/tiers.buzz:12`.
func (f forkedTier) line(alias string) string {
	out := fmt.Sprintf("%s (rank %d) -> %s, %s", f.record.Name, f.record.Rank, alias, f.record.Source)
	if f.record.Reason != "" {
		out += fmt.Sprintf(": %q", f.record.Reason)
	}
	if f.record.Site != "" {
		out += " at " + f.record.Site
	}
	return out
}

// refusal is spawn-names-model's refusal at strict of a spawn of model from host, "" when
// model resolves to a tier of the forked rank, through the declaration or a mgs_listModels
// row's id or alias, or is the model the row itself named. A host
// the declaration maps no model for is refused too, naming the hosts entry that would map
// one.
func (f forkedTier) refusal(host, model string) string {
	if f.record.Model != "" && model == f.record.Model {
		return ""
	}
	alias, err := f.set.Alias(host, f.tier)
	if err != nil {
		return fmt.Sprintf("magus workspace: job %s was forked at model tier %s, and %v; add a %s entry to the hosts magus\\job.modelTiers declares, or spawn the job from a harness it maps.",
			f.job, f.record.Name, err, host)
	}
	spawned, ok := modelTierByExactID(f.set, host, model, f.rows)
	if ok && spawned.Rank() == f.tier.Rank() {
		return ""
	}
	runs := fmt.Sprintf("%s, which harness %s maps to no model tier", model, host)
	if ok {
		runs = fmt.Sprintf("%s, model tier %s (rank %d)", model, spawned.Name(), spawned.Rank())
	}
	return fmt.Sprintf("magus workspace: this spawn runs %s, and job %s was forked at %s; spawn it on %s, or re-fork the job at the tier its work needs.",
		runs, f.job, f.line(alias), alias)
}

// rankedAbove is spawn-above-model-tier's advice for a spawn of model from host, "" unless
// model resolves to a tier ranking above the forked one.
func (f forkedTier) rankedAbove(host, model string) string {
	spawned, ok := modelTierByExactID(f.set, host, model, f.rows)
	if !ok {
		return ""
	}
	if above, err := spawned.Above(f.tier); err != nil || !above {
		return ""
	}
	// Resolve found the host mapped, so Alias cannot fail here.
	alias, _ := f.set.Alias(host, f.tier)
	return fmt.Sprintf("magus workspace: this spawn runs %s, model tier %s (rank %d), above job %s's %s: spawn it on %s, or re-fork the row at the tier the work needs.",
		model, spawned.Name(), spawned.Rank(), f.job, f.line(alias), alias)
}
