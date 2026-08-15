package status

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// fakePlanSource is a planSource returning a canned target graph and status report, the two
// halves the handler joins.
type fakePlanSource struct {
	graph    types.TargetGraphOutput
	graphErr error
	report   types.StatusReport
}

func (f fakePlanSource) TargetGraph(context.Context) (types.TargetGraphOutput, error) {
	return f.graph, f.graphErr
}

func (f fakePlanSource) StatusReport(context.Context) types.StatusReport { return f.report }

// fakePlanOutputs is a planOutputs over a fixed descriptor list, newest first (the order the
// real store returns).
type fakePlanOutputs []cache.OutputDescriptor

func (f fakePlanOutputs) ListDescriptors() []cache.OutputDescriptor { return f }

// planFixture is a two-project workspace exercising all three edge kinds: a same-project
// dependency (ci -> build), a cross-project target import (app:build -> libs/api:test), and
// a project-level dependency (app depends on libs/api, so their anchors are ordered).
func planFixture() types.TargetGraphOutput {
	return types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{
			Path:      "app",
			DependsOn: []string{"libs/api"},
			Nodes: []types.TargetGraphNode{
				{Name: "ci", Dependencies: []string{"build"}},
				{Name: "build", CrossDependencies: []types.CrossTargetRef{{Project: "libs/api", Target: "test"}}},
				{Name: "docs"}, // outside the ci closure; must not appear
			},
		},
		{
			Path: "libs/api",
			Nodes: []types.TargetGraphNode{
				{Name: "ci", Dependencies: []string{"test"}},
				{Name: "test"},
			},
		},
	}}
}

func getPlan(t *testing.T, h *PlanHandler, url string) (*httptest.ResponseRecorder, planResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	var out planResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("want valid JSON: %v; body %s", err, w.Body.String())
		}
	}
	return w, out
}

func planStates(p planResponse) map[string]planNode {
	byID := make(map[string]planNode, len(p.Nodes))
	for _, n := range p.Nodes {
		byID[n.ID] = n
	}
	return byID
}

func planEdgeSet(p planResponse) map[string]bool {
	set := make(map[string]bool, len(p.Edges))
	for _, e := range p.Edges {
		set[e.From+" -> "+e.To] = true
	}
	return set
}

// --- structure ---

func TestPlanHandler_DerivesTheAnchorClosureFromTheTargetGraph(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graph: planFixture()}, fakePlanOutputs{}, "", nil)
	w, out := getPlan(t, h, "/api/v1/plan?target=ci")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("want no-store, got %q", got)
	}
	if out.Target != "ci" || out.Anchor != planAnchorExplicit {
		t.Errorf("want ci/explicit, got %q/%q", out.Target, out.Anchor)
	}
	byID := planStates(out)
	for _, want := range []string{"app:ci", "app:build", "libs/api:ci", "libs/api:test"} {
		if _, ok := byID[want]; !ok {
			t.Errorf("missing node %q; got %v", want, out.Nodes)
		}
	}
	// A target nothing in the closure reaches is not part of this plan.
	if _, ok := byID["app:docs"]; ok {
		t.Error("app:docs is outside the ci closure and must not be served")
	}
	if n := byID["app:build"]; n.Project != "app" || n.Target != "build" {
		t.Errorf("node must carry its project and target split out, got %+v", n)
	}
}

// from is the DEPENDENCY and to the DEPENDENT, in run order, for all three edge kinds.
func TestPlanHandler_EdgesRunFromDependencyToDependent(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graph: planFixture()}, fakePlanOutputs{}, "", nil)
	_, out := getPlan(t, h, "/api/v1/plan?target=ci")
	edges := planEdgeSet(out)
	// Two same-project dependencies, one cross-project target import, and the project-level
	// ordering projected onto the two anchors.
	for _, want := range []string{
		"app:build -> app:ci",
		"libs/api:test -> libs/api:ci",
		"libs/api:test -> app:build",
		"libs/api:ci -> app:ci",
	} {
		if !edges[want] {
			t.Errorf("missing edge %q; got %v", want, out.Edges)
		}
	}
	if edges["app:ci -> app:build"] {
		t.Error("edge orientation is inverted: from must be the dependency")
	}
}

// --- state join ---

func TestPlanHandler_IdleIsTheDefaultState(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graph: planFixture()}, fakePlanOutputs{}, "", nil)
	_, out := getPlan(t, h, "/api/v1/plan?target=ci")
	for _, n := range out.Nodes {
		if n.State != planStateIdle || n.Ref != "" {
			t.Errorf("node %q with no history must be idle with no ref, got %+v", n.ID, n)
		}
	}
}

