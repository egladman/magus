package guard

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// judgeHandedCommands refuses a stop whose reply hands the person a shell block
// the guard would deny them too. The block is judged as a person's call, with no
// session, agent or lease. ok is false when the reply passes.
func judgeHandedCommands(ctx context.Context, deps Dependencies, req Request, dir, raw string) (verdict.Judgment, bool) {
	env, isStop := decodeStop(raw)
	if !isStop || env.LastAssistantMessage == "" || (env.StopHookActive != nil && *env.StopHookActive) {
		return verdict.Judgment{}, false
	}
	for _, block := range shellBlocks(env.LastAssistantMessage) {
		cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(block), "!"))
		if cmd == "" {
			continue
		}
		v := Judge(ctx, deps, Request{Input: cmd, DryRun: true, Dir: dir, Authority: req.Authority})
		if v.Decision != verdict.Deny || agentOnlyRule(v.Rule) {
			continue
		}
		first, _, _ := strings.Cut(v.Reason, "\n")
		return verdict.Judgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   verdict.Deny,
			Reason: fmt.Sprintf("this reply hands the person `%s`, a command the guard denies [%s]: %s\nA person's command reaches no hook, so fix the command before handing it over.",
				elideCommand(cmd), v.Rule, first),
			Rule: string(denyRuleBriefCommand),
		}, true
	}
	return verdict.Judgment{}, false
}

// agentOnlyRule reports whether rule's catalog row binds an agent alone. A workspace
// rule has no row, so it refuses a handed command.
func agentOnlyRule(rule string) bool {
	return slices.ContainsFunc(ruleDocRegistry, func(e ruleDocEntry) bool {
		return e.doc.Name == rule && e.audience == audienceAgent
	})
}
