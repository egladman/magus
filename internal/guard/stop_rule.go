package guard

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// seamStop is the magus\guard.stop seam.
const seamStop functionSeam = "stop"

// workspaceStopRule is the GuardJudgment.Rule an unnamed magus\guard.stop answer carries.
const workspaceStopRule = workspaceShellPrefix + "stop"

// stopEvents are the host hook events the stop rule is asked about: an agent ending its
// turn. A SessionEnd ends the session and is not a stop a rule could refuse.
var stopEvents = []string{"Stop", "SubagentStop"}

// gradeWorkspaceStop asks the workspace's magus\guard.stop rule about a stop the built-in
// check did not deny, strengthen only. A rule that fails or runs out of time judges
// nothing: refusing a stop on a broken rule would hold the session open with no way to end
// it, so the failure is told beside the verdict instead.
func gradeWorkspaceStop(ctx context.Context, deps Dependencies, verdict types.GuardJudgment, raw string, who hookAttribution, at location, lease string) types.GuardJudgment {
	if deps.StopRule == nil && deps.ApprovedStopRule == nil {
		return verdict
	}
	if verdict.Decision == decisionDeny || !slices.Contains(stopEvents, who.Event) {
		return verdict
	}
	env, ok := decodeStop(raw)
	if !ok {
		return verdict
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	req := stopRequest(ctx, deps, env, who, at, lease, facts)
	bind := func(rule workspace.StopRule) ruleCall {
		if rule == nil {
			return nil
		}
		return func(ctx context.Context) (types.GuardVerdict, error) { return rule(ctx, req, facts) }
	}
	var resolve func(context.Context) (ruleCall, error)
	if deps.ApprovedStopRule != nil {
		resolve = func(ctx context.Context) (ruleCall, error) {
			rule, err := deps.ApprovedStopRule(ctx)
			return bind(rule), err
		}
	}
	asked := askWorkspaceRules(ctx, seamStop, deps, resolve, bind(deps.StopRule))
	decided := ""
	if verdict.Decision != decisionPass {
		decided = decidedByBuiltin
	}
	verdict, _ = applyWorkspaceAnswer(verdict, decided, asked, workspaceStopRule)
	if note := stopFailureNote(asked); note != "" && verdict.Decision != decisionDeny {
		verdict.Context = strings.Join(slices.DeleteFunc([]string{verdict.Context, note}, func(s string) bool { return s == "" }), "\n\n")
		if verdict.Decision == decisionPass {
			verdict.Decision = decisionAdvise
		}
	}
	return verdict
}

// stopRequest is the request the stop rule is handed for env.
func stopRequest(ctx context.Context, deps Dependencies, env stopEnvelope, who hookAttribution, at location, lease string, facts hint.Gate) types.StopRequest {
	spawned := spawnedAs(facts, who.Agent)
	role, row := actingRole(ctx, at, lease, spawned)
	thread, from, window := callerCost(at, who, lease, facts)
	transcript := env.TranscriptPath
	if env.AgentID != "" {
		transcript = cmp.Or(env.AgentTranscriptPath, transcript)
	}
	req := types.StopRequest{
		Host:           who.Host,
		Session:        who.Session,
		Agent:          who.Agent,
		Parent:         spawned,
		Role:           role,
		Lease:          row,
		Thread:         thread,
		ThreadFrom:     from,
		Orchestration:  callerOrchestration(at, who, thread, from, lease),
		Window:         window,
		Event:          who.Event,
		StopHookActive: env.StopHookActive != nil && *env.StopHookActive,
		Transcript:     transcript,
		Dir:            at.dir,
		Workspace:      at.workspace,
	}
	if deps.CheckoutState != nil && at.workspace != "" {
		req.Checkout = deps.CheckoutState(ctx, cmp.Or(at.dir, at.workspace))
	}
	return req
}

// stopFailureNote names each side of the stop rule that judged nothing, "" when every side
// answered.
func stopFailureNote(asked rulesAnswer) string {
	if len(asked.failures) == 0 {
		return ""
	}
	lines := make([]string, len(asked.failures))
	for i, f := range asked.failures {
		lines[i] = fmt.Sprintf("%s: %s", sideName(seamStop, f.Side), f.Error)
	}
	return fmt.Sprintf("magus workspace: %s judged nothing, so this stop stands on the built-in check alone.\n%s",
		seamStop.member(), strings.Join(lines, "\n"))
}
