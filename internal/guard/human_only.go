package guard

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/types"
)

// denyRuleHumanOnly names the verdict on a run reaching a target the workspace declares
// human_only.
const denyRuleHumanOnly denyRuleName = "human-only"

// humanOnlyPair is one human_only target a run reaches.
type humanOnlyPair struct {
	project, target, reason string
	skipCache               bool

	// composed is a target some run reaches through another target's ctx.needs chain
	// rather than by naming it.
	composed bool
}

func (p humanOnlyPair) ref() string { return p.project + ":" + p.target }

// gradeHumanOnly is the verdict on command's magus runs: "deny" when any reaches a
// human_only target through a chain, or when a leased worker names one; "ask" when a
// session no lease binds names one; "" when none does or the workspace cannot be read.
func gradeHumanOnly(ctx context.Context, deps Dependencies, actingLease, command string) (decision, reason string) {
	cmds, ok := ParseCommands(command)
	if !ok {
		return "", ""
	}
	var runs []targetRun
	for _, c := range cmds {
		if r, ok := parseTargetRun(c); ok {
			runs = append(runs, r)
		}
	}
	if len(runs) == 0 {
		return "", ""
	}
	at := hookLocation(ctx, deps)
	pairs := humanOnlyReach(ctx, deps, at.workspace, runs)
	if len(pairs) == 0 {
		return "", ""
	}
	line := elideCommand(command)
	var composed []humanOnlyPair
	for _, p := range pairs {
		if p.composed {
			composed = append(composed, p)
		}
	}
	if len(composed) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "`%s` reaches targets a person runs through a ctx.needs chain; run the targets it composes that are not human-only.\n", line)
		for _, p := range composed {
			fmt.Fprintf(&b, "- %s: %s\n", p.ref(), p.reason)
		}
		b.WriteString("A run that skipped them and reported green would be a pass the push gate trusts, so the whole line is refused; the person may run it from their own terminal.")
		return decisionDeny, b.String()
	}
	var facts strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&facts, "`%s` is run by a person: %s. %s\n", humanOnlyRun(p, runs), strings.TrimRight(p.reason, "."), humanOnlyRecord(ctx, at.workspace, p))
	}
	// The worker test push-ungated uses: a row that cannot be read stays a worker.
	if me, ok := actingLiveLease(ctx, deps, actingLease); actingLease != "" && (!ok || workerRole(me, deps.caller) != "") {
		return decisionDeny, "`" + line + "` runs a target a person runs, and this session is bound to job " + actingLease +
			"; name the run in your result's unresolved risks.\nWorkers do not spend for the orchestrator.\n" + facts.String()
	}
	return decisionAsk, facts.String() + "Approving runs it once. To run it yourself: " + line
}

// humanOnlyRun renders the run that names p, for the line the reason opens with.
func humanOnlyRun(p humanOnlyPair, runs []targetRun) string {
	for _, r := range runs {
		if targetName(r.target) == p.target {
			return strings.Join(append([]string{"magus", r.verb, r.target}, r.projects...), " ")
		}
	}
	return "magus run " + p.target + " " + p.project
}

// humanOnlyReach is every human_only target runs reach in the workspace at root, each
// once. A workspace that cannot be read reaches none.
func humanOnlyReach(ctx context.Context, deps Dependencies, root string, runs []targetRun) []humanOnlyPair {
	ws, err := deps.inspect(ctx, root)
	if err != nil || ws == nil {
		return nil
	}
	var out []humanOnlyPair
	index := map[string]int{}
	for _, r := range runs {
		name := targetName(r.target)
		for _, p := range runProjects(ws, r) {
			_ = types.WalkChain(p, name, ws.Get, func(v types.ChainVisit) error {
				pol := v.Project.TargetPolicies[v.Target]
				if !pol.HumanOnly {
					return nil
				}
				if i, ok := index[v.Key()]; ok {
					out[i].composed = out[i].composed || v.Depth > 0
					return types.ErrSkipChain
				}
				index[v.Key()] = len(out)
				out = append(out, humanOnlyPair{
					project: v.Project.Path, target: v.Target, reason: pol.HumanOnlyReason,
					skipCache: pol.SkipCache, composed: v.Depth > 0,
				})
				return types.ErrSkipChain
			})
		}
	}
	return out
}

// runProjects is the projects r runs in. A run naming none, `affected`, and a reference
// that is not a project path all count as every project: the engine settles those from
// the working directory, the diff or a glob, and missing a project here misses a run.
func runProjects(ws types.WorkspaceReader, r targetRun) []*types.Project {
	if r.verb != "run" || len(r.projects) == 0 {
		return ws.All()
	}
	var out []*types.Project
	for _, ref := range r.projects {
		p := ws.Get(path.Clean(ref))
		if p == nil {
			return ws.All()
		}
		out = append(out, p)
	}
	return out
}

// humanOnlyRecord states what the run history says of p and whether a run of it replays,
// as one sentence.
func humanOnlyRecord(ctx context.Context, root string, p humanOnlyPair) string {
	cached := "cached, so an unchanged tree replays the last run"
	if p.skipCache {
		cached = "never cached (skip_cache)"
	}
	cfg, err := config.LoadWithRoot("", root)
	if err != nil || configgen.ApplyEnv(&cfg, os.Getenv) != nil || cfg.HistoryPath == "" {
		return "Run history unread; " + cached + "."
	}
	var h forecast.History
	if h.Load(ctx, cfg.HistoryPath) != nil {
		return "Run history unread; " + cached + "."
	}
	st, ok := h.FoldTargetHistories(p.project, p.target)
	if !ok || st.Samples == 0 || st.P75Ms <= 0 {
		return "No run recorded; " + cached + "."
	}
	p75 := (time.Duration(st.P75Ms) * time.Millisecond).Round(time.Second)
	return fmt.Sprintf("Recorded: p75 %s over %d runs; %s.", p75, st.Samples, cached)
}
