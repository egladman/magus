package knowledge

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const runPkg = "/internal/proc/run"

// couplingCallers are the packages that call the run.Exec shape, in path order.
var couplingCallers = []string{"/cmd/termcast", "/internal/a", "/internal/b", "/internal/c", "/internal/queue", "/std"}

// couplingFile is the one file a caller package calls from.
func couplingFile(pkg string) string {
	return map[string]string{
		"/cmd/termcast":   "cmd/termcast/main.go",
		"/internal/a":     "internal/a/a.go",
		"/internal/b":     "internal/b/b.go",
		"/internal/c":     "internal/c/c.go",
		"/internal/queue": "internal/queue/q.go",
		"/std":            "std/os.go",
	}[pkg]
}

func couplingFiles() []string {
	out := make([]string, len(couplingCallers))
	for i, pkg := range couplingCallers {
		out[i] = couplingFile(pkg)
	}
	return out
}

// couplingFn is a Go function in pkg at file lines start through end, with a signature, the
// identifiers its body branches on, and the files that call it.
func couplingFn(pkg, name, file string, start, end int, sig string, branches []string, callers ...string) types.KnowledgeSymbol {
	s := types.KnowledgeSymbol{
		Key: conformNS(pkg) + name + "().", Label: name, Language: "go", Namespace: conformNS(pkg), Signature: sig,
		Source: fmt.Sprintf("%s:%d", file, start), DefEndLine: end, Defs: []string{file}, BranchIdents: branches,
	}
	for _, c := range callers {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: c, Count: 1})
	}
	return s
}

// runField is a field of run.Options that the given files reference.
func runField(name string, refs ...string) types.KnowledgeSymbol {
	s := types.KnowledgeSymbol{
		Key: conformNS(runPkg) + "Options#" + name + ".", Label: name, Language: "go", Namespace: conformNS(runPkg),
		Source: "internal/proc/run/exec.go:75", Defs: []string{"internal/proc/run/exec.go"},
	}
	for _, r := range refs {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: r, Count: 1})
	}
	return s
}

// couplingPackages declares each caller package and run, with imports that put the callers
// three hops apart once run is removed: termcast and std import a, a, queue and c import b.
func couplingPackages() []types.KnowledgeSymbol {
	return []types.KnowledgeSymbol{
		conformPkg(runPkg, []string{"internal/proc/run/exec.go"}, couplingFiles()...),
		conformPkg("/internal/a", []string{couplingFile("/internal/a")}, couplingFile("/cmd/termcast"), couplingFile("/std")),
		conformPkg("/internal/b", []string{couplingFile("/internal/b")},
			couplingFile("/internal/a"), couplingFile("/internal/queue"), couplingFile("/internal/c")),
		conformPkg("/internal/c", []string{couplingFile("/internal/c")}),
		conformPkg("/internal/queue", []string{couplingFile("/internal/queue")}),
		conformPkg("/cmd/termcast", []string{couplingFile("/cmd/termcast")}),
		conformPkg("/std", []string{couplingFile("/std")}),
	}
}

// execShape is run.Exec: 157 lines that six packages call through an Options struct whose
// TTYCols and TTYRows only cmd/termcast sets and whose CancelGrace only internal/queue sets.
func execShape() []types.KnowledgeSymbol {
	files := couplingFiles()
	return []types.KnowledgeSymbol{
		couplingFn(runPkg, "Exec", "internal/proc/run/exec.go", 135, 291, "func Exec(ctx context.Context, opts Options) error", nil, files...),
		{
			Key: conformNS(runPkg) + "Options#", Label: "Options", Language: "go", Namespace: conformNS(runPkg),
			Source: "internal/proc/run/exec.go:70", Defs: []string{"internal/proc/run/exec.go"},
		},
		runField("TTYCols", couplingFile("/cmd/termcast")),
		runField("TTYRows", couplingFile("/cmd/termcast")),
		runField("CancelGrace", couplingFile("/internal/queue")),
		runField("Dir", files...),
	}
}

func couplingGraph(syms ...types.KnowledgeSymbol) *Graph {
	return mergeAll(assembleSymbolShards(map[string][]types.KnowledgeSymbol{".": append(couplingPackages(), syms...)}, nil))
}

// couplingOptions are this tree's live relative values as anchors, so the run.Exec shape
// crosses them the way it crosses a committed ratchet.
func couplingOptions() types.ConformOptions {
	return types.ConformOptions{
		Limits: map[types.ConformKey]types.ConformLimit{
			couplingKey(types.ConformSharedBody):   {Percentile: 90, Anchor: 30},
			couplingKey(types.ConformSharedReach):  {Percentile: 95, Anchor: 4},
			couplingKey(types.ConformCallerSpread): {Percentile: 95, Anchor: 2},
			couplingKey(types.ConformModeKnobs):    {Percentile: 0, Anchor: 0},
		},
		MinSample: 1,
		Step:      0.05,
		Gates:     testGates(),
	}
}

func TestCallerSpread(t *testing.T) {
	ig := types.ImportGraph{Packages: map[string][]string{
		"cmd/termcast": {"internal/a", "run"}, "std": {"internal/a", "run"}, "internal/a": {"internal/b", "run"},
		"internal/queue": {"internal/b", "run"}, "internal/c": {"internal/b"}, "lone": {"run"},
	}}
	for _, tc := range []struct {
		name    string
		callers []string
		want    int
	}{
		{name: "one caller", callers: []string{"std"}, want: 0},
		{name: "neighbors", callers: []string{"internal/a", "internal/b"}, want: 1},
		{name: "read either way", callers: []string{"internal/b", "cmd/termcast"}, want: 2},
		{name: "the widest pair", callers: []string{"cmd/termcast", "internal/c", "internal/queue", "std"}, want: 3},
		{name: "joined only through the definer", callers: []string{"lone", "std"}, want: spreadUnrelated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, callerSpread(ig, "run", tc.callers))
		})
	}
}

