package guard

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
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
	// unread is a target a run the guard could not read may reach: an MCP client script
	// whose magus\run argv it computes, or that does not parse.
	unread bool
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
	var composed, unread []humanOnlyPair
	for _, p := range pairs {
		switch {
		case p.unread:
			unread = append(unread, p)
		case p.composed:
			composed = append(composed, p)
		}
	}
	if len(unread) > 0 {
		var b strings.Builder
		b.WriteString("a magus client script whose runs the guard cannot read may run a target a person runs; pass magus\\run its argv as literals.\n")
		for _, p := range unread {
			fmt.Fprintf(&b, "- %s: %s\n", p.ref(), p.reason)
		}
		b.WriteString("An argv the script computes, or a script that does not parse, could name any of these, so the call is refused rather than asked about.")
		return decisionDeny, b.String()
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
		fmt.Fprintf(&facts, "`%s` is run by a person: %s. %s\n", humanOnlyRun(p, runs), strings.TrimRight(p.reason, "."), humanOnlyHistory(ctx, at.workspace, p))
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

// phaseHumanOnlyRecord is the trail's name for the read of the knowledge store's record
// of what the workspace declares human_only.
const phaseHumanOnlyRecord = "human_only_record"

// humanOnlyReach is every human_only target runs reach in the workspace at root, each
// once. It answers from the knowledge store's record while that record is provably
// current, and opens the workspace otherwise. A workspace that cannot be read reaches
// none.
func humanOnlyReach(ctx context.Context, deps Dependencies, root string, runs []targetRun) []humanOnlyPair {
	if ix, ok := deps.humanOnlyRecord(ctx, root); ok {
		return reachHumanOnly(recordedHumanOnly{ix}, runs)
	}
	ws, err := deps.inspect(ctx, root)
	if err != nil || ws == nil {
		return nil
	}
	return reachHumanOnly(inspectedHumanOnly{ws}, runs)
}

// humanOnlyDeclarations is where humanOnlyReach learns what a workspace declares
// human_only: the workspace itself, opened, or the record the knowledge store keeps of it.
// Both answer every question alike, so the verdict cannot depend on which one answered.
type humanOnlyDeclarations interface {
	// declared is every human_only target in the workspace, in project path order, then
	// target name order.
	declared() []humanOnlyPair
	// projects is the paths of the projects r runs in, as runProjects settles them.
	projects(r targetRun) []string
	// reached is the human_only targets a run of target in project reaches, sorted by
	// project, then target: the target itself when it is one, else every one its chain
	// composes before another human_only target.
	reached(project, target string) []humanOnlyPair
}

// reachHumanOnly is humanOnlyReach over decls.
func reachHumanOnly(decls humanOnlyDeclarations, runs []targetRun) []humanOnlyPair {
	var out []humanOnlyPair
	index := map[string]int{}
	unreadSeen := false
	for _, r := range runs {
		if r.target == unreadValue || slices.Contains(r.projects, unreadValue) {
			if unreadSeen {
				continue
			}
			unreadSeen = true
			for _, p := range decls.declared() {
				p.skipCache, p.unread = false, true
				out = append(out, p)
			}
			continue
		}
		name := targetName(r.target)
		for _, project := range decls.projects(r) {
			for _, p := range decls.reached(project, name) {
				if i, ok := index[p.ref()]; ok {
					out[i].composed = out[i].composed || p.composed
					continue
				}
				index[p.ref()] = len(out)
				out = append(out, p)
			}
		}
	}
	return out
}

func compareHumanOnlyPairs(a, b humanOnlyPair) int {
	return types.TargetRef{Project: a.project, Target: a.target}.Compare(types.TargetRef{Project: b.project, Target: b.target})
}

// inspectedHumanOnly answers from the opened workspace.
type inspectedHumanOnly struct{ ws types.WorkspaceReader }

func (d inspectedHumanOnly) declared() []humanOnlyPair {
	var out []humanOnlyPair
	for _, p := range d.ws.All() {
		for _, name := range slices.Sorted(maps.Keys(p.TargetPolicies)) {
			if pol := p.TargetPolicies[name]; pol.HumanOnly {
				out = append(out, humanOnlyPair{project: p.Path, target: name, reason: pol.HumanOnlyReason, skipCache: pol.SkipCache})
			}
		}
	}
	slices.SortFunc(out, compareHumanOnlyPairs)
	return out
}

func (d inspectedHumanOnly) projects(r targetRun) []string {
	var out []string
	for _, p := range runProjects(d.ws, r) {
		out = append(out, p.Path)
	}
	return out
}

func (d inspectedHumanOnly) reached(project, target string) []humanOnlyPair {
	var out []humanOnlyPair
	_ = types.WalkChain(d.ws.Get(project), target, d.ws.Get, func(v types.ChainVisit) error {
		pol := v.Project.TargetPolicies[v.Target]
		if !pol.HumanOnly {
			return nil
		}
		out = append(out, humanOnlyPair{
			project: v.Project.Path, target: v.Target, reason: pol.HumanOnlyReason,
			skipCache: pol.SkipCache, composed: v.Depth > 0,
		})
		return types.ErrSkipChain
	})
	slices.SortFunc(out, compareHumanOnlyPairs)
	return out
}

// recordedHumanOnly answers from the knowledge store's record, whose ReachedBy lists hold
// the walk inspectedHumanOnly.reached makes.
type recordedHumanOnly struct{ ix types.HumanOnlyIndex }

func (d recordedHumanOnly) declared() []humanOnlyPair {
	out := make([]humanOnlyPair, 0, len(d.ix.Targets))
	for _, t := range d.ix.Targets {
		out = append(out, recordedPair(t, false))
	}
	slices.SortFunc(out, compareHumanOnlyPairs)
	return out
}

func (d recordedHumanOnly) projects(r targetRun) []string {
	if r.verb != "run" || len(r.projects) == 0 {
		return d.ix.Projects
	}
	var out []string
	for _, ref := range r.projects {
		p := path.Clean(ref)
		if !slices.Contains(d.ix.Projects, p) {
			return d.ix.Projects
		}
		out = append(out, p)
	}
	return out
}

func (d recordedHumanOnly) reached(project, target string) []humanOnlyPair {
	run := types.TargetRef{Project: project, Target: target}
	var out []humanOnlyPair
	for _, t := range d.ix.Targets {
		if t.TargetRef == run {
			return []humanOnlyPair{recordedPair(t, false)}
		}
		if slices.Contains(t.ReachedBy, run) {
			out = append(out, recordedPair(t, true))
		}
	}
	slices.SortFunc(out, compareHumanOnlyPairs)
	return out
}

func recordedPair(t types.HumanOnlyTarget, composed bool) humanOnlyPair {
	return humanOnlyPair{project: t.Project, target: t.Target, reason: t.Reason, skipCache: t.SkipCache, composed: composed}
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

// humanOnlyHistory states what the run history says of p and whether a run of it replays,
// as one sentence.
func humanOnlyHistory(ctx context.Context, root string, p humanOnlyPair) string {
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
