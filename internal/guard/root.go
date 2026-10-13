package guard

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The cost rules judge the main thread of a session: the context every one of its calls
// re-sends, and the work it does that a subagent could do from a far smaller context. A
// level (magus\guard.profile) sets each rule's decision, and the figure it fires at comes
// from the level and the account's billing unless the workspace states one.
const (
	denyRuleRootContextBudget        denyRuleName = "root-context-budget"
	denyRuleRootHeavyCommand         denyRuleName = "root-heavy-command"
	denyRuleRootResultCap            denyRuleName = "root-result-cap"
	denyRuleRootRepeat               denyRuleName = "root-repeat"
	denyRuleSpawnNamesModel          denyRuleName = "spawn-names-model"
	denyRuleSpawnPrincipalForEconomy denyRuleName = "spawn-principal-for-economy"
)

// ladder is where a rule starts to advise and where it starts to refuse; 0 is never.
type ladder struct{ advise, deny int }

// settingLadder is the ladder s sets: param alone at s's decision when the workspace
// stated one, else the figures of the level s came from. A setting with no level reads
// standard's figures to advise and strict's to refuse.
func settingLadder(s builtin.Setting, param int, figures func(types.GuardProfile) ladder) ladder {
	switch {
	case s.Decision != builtin.Advise && s.Decision != builtin.Deny:
		return ladder{}
	case param > 0 && s.Decision == builtin.Deny:
		return ladder{deny: param}
	case param > 0:
		return ladder{advise: param}
	}
	level := s.Profile
	if level == "" || level == types.GuardProfileOff {
		level = types.GuardProfileStandard
		if s.Decision == builtin.Deny {
			level = types.GuardProfileStrict
		}
	}
	l := figures(level)
	switch {
	case s.Decision == builtin.Advise:
		return ladder{advise: cmp.Or(l.advise, l.deny)}
	case l.deny == 0:
		return ladder{deny: l.advise}
	}
	return l
}

// budgetFigures are root-context-budget's figures: half the window at light, and fixed
// sizes above it, higher on a quota that resets than on a per-token bill.
func budgetFigures(billing types.AgentBilling, window int64) func(types.GuardProfile) ladder {
	by := func(metered, quota, unknown int) int {
		switch billing {
		case types.AgentBillingMetered:
			return metered
		case types.AgentBillingQuota:
			return quota
		}
		return unknown
	}
	return func(level types.GuardProfile) ladder {
		switch level {
		case types.GuardProfileLight:
			return ladder{advise: int(window / 2)}
		case types.GuardProfileStandard:
			return ladder{advise: by(250_000, 400_000, 300_000)}
		case types.GuardProfileStrict:
			return ladder{advise: by(150_000, 250_000, 200_000), deny: by(300_000, 450_000, 350_000)}
		}
		return ladder{}
	}
}

func heavyFigures(level types.GuardProfile) ladder {
	if level == types.GuardProfileStrict {
		return ladder{advise: 5, deny: 8}
	}
	return ladder{advise: 8}
}

func repeatFigures(level types.GuardProfile) ladder {
	if level == types.GuardProfileStrict {
		return ladder{advise: 2, deny: 3}
	}
	return ladder{advise: 2}
}

func resultFigures(level types.GuardProfile) ladder {
	switch level {
	case types.GuardProfileLight:
		return ladder{advise: 40_000}
	case types.GuardProfileStrict:
		return ladder{advise: 10_000}
	}
	return ladder{advise: 20_000}
}

