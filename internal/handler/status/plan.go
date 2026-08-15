package status

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// planSource is the narrow consumer contract the plan handler needs from the console
// service: the statically extracted target graph (the plan's STRUCTURE) and the live status
// report (its STATE). Both are satisfied by *console.Service; the handler package holds no
// concrete service, matching insightSource and diffSource above.
//
// TargetGraph is the same call `magus describe graph -o json` makes and the same bytes
// gen/target-graph.json holds, read from the loaded workspace rather than from that file -
// a generated snapshot is only as fresh as the last `generate`, and this route reports live.
type planSource interface {
	TargetGraph(ctx context.Context) (types.TargetGraphOutput, error)
	StatusReport(ctx context.Context) types.StatusReport
}

// planOutputs is the stored-run half of the state overlay: the same descriptor list
// GET /api/v1/outputs serves, so a node's pass/fail and its ref come from the store the run
// browser already reads and a deep-link resolves. Satisfied by *cache.OutputStore.
type planOutputs interface {
	ListDescriptors() []cache.OutputDescriptor
}

// The four states a plan node can be in. There is deliberately no QUEUED state: the pool
// reports queueing as a daemon-wide COUNT (cache.Limiter.Snapshot's Queued is "slots
// currently blocked in Acquire"), never as the identity of the work waiting, so only the
// invoking process knows which target is next. A fifth state nothing can populate honestly
// is worse than four that can.
const (
	planStateIdle    = "idle"
	planStateRunning = "running"
	planStatePass    = "pass"
	planStateFail    = "fail"
)

// How the served anchor was chosen, reported so the console can say whose plan this is
// rather than implying the reader asked for it.
const (
	planAnchorExplicit = "explicit" // ?target= named it
	planAnchorRunning  = "running"  // the target of the in-flight run
	planAnchorRecent   = "recent"   // the target of the most recent captured output
	planAnchorDefault  = "default"  // nothing running, nothing has run
)

// planDefaultTarget is the anchor of last resort, used only when nothing is running and
// nothing has run.
const planDefaultTarget = "ci"

// planNode is one (project, target) pair the plan would run.
//
// Ref is the most recent captured output for this node and is reported INDEPENDENTLY of
// State: a running node carries the PREVIOUS run's ref until its own result lands, because
// dropping it would hide the only log there is. A consumer that wants "this run's log" must
// wait for the state to leave running rather than read Ref as live.
type planNode struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Target  string `json:"target"`
	State   string `json:"state"`
	Ref     string `json:"ref"`
}

// planEdge runs in RUN ORDER: From is the dependency (upstream, drawn left), To is the
// dependent. Same orientation as the plan ledger's parent edges, so the console renders an
// agent-declared plan and a derived one with one layout pass.
type planEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type planResponse struct {
	Target string     `json:"target"`
	Anchor string     `json:"anchor"`
	Nodes  []planNode `json:"nodes"`
	Edges  []planEdge `json:"edges"`
}

// PlanHandler serves GET /api/v1/plan: the target DAG magus derives for plain work, with
// live per-node state. Every run has a plan - the engine computes one to dispatch at all -
// and this is that plan, not a document anybody authored.
//
// With no ?target the server picks the anchor itself, because the question a reader has in
// front of a running build is "what is happening", not "what would happen if I ran ci": the
// in-flight run's target, else the most recent captured output's, else "ci". ?target=<name>
// overrides that; an unknown one is a 400 rather than an empty graph, since an empty plan
// and a misspelled target render identically and only one of them is the reader's mistake.
//
// It REPORTS and never gates: a red node is a fact on the wire, not a reason to refuse.
type PlanHandler struct {
	handler.Base
	src     planSource
	outputs planOutputs
	root    string
}

// NewPlanHandler returns the GET /api/v1/plan handler. root is the workspace this daemon
// fronts, used to drop pool entries belonging to another workspace; empty disables that
// filter (every pool entry then counts, which is the honest degradation for a caller that
// cannot say which tree it is serving).
func NewPlanHandler(src planSource, outputs planOutputs, root string, log *slog.Logger) *PlanHandler {
	h := &PlanHandler{src: src, outputs: outputs, root: root}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *PlanHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	graph, err := h.src.TargetGraph(r.Context())
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			http.Error(w, "workspace unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "plan error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	index := indexPlanTargets(graph)

	report := h.src.StatusReport(r.Context())
	descs := h.outputs.ListDescriptors()

	target, anchor, ok := h.resolveAnchor(r.URL.Query().Get("target"), index, report, descs)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown target %q; run `magus describe targets` to list them", target), http.StatusBadRequest)
		return
	}

	nodes, edges := planClosure(index, graph.Projects, target)
	overlayPlanState(nodes, report, descs, h.root)
	writeJSON(w, planResponse{Target: target, Anchor: anchor, Nodes: nodes, Edges: edges})
}

