package guard

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// advisoryModelTierContextCap advises a leased worker whose context passed the cap of the
// model tier its row was forked at. Raised to deny, it refuses the worker's writes.
const advisoryModelTierContextCap hint.MarkerKind = "model-tier-context-cap"

// adviseModelTierContext is model-tier-context-cap's advice for a write under lease, empty
// when the rule is off, the lease names no live row with a tier, the tier has no cap, or
// the host has not reported the worker's context past it.
func adviseModelTierContext(ctx context.Context, deps Dependencies, at location, who hookAttribution, facts hint.Gate, lease string) advice {
	if lease == "" || at.cacheDir == "" {
		return advice{}
	}
	if s, _ := deps.setting(string(advisoryModelTierContextCap)); s.Decision == builtin.Off {
		return advice{}
	}
	tokens := reportedContext(facts, who.Agent)
	if tokens <= 0 {
		return advice{}
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return advice{}
	}
	me, ok := liveLease(liveLeases(rows), lease)
	if !ok || me.ModelTier == nil {
		return advice{}
	}
	limit := tierContextCap(deps, facts, me)
	if limit <= 0 || tokens <= limit {
		return advice{}
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: lease %s runs at model tier %s, which caps a worker's context at %s tokens, and yours has reached %s: report what you have and stop.",
			me.ID, me.ModelTier.Name, kTokens(limit), kTokens(tokens)),
		Why: fmt.Sprintf("The bounds of model tier %s (rank %d) in the workspace's model-tier declaration cap its workers' context, since a smaller model past a large context works worse and resends all of it every turn. "+
			"Whether the rest of the job is re-forked at a higher tier or split smaller is the root's call, and it needs your report to make it.",
			me.ModelTier.Name, me.ModelTier.Rank),
	}
}

// reportedContext is the caller's context in tokens as its host last reported it: a
// subagent's from the usage recorded for it, a session's own from its transcript. Zero
// when nothing was reported.
func reportedContext(facts hint.Gate, agentID string) int64 {
	if agentID == "" {
		return readCostState(facts).Tokens
	}
	rec, _ := readSpawnedAgent(facts, agentID)
	if rec.ContextTokens == nil {
		return 0
	}
	return *rec.ContextTokens
}

// tierContextCap is the context cap of the tier row was forked at, 0 for none. The answer
// is kept in the session's facts by row and tier: the declaration is read by opening the
// workspace, which a worker's every write must not pay for. A declaration that could not be
// read is not kept, so the next write asks again.
func tierContextCap(deps Dependencies, facts hint.Gate, row types.Job) int64 {
	kind := agentMarkerKind("model-tier-cap-", row.ID+"\x00"+row.ModelTier.Name+"\x00"+strconv.Itoa(row.ModelTier.Rank))
	path := ""
	if facts.CacheDir() != "" {
		path = hint.MarkerPath(facts.CacheDir(), facts.Session(), kind)
		if body, err := os.ReadFile(path); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64); err == nil {
				return n
			}
		}
	}
	if deps.ModelTiers == nil {
		return 0
	}
	set := deps.ModelTiers()
	if set == nil {
		return 0
	}
	var limit int64
	if forked, ok := tierIn(set, row); ok {
		limit = forked.set.Bounds(forked.tier).MaxContext
	}
	if path != "" {
		_ = file.ReplaceFile(path, []byte(strconv.FormatInt(limit, 10)), 0o644)
	}
	return limit
}
