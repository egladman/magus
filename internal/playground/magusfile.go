package playground

import (
	"context"
	"slices"

	buzz "github.com/egladman/gopherbuzz"
)

// Graph is the evaluated shape of a magusfile: its registered projects, the
// targets discovered from exported functions, and the depends_on edges between
// them. Output is anything the magusfile printed (including magus.info logs).
type Graph struct {
	OK       bool      `json:"ok"`
	Projects []Project `json:"projects"`
	Targets  []Target  `json:"targets"`
	Edges    []Edge    `json:"edges"`
	Output   string    `json:"output"`
	Diag     *Diag     `json:"diag"`
}

// DryRunResult is the ordered plan for one target: the dependency closure in run
// order, then the host operations each target's body would perform.
type DryRunResult struct {
	OK     bool     `json:"ok"`
	Order  []string `json:"order"`
	Trace  []Op     `json:"trace"`
	Output string   `json:"output"`
	Diag   *Diag    `json:"diag"`
}

// targetInfo pairs a discovered target with its callable export value.
type targetInfo struct {
	key  string
	name string
	val  buzz.Value
}

// evalAndProbe runs the magusfile top level (registering projects), discovers
// targets from exported functions, then probes each target body once under the
// recording host to capture its depends_on edges and host ops. Host effects are
// inert, so probing a target never cascades into running its dependencies.
func evalAndProbe(ctx context.Context, src string) (*Recorder, []targetInfo, *Diag) {
	rec := newRecorder()
	sess := buzz.NewSession(ctx)
	installHost(sess, rec)

	if err := sess.Exec(ctx, src); err != nil {
		return rec, nil, toDiag(err)
	}

	targets := discoverTargets(sess)
	for _, t := range targets {
		rec.cur = t.key
		// A failing body still yields the ops recorded up to the failure; the
		// graph stays useful, so probe errors are intentionally swallowed.
		_, _ = sess.CallValue(ctx, t.val, nil)
	}
	rec.cur = ""
	return rec, targets, nil
}

// discoverTargets reads the session's exported functions as targets, keyed by
// their canonical kebab-case name, sorted for determinism.
func discoverTargets(sess *buzz.Session) []targetInfo {
	exports := sess.Exports()
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	slices.Sort(names)

	var out []targetInfo
	seen := map[string]bool{}
	for _, name := range names {
		v := exports[name]
		if !v.IsFun() {
			continue
		}
		key := normalizeTarget(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, targetInfo{key: key, name: name, val: v})
	}
	return out
}

// LoadMagusfile evaluates src to its project/target/edge graph.
func LoadMagusfile(ctx context.Context, src string) Graph {
	rec, targets, diag := evalAndProbe(ctx, src)
	if diag != nil {
		return Graph{Output: rec.out.String(), Diag: diag}
	}
	ts := make([]Target, len(targets))
	for i, t := range targets {
		ts[i] = Target{Key: t.key, Name: t.name}
	}
	return Graph{
		OK:       true,
		Projects: rec.projects,
		Targets:  ts,
		Edges:    rec.edges,
		Output:   rec.out.String(),
	}
}

// DryRun evaluates src, then returns the ordered execution plan for targetKey:
// its dependency closure in run order, followed by the concatenated host-op
// trace of each target in that order.
func DryRun(ctx context.Context, src, targetKey string) DryRunResult {
	rec, targets, diag := evalAndProbe(ctx, src)
	if diag != nil {
		return DryRunResult{Output: rec.out.String(), Diag: diag}
	}
	known := map[string]bool{}
	for _, t := range targets {
		known[t.key] = true
	}
	if !known[targetKey] {
		return DryRunResult{Output: rec.out.String(), Diag: &Diag{Msg: "unknown target: " + targetKey}}
	}

	order := rec.topoOrder(targetKey)
	var trace []Op
	for _, t := range order {
		trace = append(trace, rec.opsByTarget[t]...)
	}
	return DryRunResult{OK: true, Order: order, Trace: trace, Output: rec.out.String()}
}