// resolveAnchor picks the target this plan is about and says how it was picked. An explicit
// ?target must resolve or the request is rejected (ok false, with target carrying the
// caller's spelling back for the message); a DERIVED candidate that no project defines is
// silently skipped instead, so `magus x <ref>` running in the pool falls through to the
// most recent output rather than serving an empty graph while a build is visibly in flight.
func (h *PlanHandler) resolveAnchor(raw string, index planTargets, report types.StatusReport, descs []cache.OutputDescriptor) (target, anchor string, ok bool) {
	if raw != "" {
		t, err := types.ParseTarget(raw)
		if err != nil || !index.defines(t.Name) {
			return raw, "", false
		}
		return t.Name, planAnchorExplicit, true
	}
	if t := runningAnchor(report.Pool, h.root); index.defines(t) {
		return t, planAnchorRunning, true
	}
	if len(descs) > 0 {
		if t := bareTarget(descs[0].Target); index.defines(t) {
			return t, planAnchorRecent, true
		}
	}
	return planDefaultTarget, planAnchorDefault, true
}

// runningAnchor is the target of the in-flight run, or "" when nothing is running in this
// workspace. Concurrent runs resolve to the most recently STARTED one: that is the one the
// reader just triggered and is waiting to watch.
//
// A pool entry identifies its work by the invocation's ARGV, so this reads argv the way
// `magus status` does (parseRunning, cmd/magus/status.go) - narrower, because a plan needs
// only the target: it covers every project defining it, and the project argument may be a
// fuzzy name that only the run itself resolves to a path.
func runningAnchor(pool *types.StatusOutput, root string) string {
	if pool == nil {
		return ""
	}
	var best types.StatusRunningTarget
	found := false
	for _, c := range pool.RunningTargets {
		if root != "" && c.Workspace != "" && c.Workspace != root {
			continue
		}
		if !found || c.StartedAt.After(best.StartedAt) {
			best, found = c, true
		}
	}
	if !found {
		return ""
	}
	return targetFromArgs(best.Args)
}

// targetFromArgs reads the target out of an invocation's argv: leading flags are skipped,
// `run` and `affected` take their operand, and any other subcommand IS the target (the
// `magus build` shorthand). Charms are stripped, so `build:rw` anchors the `build` plan.
func targetFromArgs(args []string) string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i >= len(args) {
		return ""
	}
	sub := args[i]
	if sub == "run" || sub == "affected" {
		i++
		if i >= len(args) {
			return ""
		}
		sub = args[i]
	}
	t, err := types.ParseTarget(sub)
	if err != nil {
		return ""
	}
	return t.Name
}

// planKey is a node's identity: the project that defines the target and the target's
// normalized name. It is what every state source must be joined on.
type planKey struct{ project, target string }

func (k planKey) id() string { return k.project + ":" + k.target }

// planTargets indexes the extracted graph by project path then target name, which is the
// lookup the closure walk and the anchor check both need.
type planTargets map[string]map[string]types.TargetGraphNode

// defines reports whether any project declares this target. The empty name never does, so
// callers can pass an unresolved candidate straight in.
func (t planTargets) defines(target string) bool {
	if target == "" {
		return false
	}
	for _, byName := range t {
		if _, ok := byName[target]; ok {
			return true
		}
	}
	return false
}

func indexPlanTargets(graph types.TargetGraphOutput) planTargets {
	index := make(planTargets, len(graph.Projects))
	for _, p := range graph.Projects {
		if len(p.Nodes) == 0 {
			continue // a project on an engine with no extractor contributes no plan
		}
		byName := make(map[string]types.TargetGraphNode, len(p.Nodes))
		for _, n := range p.Nodes {
			byName[n.Name] = n
		}
		index[p.Path] = byName
	}
	return index
}