// mainCall is one main-thread call as the cost rules read it.
type mainCall struct {
	// known is whether the host said the caller is the main thread; inferred, every
	// refusal is said as advice.
	known   bool
	billing types.AgentBilling
	window  int64
	// class is the call's delegable class, "" for a call no subagent would make for it.
	class string
	line  string
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
func mainFindings(setting func(string) builtin.Setting, st costState, c mainCall) []costFinding {
	var out []costFinding
	s := setting(string(denyRuleRootContextBudget))
	if l := settingLadder(s, s.Tokens, budgetFigures(c.billing, c.window)); st.Tokens > 0 {
		switch {
		case l.deny > 0 && st.Tokens >= int64(l.deny) && c.class != "":
			out = append(out, costFinding{rule: denyRuleRootContextBudget, deny: true, key: hint.MarkerKind(denyRuleRootContextBudget),
				say: fmt.Sprintf("magus workspace: this session's context is %s tokens, past the %s at which this workspace hands %s work to a subagent; spawn one and read its report.\n", kTokens(st.Tokens), kTokens(int64(l.deny)), c.class) +
					"Every call re-sends the whole context, so work done here costs its size on each call after it, while a subagent starts small and returns only its findings."})
		case l.advise > 0 && st.Tokens >= int64(l.advise):
			out = append(out, costFinding{rule: denyRuleRootContextBudget, key: hint.MarkerKind(fmt.Sprintf("%s-%d", denyRuleRootContextBudget, l.advise)),
				say: fmt.Sprintf("magus workspace: this session's context is %s tokens and every call re-sends all of it; hand searches, reads, diffs and runs to a subagent and keep its report.", kTokens(st.Tokens))})
		}
	}
	if c.class != "" {
		s = setting(string(denyRuleRootHeavyCommand))
		n := st.Streak + 1
		switch l := settingLadder(s, s.Calls, heavyFigures); {
		case l.deny > 0 && n >= l.deny:
			out = append(out, costFinding{rule: denyRuleRootHeavyCommand, deny: true, key: hint.MarkerKind(denyRuleRootHeavyCommand),
				say: fmt.Sprintf("magus workspace: this is the session's %s delegable call since it last spawned a subagent; spawn one for this %s work and read its report.\n", ordinal(n), c.class) +
					"Each result lands in the context every later call re-sends; a subagent pays for it once, from a small context."})
		case l.advise > 0 && n >= l.advise:
			out = append(out, costFinding{rule: denyRuleRootHeavyCommand, key: hint.MarkerKind(denyRuleRootHeavyCommand),
				say: fmt.Sprintf("magus workspace: %d searches, reads, diffs or runs in a row since the last spawn; a subagent does this work from a small context and returns only what matters.", n)})
		}
		s = setting(string(denyRuleRootRepeat))
		n = st.Repeats[lineDigest(c.line)] + 1
		switch l := settingLadder(s, s.Calls, repeatFigures); {
		case l.deny > 0 && n >= l.deny:
			out = append(out, costFinding{rule: denyRuleRootRepeat, deny: true, key: hint.MarkerKind(denyRuleRootRepeat),
				say: fmt.Sprintf("magus workspace: this session already ran this exact command %d times with no write since; its output is in the context already.\n", n-1) +
					"Read the earlier result, or spawn a subagent to watch for a change."})
		case l.advise > 0 && n >= l.advise:
			out = append(out, costFinding{rule: denyRuleRootRepeat, key: hint.MarkerKind(denyRuleRootRepeat),
				say: "magus workspace: this session ran this exact command before with no write since, and its output is still in the context."})
		}
	}
	s = setting(string(denyRuleRootResultCap))
	l := settingLadder(s, s.Chars, resultFigures)
	if l.advise == 0 {
		l.advise = l.deny
	}
	if l.advise > 0 {
		for _, tool := range slices.Sorted(maps.Keys(st.Results)) {
			chars := st.Results[tool]
			if chars < l.advise {
				continue
			}
			key := hint.MarkerKind(denyRuleRootResultCap)
			if s.Profile != types.GuardProfileLight {
				key += hint.MarkerKind("-" + lineDigest(tool))
			}
			out = append(out, costFinding{rule: denyRuleRootResultCap, key: key,
				say: fmt.Sprintf("magus workspace: one %s result put %d characters into this session's context, which every later call re-sends; a subagent reading it returns only what matters.", tool, chars)})
		}
	}
	return out
}

// kTokens renders a token count in thousands.
func kTokens(n int64) string { return fmt.Sprintf("%dk", n/1000) }

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return fmt.Sprintf("%dth", n)
	case n%10 == 1:
		return fmt.Sprintf("%dst", n)
	case n%10 == 2:
		return fmt.Sprintf("%dnd", n)
	case n%10 == 3:
		return fmt.Sprintf("%drd", n)
	}
	return fmt.Sprintf("%dth", n)
}