// The stored outcome and its ref come from the same descriptor list /api/v1/outputs serves.
// The charm suffix on a repro target ("build:rw") must not break the join - with default
// charms configured every local run carries one, so an exact match would find nothing.
func TestPlanHandler_PassAndFailComeFromTheMostRecentOutput(t *testing.T) {
	outputs := fakePlanOutputs{ // newest first, as the store returns
		{Ref: "aa11", Project: "app", Target: "build:rw", Failed: true, TimestampMs: 300},
		{Ref: "bb22", Project: "app", Target: "build", TimestampMs: 200},
		{Ref: "cc33", Project: "libs/api", Target: "test", TimestampMs: 100},
	}
	h := NewPlanHandler(fakePlanSource{graph: planFixture()}, outputs, "", nil)
	_, out := getPlan(t, h, "/api/v1/plan?target=ci")
	byID := planStates(out)
	if n := byID["app:build"]; n.State != planStateFail || n.Ref != "aa11" {
		t.Errorf("want fail/aa11 from the newest descriptor (charm suffix cut), got %+v", n)
	}
	if n := byID["libs/api:test"]; n.State != planStatePass || n.Ref != "cc33" {
		t.Errorf("want pass/cc33, got %+v", n)
	}
	if n := byID["app:ci"]; n.State != planStateIdle {
		t.Errorf("a node with no descriptor stays idle, got %+v", n)
	}
}

// A node the daemon reports as running must not render as the green it was last time.
func TestPlanHandler_RunningWinsOverAStalePass(t *testing.T) {
	src := fakePlanSource{
		graph: planFixture(),
		report: types.StatusReport{Runs: []types.StatusRun{{
			Inv: "inv1",
			Targets: []types.StatusTargetRun{
				{Project: "app", Target: "build:rw", State: types.TargetRunRunning},
				{Project: "libs/api", Target: "test", State: types.TargetRunQueued},
			},
		}}},
	}
	outputs := fakePlanOutputs{
		{Ref: "bb22", Project: "app", Target: "build", TimestampMs: 200},
		{Ref: "cc33", Project: "libs/api", Target: "test", TimestampMs: 100},
	}
	h := NewPlanHandler(src, outputs, "", nil)
	_, out := getPlan(t, h, "/api/v1/plan?target=ci")
	byID := planStates(out)
	if n := byID["app:build"]; n.State != planStateRunning {
		t.Errorf("running must beat the previous pass, got %+v", n)
	}
	if n := byID["app:build"]; n.Ref != "bb22" {
		t.Errorf("a running node keeps the previous ref so there is a log to open, got %+v", n)
	}
	// There is no queued state on the wire; queued work is not running.
	if n := byID["libs/api:test"]; n.State != planStatePass {
		t.Errorf("a queued target must not report running, got %+v", n)
	}
}

// A run the daemon did not adopt emits no journal events, so only the pool sees it. The
// match is by target name alone, which lights every project's copy of that anchor.
func TestPlanHandler_PoolEntryMarksTheInvokedTargetRunning(t *testing.T) {
	src := fakePlanSource{
		graph: planFixture(),
		report: types.StatusReport{Pool: &types.StatusOutput{RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "test"}, Workspace: "/w"},
		}}},
	}
	h := NewPlanHandler(src, fakePlanOutputs{}, "/w", nil)
	_, out := getPlan(t, h, "/api/v1/plan?target=ci")
	if n := planStates(out)["libs/api:test"]; n.State != planStateRunning {
		t.Errorf("want running from the pool entry, got %+v", n)
	}
}

func TestPlanHandler_PoolEntryFromAnotherWorkspaceIsIgnored(t *testing.T) {
	src := fakePlanSource{
		graph: planFixture(),
		report: types.StatusReport{Pool: &types.StatusOutput{RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "test"}, Workspace: "/other"},
		}}},
	}
	h := NewPlanHandler(src, fakePlanOutputs{}, "/w", nil)
	_, out := getPlan(t, h, "/api/v1/plan?target=ci")
	if n := planStates(out)["libs/api:test"]; n.State != planStateIdle {
		t.Errorf("another workspace's run must not light this plan, got %+v", n)
	}
}

// --- anchor selection ---

func TestPlanHandler_AnchorFollowsTheInFlightRun(t *testing.T) {
	src := fakePlanSource{
		graph: planFixture(),
		report: types.StatusReport{Pool: &types.StatusOutput{RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "build:rw", "app"}, Workspace: "/w", StartedAt: time.Unix(100, 0)},
		}}},
	}
	outputs := fakePlanOutputs{{Ref: "cc33", Project: "libs/api", Target: "test", TimestampMs: 100}}
	_, out := getPlan(t, NewPlanHandler(src, outputs, "/w", nil), "/api/v1/plan")
	if out.Target != "build" || out.Anchor != planAnchorRunning {
		t.Errorf("want build/running (charms stripped, running beats recent), got %q/%q", out.Target, out.Anchor)
	}
}

