package guard

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// denyRuleSpawnAboveParentTier refuses a spawn whose model's tier ranks above the tier of
// the model its spawner runs on, the ceiling `magus job fork` holds a child row to.
const denyRuleSpawnAboveParentTier denyRuleName = "spawn-above-parent-tier"

// gradeSpawnCeiling judges a spawn by spawn-above-parent-tier. Both models resolve to tiers
// through the spawning host's aliases in the workspace's declaration and compare by rank. A
// spawn naming no model runs on its spawner's, and a model no tier maps is never judged.
//
// The spawner's model is proven when its host reported it: a main thread's from its
// transcript once the host has reported the thread, a subagent's from the model its own
// spawn named. A subagent spawned on no model is taken to run on the session's, inferred,
// and an inferred ceiling only advises.
func gradeSpawnCeiling(deps Dependencies, verdict types.GuardJudgment, env hookRequest, who hookAttribution, at location, lease string) types.GuardJudgment {
	if verdict.Decision == decisionDeny || !env.IsSpawn || env.DeclaredModel == "" || at.cacheDir == "" || deps.ModelTiers == nil {
		return verdict
	}
	s := deps.settingLookup(string(denyRuleSpawnAboveParentTier))
	if s.Decision == builtin.Off {
		return verdict
	}
	spawner := spawnerModel(at, who, lease)
	if spawner.model == "" {
		return verdict
	}
	set := deps.ModelTiers()
	if set == nil {
		return verdict
	}
	host := types.HarnessKey{ID: who.Host}
	child, ok := modelTier(set, host, env.DeclaredModel)
	if !ok {
		return verdict
	}
	parent, ok := modelTier(set, host, spawner.model)
	if !ok || child.Within(parent) == nil {
		return verdict
	}
	// modelTier found the host mapped, so Alias cannot fail here.
	alias, _ := set.Alias(host, parent)
	from := types.ThreadInferred
	if spawner.proven {
		from = types.ThreadReported
	}
	return applyFindings(hint.NewGate(at.cacheDir, who.callerKey()), verdict, []costFinding{{
		rule: denyRuleSpawnAboveParentTier,
		deny: s.Decision == builtin.Deny,
		key:  hint.MarkerKind(denyRuleSpawnAboveParentTier),
		say: fmt.Sprintf("magus workspace: this spawn runs %s, model tier %s (rank %d), above %s, %s, model tier %s (rank %d); "+
			"a child never outranks its parent, so spawn it on %s or a lower tier, or a person forks the higher-tier row and runs it from a session at that tier.",
			env.DeclaredModel, child.Name(), child.Rank(), spawner.says, spawner.model, parent.Name(), parent.Rank(), alias),
	}}, from)
}

// spawnedOn is the model a spawner runs on, what it rests on, and how a verdict names it.
type spawnedOn struct {
	model  string
	proven bool
	says   string
}

// spawnerModel is the model the caller making a spawn runs on, "" when no host reported one.
// The root session's is the model the person picked, as its transcript records it.
func spawnerModel(at location, who hookAttribution, lease string) spawnedOn {
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	session := readCostState(facts).Model
	if who.Agent != "" {
		if rec, _ := readSpawnedAgent(facts, who.Agent); rec.Model != "" {
			return spawnedOn{model: rec.Model, proven: true, says: "the model this agent was spawned on"}
		}
		return spawnedOn{model: session, says: "the session's model, which this agent inherited"}
	}
	thread, from := callerThread(at.cacheDir, who, lease)
	return spawnedOn{model: session, proven: thread == types.AgentThreadMain && from == types.ThreadReported, says: "this session's model"}
}

// modelTier is the tier host's alias model names in set: the alias itself, else one
// dash-separated word of a model id, the way a transcript names the model an alias picked
// (opus in claude-opus-5-5). A word two tiers' aliases match names no one tier.
func modelTier(set *types.ModelTierSet, host types.HarnessKey, model string) (types.ModelTier, bool) {
	if t, ok := set.Resolve(host, model); ok {
		return t, true
	}
	words := strings.Split(strings.ToLower(strings.TrimSpace(model)), "-")
	var found types.ModelTier
	for _, t := range set.All() {
		alias, err := set.Alias(host, t)
		if err != nil {
			return types.ModelTier{}, false
		}
		if !slices.Contains(words, strings.ToLower(alias)) {
			continue
		}
		if !found.IsZero() {
			return types.ModelTier{}, false
		}
		found = t
	}
	return found, !found.IsZero()
}