// applyFindings puts findings on verdict: the first refusal replaces it when the thread is
// known, and every other finding advises once per session, appended to advice already
// there. An inferred thread is told a refusal as advice.
func applyFindings(markers hint.Gate, verdict Verdict, findings []costFinding, known bool) Verdict {
	for _, f := range findings {
		if f.deny && known {
			return Verdict{SchemaVersion: verdict.SchemaVersion, Decision: decisionDeny, Reason: f.say, Rule: string(f.rule), Lease: verdict.Lease, LeaseFrom: verdict.LeaseFrom}
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

// settingOf adapts deps.setting to the lookup mainFindings takes.
func (d Dependencies) settingOf(rule string) builtin.Setting {
	s, _ := d.setting(rule)
	return s
}

// gradeMainCommand judges a shell command of a session's unleased main thread by the cost
// rules. A served next is magus's own suggestion and is left alone.
func gradeMainCommand(deps Dependencies, verdict Verdict, in commandRuleInput, who hookAttribution, at location) Verdict {
	if verdict.Decision == decisionDeny || in.preauth != "" || in.lease != "" || at.cacheDir == "" {
		return verdict
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	thread, known := callerThread(at.cacheDir, who, in.lease)
	if thread != types.AgentThreadMain {
		return verdict
	}
	st := readCostState(facts)
	call := mainCall{known: known, billing: accountAt(at.workspace).billing, window: contextWindow(st, at), class: delegableClass(in.command), line: in.command}
	return applyFindings(hint.NewGate(at.cacheDir, who.callerKey()), verdict, mainFindings(deps.settingOf, st, call), known)
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
			if f.deny && c.known {
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
// names the model the child runs on, and whether a read-only job runs on one of the
// costliest.
func gradeMainSpawn(ctx context.Context, deps Dependencies, verdict Verdict, env hookRequest, who hookAttribution, at location, lease string) Verdict {
	if verdict.Decision == decisionDeny || !env.IsSpawn || lease != "" || at.cacheDir == "" {
		return verdict
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	thread, known := callerThread(at.cacheDir, who, lease)
	if thread != types.AgentThreadMain {
		return verdict
	}
	models := accountAt(at.workspace).models
	row, hasRow := spawnTitleJob(ctx, at, env.Spawn.Description)
	var findings []costFinding
	switch s := deps.settingOf(string(denyRuleSpawnNamesModel)); {
	case s.Decision == builtin.Off:
	case env.DeclaredModel == "":
		findings = append(findings, costFinding{rule: denyRuleSpawnNamesModel, deny: s.Decision == builtin.Deny, key: hint.MarkerKind(denyRuleSpawnNamesModel),
			say: "magus workspace: this spawn names no model, so the subagent runs on this session's own; name the model the job's work needs.\n" +
				"A spawn that inherits runs every scout and every search on the costliest model the session uses."})
	case s.Decision == builtin.Deny && hasRow && row.Model != "" && differentModels(env.DeclaredModel, row.Model, models):
		findings = append(findings, costFinding{rule: denyRuleSpawnNamesModel, deny: true, key: hint.MarkerKind(denyRuleSpawnNamesModel),
			say: fmt.Sprintf("magus workspace: this spawn runs %s, and job %s was forked for %s; spawn it on that model, or re-fork the job with the one it needs.", env.DeclaredModel, row.ID, row.Model)})
	}
	if s := deps.settingOf(string(denyRuleSpawnPrincipalForEconomy)); s.Decision != builtin.Off {
		runs := cmp.Or(env.DeclaredModel, readCostState(facts).Model)
		// The host's own read-only agent type stands in for a read-only job at strict.
		explore := s.Profile == types.GuardProfileStrict && strings.EqualFold(env.Spawn.AgentType, "explore")
		if (hasRow && row.ReadOnly || explore) && principalPriced(runs, models) {
			findings = append(findings, costFinding{rule: denyRuleSpawnPrincipalForEconomy, key: hint.MarkerKind(denyRuleSpawnPrincipalForEconomy),
				say: fmt.Sprintf("magus workspace: this read-only spawn runs on %s, among the costliest models in the table; a cheaper one reads and searches as well.", runs)})
		}
	}
	return applyFindings(hint.NewGate(at.cacheDir, who.callerKey()), verdict, findings, known)
}

// differentModels reports whether a and b name two rows of the table. A name the table
// does not resolve, such as a tier word, differs from nothing.
func differentModels(a, b string, t model.Table) bool {
	ra, okA := resolveModel(a, t)
	rb, okB := resolveModel(b, t)
	return okA && okB && ra.ID != rb.ID
}

// principalPriced reports whether id resolves to a row whose input price is above the
// table's median: the costliest models, by the table rather than by name.
func principalPriced(id string, t model.Table) bool {
	row, ok := resolveModel(id, t)
	if !ok || row.Input == 0 {
		return false
	}
	var prices []float64
	for _, r := range t.Rows() {
		if r.Input > 0 {
			prices = append(prices, r.Input)
		}
	}
	slices.Sort(prices)
	return row.Input > prices[len(prices)/2]
}
