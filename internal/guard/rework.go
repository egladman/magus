package guard

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/codec"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

const denyRuleSpawnColdContinuation denyRuleName = "continue-cold-worker"

// recordRework counts one rework round on the job whose worker addressed was sent a
// continuation after handing back: the worker's spawn title names a job that has exited or
// ended. The model-tier lens reads the count, as evidence that a tier needed another round.
//
// It fails open and records nothing it cannot attribute: no cache, an unreadable store, a
// spawn the spawner could not hand a job out in (see [spawnedAgent.UntrustedJob]), a job
// still taken, and a dry run. It never changes the verdict.
func recordRework(ctx context.Context, at location, facts hint.Gate, addressed string) {
	if isDryRun(ctx) {
		return
	}
	if row, ok := handedBackRow(ctx, at, facts, addressed); ok {
		_ = job.NewStore(at.jobs()).RecordRework(ctx, row.ID)
	}
}

// handedBackRow is the row addressed's spawn title names when it has exited or
// ended; false when nothing attributes one.
func handedBackRow(ctx context.Context, at location, facts hint.Gate, addressed string) (jobtypes.Job, bool) {
	row, ok := continuedJob(ctx, at, facts, addressed)
	if !ok || (row.State != jobtypes.StateExited && !row.State.Terminal()) {
		return jobtypes.Job{}, false
	}
	return row, true
}

// continuedJob is the job the addressed worker was spawned to take. It is kept
// Separate from handedBackRow: an active row can be cold when its worker
// sits idle past TTL.
func continuedJob(ctx context.Context, at location, facts hint.Gate, addressed string) (jobtypes.Job, bool) {
	if at.cacheDir == "" || addressed == "" {
		return jobtypes.Job{}, false
	}
	rec, ok := readSpawnedAgent(facts, resolveAgentID(facts, addressed))
	if !ok || rec.UntrustedJob != "" {
		return jobtypes.Job{}, false
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return jobtypes.Job{}, false
	}
	row, ok := spawnTitleRow(rows, rec.Description)
	if !ok {
		return jobtypes.Job{}, false
	}
	return row, true
}

// coldContinuation explains why a continuation cannot be known to resume a
// warm, in-budget context. Unknown evidence is cold: dispatch must not silently
// authorize an unpriced model call.
func coldContinuation(ctx context.Context, at location, facts hint.Gate, addressed string, now time.Time) (string, bool) {
	row, ok := continuedJob(ctx, at, facts, addressed)
	if !ok {
		return "the worker's job or last activity is unknown", true
	}
	ttl := resumeTTL(accountAt(at.workspace))
	if ttl.ttl <= 0 {
		return "the prompt-cache TTL is unknown", true
	}
	since, seen := facts.LastSeen(agentSeenKind(resolveAgentID(facts, addressed)))
	if !seen {
		updated := max(row.Updated, row.HolderStopped)
		if updated == 0 {
			return "the worker's last activity is unknown", true
		}
		since = time.Unix(updated, 0)
	}
	idle := max(now.Sub(since), 0)
	if idle >= ttl.ttl {
		return fmt.Sprintf("the worker has been idle %s, past its %s prompt-cache TTL", idle.Round(time.Second), ttl.ttl), true
	}

	rec, ok := readSpawnedAgent(facts, resolveAgentID(facts, addressed))
	if !ok || rec.ContextTokens == nil {
		return "the worker's context size is unknown", true
	}
	modelID := rec.Model
	if modelID == "" {
		modelID = readCostState(facts).Model
	}
	if modelID == "" || at.models == nil {
		return "the worker's model cost is unknown", true
	}
	models := at.models()
	modelKnown := false
	for _, m := range models {
		if m.ID == modelID || m.Alias != "" && m.Alias == modelID {
			modelKnown = true
			break
		}
	}
	if !modelKnown {
		return "the worker's model cost is unknown", true
	}
	if nearPriceStep(rec, modelID, models) {
		return "the worker is at the model's long-context price boundary", true
	}
	return "", false
}

func coldContinuationVerdict(ctx context.Context, at location, facts hint.Gate, env hookRequest, lease string, req Request, judged verdict.Judgment, now time.Time) (verdict.Judgment, bool) {
	addressed := env.Target
	reason, cold := coldContinuation(ctx, at, facts, addressed, now)
	if !cold {
		return judged, false
	}
	why := "continuing this worker may start another model turn, but its warm, in-budget context cannot be established: " + reason
	decision := verdict.Deny
	if lease != "" {
		why += ". A leased worker cannot approve its own spend; take the decision to the root session for human approval."
	} else {
		if req.RendersAsk {
			decision = verdict.Ask
			why += ". Approve this cold continuation before the host dispatches it."
		} else {
			why += ". This host cannot render a human approval prompt."
		}
		row, ok := continuedJob(ctx, at, facts, addressed)
		rule := continuationKey(facts, row, env, req.Input)
		if ok && row.State.Live() && rule != "" && !isDryRun(ctx) {
			spent, err := job.NewStore(at.jobs()).ConsumeGrant(ctx, row, rule)
			if err != nil {
				why += " Could not verify a one-use approval: " + err.Error()
			} else if spent {
				return judged, false
			}
			why += fmt.Sprintf(" To approve this exact call for five minutes, run in your terminal: magus job grant %s %s", row.ID, rule)
		}
	}
	return verdict.Judgment{
		APIVersion: judged.APIVersion,
		Decision:   decision,
		Reason:     why,
		Context:    judged.Context,
		Rule:       string(denyRuleSpawnColdContinuation),
		Lease:      judged.Lease,
	}, true
}

// A retry gets a new call ID even when the tool arguments match.
func continuationKey(facts hint.Gate, row jobtypes.Job, env hookRequest, input string) string {
	var envelope struct {
		Args map[string]any `json:"tool_input"`
	}
	if codec.UnmarshalLossless([]byte(input), &envelope) != nil || envelope.Args == nil {
		return ""
	}
	id := resolveAgentID(facts, env.Target)
	rec, ok := readSpawnedAgent(facts, id)
	if !ok || id == "" || facts.Session() == "" || env.Tool == "" {
		return ""
	}
	parts := []any{facts.Session(), row.ID, row.Generation, id, rec.Model, env.Tool, envelope.Args}
	data, err := codec.Marshal(parts)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s%x", continuationPrefix, sha256.Sum256(data))
}

// adviseContinueHandedBack tells the root the worker it continues handed its
// job back. It never denies, so the root keeps its own call.
func adviseContinueHandedBack(ctx context.Context, at location, facts hint.Gate, addressed string) advice {
	row, ok := handedBackRow(ctx, at, facts, addressed)
	if !ok {
		return advice{}
	}
	return handedBackAdvice(row)
}

func handedBackAdvice(row jobtypes.Job) advice {
	if row.State.Terminal() {
		return advice{
			Say: fmt.Sprintf("magus workspace: job %s is %s; fork a new job for more work.", row.ID, row.State),
			Why: "An ended job grades nothing and the worker holds no write paths, so its writes in its checkout are denied.",
		}
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: job %s is exited; widen it yourself with `%s`, or the worker re-takes it with `%s`.",
			row.ID, hint.JobApply, hint.JobExec.With(row.ID)),
		Why: "The worker stays bound to the exited job's write paths, and a write outside them is denied.",
	}
}