func TestSharedUsesReadTheExecShape(t *testing.T) {
	g := couplingGraph(execShape()...)

	uses := g.sharedUses(couplingOptions(), nil)
	require.Len(t, uses, 1, "Options and its fields are not callables")
	exec := uses[0]
	assert.Equal(t, sharedUse{
		ID: "internal/proc/run/exec.go#Exec", Source: "internal/proc/run/exec.go:135", Body: 157,
		Callers: []string{"cmd/termcast", "internal/a", "internal/b", "internal/c", "internal/queue", "std"},
		Spread:  3,
		Knobs: []modeKnob{
			{Name: "CancelGrace", Kind: knobOption, Setter: "internal/queue"},
			{Name: "TTYCols", Kind: knobOption, Setter: "cmd/termcast"},
			{Name: "TTYRows", Kind: knobOption, Setter: "cmd/termcast"},
		},
	}, exec)
	assert.True(t, exec.overShared(couplingOptions()))
	assert.False(t, exec.overShared(types.ConformOptions{}), "with nothing configured the rule holds no verdict")
}

func TestModeKnobs(t *testing.T) {
	callers := map[string]bool{"cmd/a": true, "cmd/b": true, "cmd/c": true}
	for _, tc := range []struct {
		name     string
		sig      string
		branches []string
		want     []modeKnob
	}{
		{name: "a bool the body tests", sig: "func Load(root string, global bool) error", branches: []string{"global"}, want: []modeKnob{{Name: "global", Kind: knobBranch}}},
		{name: "a local the body tests", sig: "func Load(root string) error", branches: []string{"ok"}},
		{name: "a func parameter is the caller's code", sig: "func boundedPhase(ctx context.Context, phase string, call func(context.Context) error) error"},
		{name: "data the body never branches on", sig: "func gitConfigSources(key string) []string", branches: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := types.KnowledgeNode{Attrs: map[string]string{AttrSignature: tc.sig, attrLanguage: "go"}}
			if len(tc.branches) > 0 {
				n.Attrs[attrBranchIdents] = tc.branches[0]
			}
			local := []descriptor{{Name: "F", Suffix: '('}}
			assert.Equal(t, tc.want, modeKnobs(NewGraph(), n, symbolShape(n, local), callers))
		})
	}
}

// The lens judges the run.Exec shape as one mode-knobs item, and a known entry that names a
// function no longer past the rule is stale.
func TestConformModeKnobsRow(t *testing.T) {
	g := couplingGraph(execShape()...)
	o := couplingOptions()

	out := g.Conform(o)
	knobs := conformRowFor(t, out, types.ConformModeKnobs, types.ConformPopSource, "")
	assert.Equal(t, []types.ConformItem{{ID: "internal/proc/run/exec.go#Exec", Source: "internal/proc/run/exec.go:135", Measure: 3}}, knobs.Violations)
	assert.Equal(t, []int{1, 157, 30}, []int{
		conformRowFor(t, out, types.ConformSharedBody, types.ConformPopSource, "").N,
		conformRowFor(t, out, types.ConformSharedBody, types.ConformPopSource, "").P99,
		conformRowFor(t, out, types.ConformSharedBody, types.ConformPopSource, "").Value,
	})
	assert.Equal(t, 3, conformRowFor(t, out, types.ConformCallerSpread, types.ConformPopSource, "").P99)
	assert.Equal(t, 6, conformRowFor(t, out, types.ConformSharedReach, types.ConformPopSource, "").P99)

	o.Ratchet = types.ConformRatchet{Schema: 1, Known: []types.ConformKnown{{
		Metric: types.ConformModeKnobs, Items: []string{"internal/proc/run/exec.go#Exec", "internal/a/a.go#Gone"},
		Reason: "one process primitive: TTY sizing is termcast's knob, CancelGrace the queue's", Line: 2,
	}}}
	out = g.Conform(o)
	knobs = conformRowFor(t, out, types.ConformModeKnobs, types.ConformPopSource, "")
	assert.Empty(t, knobs.Violations)
	assert.Equal(t, 1, knobs.Known)
	assert.Equal(t, []types.ConformKnown{{
		Metric: types.ConformModeKnobs, Items: []string{"internal/a/a.go#Gone"},
		Reason: "one process primitive: TTY sizing is termcast's knob, CancelGrace the queue's", Line: 2,
	}}, out.Stale)
}

func TestSharedLoopsAreDeterministic(t *testing.T) {
	o := couplingOptions()
	samples, violations := sharedLoops(couplingGraph(execShape()...), o)
	for range 10 {
		s, v := sharedLoops(couplingGraph(execShape()...), o)
		assert.Equal(t, samples, s)
		assert.Equal(t, violations, v)
	}
}

func TestSharedUsesWithoutASymbolIndex(t *testing.T) {
	assert.Empty(t, NewGraph().sharedUses(couplingOptions(), nil))
	samples, violations := sharedLoops(NewGraph(), couplingOptions())
	assert.Empty(t, samples)
	assert.Empty(t, violations)
}
