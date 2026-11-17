package guard

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// hostRefusal is why a call cannot be judged under its --agent-name, and the
// command that gets the person unstuck.
type hostRefusal struct {
	err  error
	next types.NextStep
}

// harnessInstallStep is the command that writes the tool rows of host.
func harnessInstallStep(host string) types.NextStep {
	argv := []string{"magus", "agent", "harness", "install", "--id", host}
	return types.NextStep{
		ID: "harness-install", Run: strings.Join(argv, " "), Argv: argv,
		Why: fmt.Sprintf("installs the tool rows of %s, when the workspace loads that harness", host),
	}
}

// applyHostFacts fills req from the tool rows installed for its host, whose limits file
// install writes from the host's harness spell, so the glue names only the host and the
// hook never loads the workspace for them.
// It refuses a host nothing installed names, a payload another host sent, and tool rows
// installed for the host that another magus stamped, naming the install that rewrites them.
// With nothing installed the gap is logged and the limits come back false.
func (e HookEnv) applyHostFacts(ctx context.Context, req *Request) (types.Limits, bool, *hostRefusal) {
	if strings.TrimSpace(req.Host) == "" {
		return types.Limits{}, false, nil
	}
	root, err := e.FindRoot("")
	if err != nil {
		root = ""
	}
	installed := agent.InstalledHosts(root)
	if err := agent.CheckHostInstalled(installed, req.Host); err != nil {
		return types.Limits{}, false, &hostRefusal{err: err, next: harnessInstallStep(req.Host)}
	}
	if err := agent.CheckHostPayload(installed, req.Host, req.Input); err != nil {
		argv := []string{"magus", "describe", "harness"}
		return types.Limits{}, false, &hostRefusal{err: err, next: types.NextStep{
			ID: "harness-describe", Run: strings.Join(argv, " "), Argv: argv,
			Why: "prints each harness's hook entries, each naming its own host",
		}}
	}
	req.Tools = agent.FindToolRows(root, req.Host)
	limits, err := req.Tools.Limits()
	if err != nil {
		if _, ok := installed[req.Host]; ok {
			return types.Limits{}, false, &hostRefusal{err: err, next: harnessInstallStep(req.Host)}
		}
		slog.WarnContext(ctx, "the guard judges without the host's limits and skill-load capability",
			attr.Error(err), attr.Notice(""), attr.Component("magus shell"))
		return types.Limits{}, false, nil
	}
	req.ObservesSkillLoads = limits.ObservesSkillLoads
	req.HostForegroundDefault = time.Duration(limits.ForegroundDefaultMs) * time.Millisecond
	req.HostForegroundMax = time.Duration(limits.ForegroundMaxMs) * time.Millisecond
	req.HostResumesOnBackgroundFrom = limits.ResumesOnBackgroundFrom
	return limits, true, nil
}

// routeDeny is quiet.Wrap's decision for a hook reply: a host whose harness declares no
// capture channel gets a deny's verdict line alone, since there all a reply carries reaches
// the person too. Limits that name no usable channel leave the reason whole and say so.
func routeDeny(ctx context.Context, limits types.Limits, known bool, verdict types.GuardJudgment) types.GuardJudgment {
	if !known || verdict.Decision != "deny" {
		return verdict
	}
	channels, err := agent.ParseHarnessChannels(limits.Channels)
	if err != nil {
		slog.WarnContext(ctx, "the deny goes out whole because the host's installed limits name no usable channel; rerun `magus agent harness install`",
			attr.Error(err), attr.Notice(""), attr.Component("magus shell"))
		return verdict
	}
	if !slices.Contains(channels, agent.ChannelCapture) {
		verdict.Reason, _, _ = strings.Cut(verdict.Reason, "\n")
	}
	return verdict
}
