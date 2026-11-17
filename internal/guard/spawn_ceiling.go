package guard

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
)

// denyRuleSpawnAboveParentTier judges a spawn whose model's tier ranks above
// its spawner's, as `magus job fork` judges a child row. It advises by
// default and refuses only under a deny setting.
const denyRuleSpawnAboveParentTier denyRuleName = "spawn-above-parent-tier"

// gradeSpawnCeiling judges a spawn by spawn-above-parent-tier. Both models resolve to tiers
// by an exact alias of the spawning host in the workspace's declaration, or by the exact id
// of a mgs_listModels row that names a tier, and compare by rank. A spawn naming no model
// runs on its spawner's, and a spawner with no reported model is not judged. Any other model
// has an unknown tier, never one inferred from its name, and the rule advises naming the row
// to add.
//
// The spawner's model is proven when its host reported it: a main thread's from its
// transcript once the host has reported the thread, a subagent's from the model its own
// spawn named. A subagent spawned on no model is taken to run on the session's, inferred,
// and an inferred ceiling only advises.
func gradeSpawnCeiling(deps Dependencies, judged verdict.Judgment, env hookRequest, who hookAttribution, at location, lease string) verdict.Judgment {
	if judged.Decision == verdict.Deny || !env.IsSpawn || env.DeclaredModel == "" || at.cacheDir == "" || deps.ModelTiers == nil {
		return judged
	}
	s := deps.settingLookup(string(denyRuleSpawnAboveParentTier))
	if s.Decision == builtin.Off {
		return judged
	}
	spawner := spawnerModel(at, who, lease)
	if spawner.model == "" {
		return judged
	}
	set := deps.ModelTiers()
	if set == nil {
		return judged
	}
	host := who.Host
	from := types.ThreadInferred
	if spawner.proven {
		from = types.ThreadReported
	}
	gate := hint.NewGate(at.cacheDir, who.callerKey())
	var rows []model.Row
	if at.models != nil {
		rows = at.models()
	}
	child, childKnown := modelTierByExactID(set, host, env.DeclaredModel, rows)
	parent, parentKnown := modelTierByExactID(set, host, spawner.model, rows)
	if !childKnown || !parentKnown {
		var unknown, missing []string
		if !childKnown {
			unknown = append(unknown, "this spawn's model, "+env.DeclaredModel)
			missing = append(missing, env.DeclaredModel)
		}
		if !parentKnown {
			unknown = append(unknown, fmt.Sprintf("%s, %s", spawner.says, spawner.model))
			missing = append(missing, spawner.model)
		}
		why := "is in no mgs_listModels row and no alias the declaration maps, so its model tier is unknown"
		if len(unknown) == 2 {
			why = "are in no mgs_listModels row and no alias the declaration maps, so their model tiers are unknown"
		}
		return applyFindings(gate, judged, []costFinding{{
			rule: denyRuleSpawnAboveParentTier,
			key:  hint.MarkerKind(denyRuleSpawnAboveParentTier) + "-unknown",
			say: fmt.Sprintf("magus workspace: this spawn's tier ceiling is not judged: %s %s (%s): "+
				"add a row for %s with a tier to the %s spell's mgs_listModels, or map it in that declaration.",
				strings.Join(unknown, " and "), why, declaredAliases(set, host), strings.Join(missing, " and "), host),
		}}, from)
	}
	if deps.TierRule == nil {
		return applyFindings(gate, judged, []costFinding{{
			rule: denyRuleSpawnAboveParentTier,
			key:  hint.MarkerKind(denyRuleSpawnAboveParentTier) + "-unruled",
			say:  "magus workspace: this spawn's tier ceiling is not judged: the guard was wired with no spawn rule (magus.WithSpawnRule).",
		}}, from)
	}
	if deps.TierRule(parent, child) == nil {
		return judged
	}
	// A parent resolved through a harness row runs on a host the declaration does not map,
	// so the way down is named by that row's id.
	alias, err := set.Alias(host, parent)
	if err != nil {
		alias = spawner.model
	}
	say := fmt.Sprintf("magus workspace: this spawn runs %s, model tier %s (rank %d), above %s, %s, model tier %s (rank %d); %s",
		env.DeclaredModel, child.Name(), child.Rank(), spawner.says, spawner.model, parent.Name(), parent.Rank(), job.ModelCost(rows, env.DeclaredModel, spawner.model))
	if s.Decision == builtin.Deny {
		say += fmt.Sprintf(": this workspace sets %s to deny, so spawn it on %s or lower.", denyRuleSpawnAboveParentTier, alias)
	} else {
		say += fmt.Sprintf(". %s stays at the parent's tier, and setting %s off in magus\\guard.builtins stops this advice.", alias, denyRuleSpawnAboveParentTier)
	}
	return applyFindings(gate, judged, []costFinding{{
		rule: denyRuleSpawnAboveParentTier,
		deny: s.Decision == builtin.Deny,
		key:  hint.MarkerKind(denyRuleSpawnAboveParentTier),
		say:  say,
	}}, from)
}

// SpawnCeilingSetting is root's approved spawn-above-parent-tier setting, as
// LoadApprovedBuiltins resolves it: the setting `magus job fork` holds a row
// above its parent's tier to. A magus.yaml that does not load is an error.
func SpawnCeilingSetting(ctx context.Context, root string) (builtin.Setting, error) {
	settings, err := LoadApprovedBuiltins(ctx, root)
	if err != nil {
		return builtin.Setting{}, fmt.Errorf("reading the %s setting: %w", denyRuleSpawnAboveParentTier, err)
	}
	s, ok := settings[string(denyRuleSpawnAboveParentTier)]
	if !ok {
		return builtin.Setting{}, fmt.Errorf("guard: the resolved builtins carry no %s setting", denyRuleSpawnAboveParentTier)
	}
	return s, nil
}

// modelTierByExactID is id's tier: the case the declaration maps id to for host, else the
// tier the mgs_listModels row whose id or alias is exactly id names. False for any other
// model.
func modelTierByExactID(set *tier.Set, host, id string, rows []model.Row) (tier.Tier, bool) {
	if t, ok := set.Resolve(host, id); ok {
		return t, true
	}
	i := slices.IndexFunc(rows, func(r model.Row) bool {
		return r.Tier != "" && (r.ID == id || r.Alias != "" && r.Alias == id)
	})
	if i < 0 {
		return tier.Tier{}, false
	}
	t, err := set.Parse(rows[i].Tier)
	return t, err == nil
}

// declaredAliases says which aliases set maps for host, cheapest first, or why it maps none.
func declaredAliases(set *tier.Set, host string) string {
	var aliases []string
	for _, t := range set.All() {
		alias, err := set.Alias(host, t)
		if err != nil {
			return err.Error()
		}
		aliases = append(aliases, alias)
	}
	return fmt.Sprintf("the declaration at %s maps %s for %s", set.Site(), strings.Join(aliases, ", "), host)
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