// planClosure derives the nodes and edges of an anchor's plan: the anchor in every project
// that defines it, plus everything those anchors reach through same-project dependencies and
// cross-project target imports. A dependency no project declares is dropped rather than
// emitted as a dangling endpoint - the engine would refuse the run, and a node with no
// definition has nothing to report a state for.
//
// The project-level pass at the end draws the ordering the engine imposes on top of the
// target edges (run.go sets every step's DependsOn to its project's), projected onto the
// anchors: strictly it orders EVERY step in a project after every step in its dependencies,
// and drawing that product would be a wall of lines rather than a plan.
//
// Both slices are non-nil so an empty plan serializes as [] rather than null.
func planClosure(index planTargets, projects []types.TargetGraphProject, anchor string) ([]planNode, []planEdge) {
	seen := map[planKey]bool{}
	var queue []planKey
	// visit reports whether k is a real node, enqueuing it the first time. Callers use the
	// answer to decide whether an edge to it is drawable, so the two can never disagree.
	visit := func(k planKey) bool {
		if _, ok := index[k.project][k.target]; !ok {
			return false
		}
		if !seen[k] {
			seen[k] = true
			queue = append(queue, k)
		}
		return true
	}
	for _, p := range projects {
		visit(planKey{p.Path, anchor})
	}

	nodes := make([]planNode, 0, len(queue))
	edges := make([]planEdge, 0, len(queue))
	drawn := map[planEdge]bool{}
	draw := func(from, to planKey) {
		e := planEdge{From: from.id(), To: to.id()}
		if !drawn[e] {
			drawn[e] = true
			edges = append(edges, e)
		}
	}
	for i := 0; i < len(queue); i++ {
		k := queue[i]
		nodes = append(nodes, planNode{ID: k.id(), Project: k.project, Target: k.target, State: planStateIdle})
		n := index[k.project][k.target]
		for _, dep := range n.Dependencies {
			if d := (planKey{k.project, dep}); visit(d) {
				draw(d, k)
			}
		}
		for _, cd := range n.CrossDependencies {
			if d := (planKey{cd.Project, cd.Target}); visit(d) {
				draw(d, k)
			}
		}
	}
	for _, p := range projects {
		to := planKey{p.Path, anchor}
		if !seen[to] {
			continue
		}
		for _, dep := range p.DependsOn {
			if from := (planKey{dep, anchor}); seen[from] {
				draw(from, to)
			}
		}
	}
	return nodes, edges
}

// overlayPlanState stamps live state onto the derived nodes, in place.
//
// Running WINS over a stored outcome, always: the outcome describes a run that has already
// ended, and showing a green node for work restarted ten seconds ago is the one reading that
// makes the view useless.
func overlayPlanState(nodes []planNode, report types.StatusReport, descs []cache.OutputDescriptor, root string) {
	last := lastOutcomes(descs)
	live := runningNodes(report.Runs)
	invoked := runningInvocations(report.Pool, root)
	for i := range nodes {
		k := planKey{nodes[i].Project, nodes[i].Target}
		if d, ok := last[k]; ok {
			nodes[i].Ref = d.Ref
			nodes[i].State = planStatePass
			if d.Failed {
				nodes[i].State = planStateFail
			}
		}
		if live[k] || invoked[k.target] {
			nodes[i].State = planStateRunning
		}
	}
}

// lastOutcomes reduces the descriptor list to the most recent stored run per node.
// ListDescriptors is newest-first, so the first descriptor seen for a key wins.
//
// A descriptor's target is the REPRO target ("build:rw" under a default charm), so the
// charm suffix is cut off before joining: with default_charms set, an exact-match join finds
// nothing at all and every node reports idle forever.
func lastOutcomes(descs []cache.OutputDescriptor) map[planKey]cache.OutputDescriptor {
	out := make(map[planKey]cache.OutputDescriptor, len(descs))
	for _, d := range descs {
		k := planKey{d.Project, bareTarget(d.Target)}
		if _, ok := out[k]; !ok {
			out[k] = d
		}
	}
	return out
}

// runningNodes is the PRECISE half of the running overlay: the daemon's run registry folds
// each adopted run's journal events into per-target state keyed by project and target, which
// is exactly a plan node's identity.
//
// It carries no workspace, so a run in a second workspace whose project and target both
// match this one's would light a node here. That is a daemon serving one console for two
// trees at once; the pool half below can be filtered and this one cannot.
//
// Queued targets are reported as idle: the wire contract has no queued state (see above),
// and calling work that has not started "running" would be a straight lie.
func runningNodes(runs []types.StatusRun) map[planKey]bool {
	out := map[planKey]bool{}
	for _, run := range runs {
		for _, t := range run.Targets {
			if t.State == types.TargetRunRunning {
				out[planKey{t.Project, bareTarget(t.Target)}] = true
			}
		}
	}
	return out
}

// runningInvocations is the COARSE half: a pool entry names the argv a run was invoked with,
// never the node executing inside it, so it can only answer "a run of target X is in
// flight". Every node with that target name is therefore marked running - which is true of
// an anchor dispatched across the workspace, and deliberately loose about which project's
// step holds a slot at this instant. It exists so a run the daemon did not adopt (no journal
// events, so nothing in runningNodes) still shows as live rather than as idle.
func runningInvocations(pool *types.StatusOutput, root string) map[string]bool {
	out := map[string]bool{}
	if pool == nil {
		return out
	}
	for _, c := range pool.RunningTargets {
		if root != "" && c.Workspace != "" && c.Workspace != root {
			continue
		}
		if t := targetFromArgs(c.Args); t != "" {
			out[t] = true
		}
	}
	return out
}

// bareTarget drops a charm suffix ("build:rw" -> "build"). A cut rather than
// types.ParseTarget because both sides of every join here are names the engine already
// normalized, and a parse error would have to be swallowed into an empty name that silently
// matches nothing.
func bareTarget(target string) string {
	name, _, _ := strings.Cut(target, ":")
	return name
}
