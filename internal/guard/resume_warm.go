package guard

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

const denyRuleSpawnWarmWorker denyRuleName = "spawn-warm-worker"

// priceStepMargin is the share of a model's long-context price step a warm worker's context
// may sit below it and still pass a fresh spawn: one more round of follow-up work crosses it.
const priceStepMargin = 0.1

// promptCacheTTL is the TTL a warm worker is measured against and the sentence naming where
// it came from.
type promptCacheTTL struct {
	ttl    time.Duration
	source string
}

// resumeTTL is the shortest window the published prompt-cache table holds: no harness model
// row carries a TTL, and the shortest window never calls a cold cache warm.
func resumeTTL() promptCacheTTL {
	var best promptCacheTTL
	for _, p := range sessions.PromptCacheProviders {
		for _, w := range p.Windows {
			if best.ttl == 0 || w.Min < best.ttl {
				best = promptCacheTTL{ttl: w.Min, source: fmt.Sprintf(
					"no harness model row carries a prompt-cache TTL, so this is the shortest published window, %s %s, from %s", p.Provider, w.Window, p.Source)}
			}
		}
	}
	return best
}

// warmWorker is a worker this session spawned whose job ended within the TTL, and which a
// spawn's job continues.
type warmWorker struct {
	agent string
	job   string
	link  string
	idle  time.Duration
	left  time.Duration
}

// findWarmWorker is the warm worker row continues: a spawn record of facts whose job ended
// within ttl of now and that row names in Follows or shares a write path with. False when
// row carries a FreshReason, or when every such worker is near its model's price step.
func findWarmWorker(facts hint.Gate, rows []types.Job, row types.Job, models []types.ModelPricing, ttl time.Duration, now time.Time) (warmWorker, bool) {
	if row.FreshReason != "" || ttl <= 0 {
		return warmWorker{}, false
	}
	session := readCostState(facts).Model
	for _, kind := range facts.Kinds(spawnedAgentPrefix) {
		rec, ok := readMarker[spawnedAgent](facts, kind)
		if !ok || rec.UntrustedJob != "" {
			continue
		}
		agent := cmp.Or(rec.ID, rec.Name)
		ended, ok := spawnTitleRow(rows, rec.Description)
		if !ok || agent == "" || ended.ID == row.ID || ended.State == "" || ended.State.Editing() {
			continue
		}
		link := continuationLink(row, ended)
		if link == "" {
			continue
		}
		since := time.Unix(ended.Updated, 0)
		if seen, ok := facts.LastSeen(agentSeenKind(rec.ID)); rec.ID != "" && ok {
			since = seen
		}
		idle := max(now.Sub(since), 0)
		if idle >= ttl || nearPriceStep(rec, cmp.Or(rec.Model, session), models) {
			continue
		}
		return warmWorker{agent: agent, job: ended.ID, link: link, idle: idle, left: ttl - idle}, true
	}
	return warmWorker{}, false
}

// continuationLink says how row continues ended, "" when it does not.
func continuationLink(row, ended types.Job) string {
	if row.Follows == ended.ID {
		return "follows " + ended.ID
	}
	for _, p := range row.WritePaths {
		if slices.ContainsFunc(ended.WritePaths, func(q string) bool { return types.PathsIntersect(p, q) }) {
			return "shares " + p + " with " + ended.ID
		}
	}
	return ""
}

// nearPriceStep reports whether the worker's last observed context is within
// priceStepMargin of its model's long-context price step, or past it. False when either
// is unknown: an unpriced model is not a reason to pay orientation again.
func nearPriceStep(rec spawnedAgent, model string, models []types.ModelPricing) bool {
	if rec.ContextTokens == nil || model == "" {
		return false
	}
	i := slices.IndexFunc(models, func(m types.ModelPricing) bool { return m.ID == model || m.Alias != "" && m.Alias == model })
	if i < 0 || models[i].LongContext == nil || models[i].LongContext.Above <= 0 {
		return false
	}
	step := models[i].LongContext.Above
	return float64(*rec.ContextTokens) >= float64(step)*(1-priceStepMargin)
}

func denySpawnWarmWorker(row types.Job, w warmWorker, ttl promptCacheTTL) denial {
	return denial{
		Say: fmt.Sprintf("job %s %s, whose worker %s ended %s ago and stays warm for about %s more: continue agent %s with a message (SendMessage) instead of spawning, or set `fresh_reason` in %s's record and `%s` (TTL %s: %s).",
			row.ID, w.link, w.agent, w.idle.Round(time.Second), w.left.Round(time.Second), w.agent, row.ID, hint.JobApply.With("-f", "<record>"), ttl.ttl, ttl.source),
		Why: "A resumed worker reads its context at the cache-read price and has already oriented; a fresh one pays the prefix and the orientation again for code another context already holds. " +
			"`fresh_reason` is for a context that is wrong for the job, never for one that is merely large.",
	}
}
