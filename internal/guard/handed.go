package guard

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/types"
)

// judgeHandedCommands refuses a stop whose reply hands the person a shell block
// the guard would deny the agent. A person's command reaches no hook, so
// passing it to them skips the denial. ok is false when the reply passes.
func judgeHandedCommands(ctx context.Context, deps Dependencies, req Request, dir, raw string) (types.GuardJudgment, bool) {
	env, isStop := decodeStop(raw)
	if !isStop || env.LastAssistantMessage == "" || (env.StopHookActive != nil && *env.StopHookActive) {
		return types.GuardJudgment{}, false
	}
	for _, block := range shellBlocks(env.LastAssistantMessage) {
		cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(block), "!"))
		if cmd == "" {
			continue
		}
		asked := req
		asked.Input, asked.DryRun, asked.Dir, asked.Event, asked.Transcript = cmd, true, dir, "", ""
		v := Judge(ctx, deps, asked)
		if v.Decision != decisionDeny {
			continue
		}
		first, _, _ := strings.Cut(v.Reason, "\n")
		return types.GuardJudgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   decisionDeny,
			Reason: fmt.Sprintf("this reply hands the person `%s`, a command the guard denies [%s]: %s\nA person's command reaches no hook, so fix the command before handing it over.",
				elideCommand(cmd), v.Rule, first),
			Rule: string(denyRuleSpawnCommand),
		}, true
	}
	return types.GuardJudgment{}, false
}
