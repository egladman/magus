package playground

import (
	"context"
	"slices"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
)

// Graph is the evaluated shape of a magusfile OR a spell buffer: its registered
// projects, the targets/ops discovered, and the depends_on edges between them.
// Output is anything the buffer printed (including magus.info logs).
//
// Spell reports whether the buffer was a SPELL (its ops discovered by calling
// mgs_listTargets) rather than a magusfile (targets are its exported funs). When
// true, Targets holds one entry per spell op; each op's kind and declared command
// are surfaced via DryRun, not the graph.
type Graph struct {
	OK       bool      `json:"ok"`
	Spell    bool      `json:"spell"`
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
	val  vm.Value
}

// evalAndProbe runs the buffer's top level, then probes it. For a magusfile it
// registers projects, discovers targets from exported functions, and probes each
// target body once under the recording host to capture its depends_on edges and
// host ops. For a SPELL buffer (one exporting mgs_listTargets) it instead resolves
// the spell's ops — the returned []spellOp is non-nil and []targetInfo is empty.
// Host effects are inert, so probing never cascades into running dependencies.
func evalAndProbe(ctx context.Context, src string, charms []string, spells map[string][]string) (rec *Recorder, targets []targetInfo, ops []spellOp, isSpellBuf bool, diag *Diag) {
	rec = newRecorder()
	rec.charms = charms
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	installHost(ctx, sess, rec, spells)

	if err := sess.Exec(ctx, src); err != nil {
		return rec, nil, nil, false, toDiag(err)
	}

	// A SPELL buffer's targets are its ops, discovered by calling mgs_listTargets —
	// not the exported funs. Route it to the spell probe; the ward check runs there.
	if isSpell(sess) {
		return rec, nil, probeSpell(ctx, sess), true, nil
	}

	targets = discoverTargets(sess)
	// Publish the full target set before probing so a magus.needs(glob/regex) in a
	// body can expand its pattern against it.
	for _, t := range targets {
		rec.targetKeys = append(rec.targetKeys, t.key)
	}
	for _, t := range targets {
		rec.cur = t.key
		// A failing body still yields the ops recorded up to the failure; the
		// graph stays useful, so probe errors are intentionally swallowed.
		_, _ = sess.CallValue(ctx, t.val, nil)
	}
	rec.cur = ""
	return rec, targets, nil, false, nil
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

// LoadMagusfile evaluates src to its project/target/edge graph. It transparently
// handles both a magusfile and a SPELL buffer: a spell's ops become the Targets and
// Graph.Spell is set. Charms are off (empty) for a structural load — the graph is
// charm-independent. A ward on a spell op (e.g. MGS5002) is NOT a load diagnostic:
// the op still lists here, and the ward surfaces via DryRun.
func LoadMagusfile(ctx context.Context, src string) Graph {
	rec, targets, ops, isSpellBuf, diag := evalAndProbe(ctx, src, nil, builtinSpellOps)
	if diag != nil {
		return Graph{Output: rec.out.String(), Diag: diag}
	}
	if isSpellBuf {
		ts := make([]Target, len(ops))
		for i, o := range ops {
			ts[i] = Target{Key: o.name, Name: o.name}
		}
		return Graph{OK: true, Spell: true, Targets: ts, Output: rec.out.String()}
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
// trace of each target in that order. charms is the active charm set (from a
// `run t:charm` invocation), so charm-gated branches (has_charm) resolve.
func DryRun(ctx context.Context, src, targetKey string, charms []string) DryRunResult {
	rec, targets, ops, isSpellBuf, diag := evalAndProbe(ctx, src, charms, builtinSpellOps)
	if diag != nil {
		return DryRunResult{Output: rec.out.String(), Diag: diag}
	}
	if isSpellBuf {
		return dryRunSpell(ops, targetKey, rec.out.String())
	}
	// Normalize the requested name the same way registration does, so any casing or
	// separator resolves: `run goBuild`, `run go_build`, and `run go-build` all hit
	// the go-build target.
	targetKey = normalizeTarget(targetKey)
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