func TestPlanHandler_AnchorPrefersTheMostRecentlyStartedRun(t *testing.T) {
	src := fakePlanSource{
		graph: planFixture(),
		report: types.StatusReport{Pool: &types.StatusOutput{RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "build"}, Workspace: "/w", StartedAt: time.Unix(100, 0)},
			{Args: []string{"affected", "ci"}, Workspace: "/w", StartedAt: time.Unix(200, 0)},
		}}},
	}
	_, out := getPlan(t, NewPlanHandler(src, fakePlanOutputs{}, "/w", nil), "/api/v1/plan")
	if out.Target != "ci" || out.Anchor != planAnchorRunning {
		t.Errorf("want ci/running from the newest start, got %q/%q", out.Target, out.Anchor)
	}
}

func TestPlanHandler_AnchorFallsBackToTheMostRecentOutput(t *testing.T) {
	outputs := fakePlanOutputs{
		{Ref: "aa11", Project: "libs/api", Target: "test:rw", TimestampMs: 300},
		{Ref: "bb22", Project: "app", Target: "ci", TimestampMs: 200},
	}
	_, out := getPlan(t, NewPlanHandler(fakePlanSource{graph: planFixture()}, outputs, "", nil), "/api/v1/plan")
	if out.Target != "test" || out.Anchor != planAnchorRecent {
		t.Errorf("want test/recent, got %q/%q", out.Target, out.Anchor)
	}
}

func TestPlanHandler_AnchorDefaultsToCI(t *testing.T) {
	_, out := getPlan(t, NewPlanHandler(fakePlanSource{graph: planFixture()}, fakePlanOutputs{}, "", nil), "/api/v1/plan")
	if out.Target != planDefaultTarget || out.Anchor != planAnchorDefault {
		t.Errorf("want ci/default, got %q/%q", out.Target, out.Anchor)
	}
}

// `magus x <ref>` is in the pool but is not a target, so it must not serve an empty plan
// while a build is visibly in flight - the derived candidate falls through instead.
func TestPlanHandler_UndefinedDerivedAnchorFallsThrough(t *testing.T) {
	src := fakePlanSource{
		graph: planFixture(),
		report: types.StatusReport{Pool: &types.StatusOutput{RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"x", "aa11"}, Workspace: "/w"},
		}}},
	}
	outputs := fakePlanOutputs{{Ref: "bb22", Project: "app", Target: "build", TimestampMs: 200}}
	_, out := getPlan(t, NewPlanHandler(src, outputs, "/w", nil), "/api/v1/plan")
	if out.Target != "build" || out.Anchor != planAnchorRecent {
		t.Errorf("want build/recent, got %q/%q", out.Target, out.Anchor)
	}
}

func TestPlanHandler_ExplicitTargetOverridesTheRunningOne(t *testing.T) {
	src := fakePlanSource{
		graph: planFixture(),
		report: types.StatusReport{Pool: &types.StatusOutput{RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "build"}, Workspace: "/w"},
		}}},
	}
	_, out := getPlan(t, NewPlanHandler(src, fakePlanOutputs{}, "/w", nil), "/api/v1/plan?target=ci")
	if out.Target != "ci" || out.Anchor != planAnchorExplicit {
		t.Errorf("want ci/explicit, got %q/%q", out.Target, out.Anchor)
	}
}

// --- errors and empties ---

func TestPlanHandler_UnknownExplicitTargetIs400(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graph: planFixture()}, fakePlanOutputs{}, "", nil)
	// A name no project declares, and one ParseTarget itself rejects (empty charm).
	for _, target := range []string{"nope", "ci:"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/plan?target="+target, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("target %q: want 400, got %d", target, w.Code)
		}
		if !strings.Contains(w.Body.String(), "magus describe targets") {
			t.Errorf("target %q: the error must name how to list targets, got %q", target, w.Body.String())
		}
	}
}

// An empty plan is a shape the console renders, so it must serialize as [] - a null would
// make every reader branch before it could iterate.
func TestPlanHandler_EmptyPlanIsNeverNull(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graph: types.TargetGraphOutput{}}, fakePlanOutputs{}, "", nil)
	w, out := getPlan(t, h, "/api/v1/plan")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for an empty workspace, got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `"nodes":[]`) || !strings.Contains(body, `"edges":[]`) {
		t.Errorf("want empty arrays on the wire, got %s", body)
	}
	if out.Target != planDefaultTarget {
		t.Errorf("want the default anchor, got %q", out.Target)
	}
}

func TestPlanHandler_NoWorkspaceReturns503(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graphErr: console.ErrNoWorkspace}, fakePlanOutputs{}, "", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/plan", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", w.Code)
	}
}

func TestPlanHandler_ErrorReturns500(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graphErr: errors.New("extract boom")}, fakePlanOutputs{}, "", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/plan", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

func TestPlanHandler_MethodNotAllowed(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graph: planFixture()}, fakePlanOutputs{}, "", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/plan", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestPlanHandler_OptionsNoContent(t *testing.T) {
	h := NewPlanHandler(fakePlanSource{graph: planFixture()}, fakePlanOutputs{}, "", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/api/v1/plan", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 for preflight, got %d", w.Code)
	}
}
