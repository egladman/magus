package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func cmdFunc(name, params string) types.KnowledgeSymbol {
	return goSym("cmd/magus", name+"().", name, "Function", "func "+name+"("+params+") error", "cmd/magus/"+name+".go")
}

// This tree's own case: five commands beside jobWatchFiles take the workspace root before the
// writer they report to, and it takes them the other way round.
func TestConformanceFindsParameterOrderDrift(t *testing.T) {
	peers := []types.KnowledgeSymbol{
		cmdFunc("applyDoctorFixes", "ctx context.Context, root string, out io.Writer"),
		cmdFunc("checkpointCmd", "ctx context.Context, root string, args []string, out io.Writer"),
		cmdFunc("notifyCmd", "ctx context.Context, root string, out io.Writer"),
		cmdFunc("printPlan", "root string, out io.Writer"),
		cmdFunc("writeReport", "root string, name string, out io.Writer"),
		// Takes neither name, so it is no evidence either way.
		cmdFunc("unrelated", "ctx context.Context, w io.Writer"),
	}
	subj := cmdFunc("jobWatchFiles", "ctx context.Context, out io.Writer, root string, plan func() []types.Job")
	g := namingGraph(t, append(peers, subj))

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(subj), Introduced: introduced(subj)})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckParamOrder,
		"`jobWatchFiles` takes `out` before `root`; 5 of 5 functions in its scope that take both take `root` first",
		"`root` first: applyDoctorFixes, checkpointCmd, notifyCmd",
	)}}, got)
}

func TestConformanceFindsParameterOrderDriftInTypeScript(t *testing.T) {
	sym := func(name, params string) types.KnowledgeSymbol {
		return tsSym("rows.ts", name, "function "+name+"("+params+"): void")
	}
	var syms []types.KnowledgeSymbol
	for _, name := range []string{"drawRow", "drawGutter", "drawHunk", "drawMarker", "drawFold"} {
		syms = append(syms, sym(name, "host: HTMLElement, row?: Row, ...rest: unknown[]"))
	}
	subj := sym("drawBadge", "row: Row, host: HTMLElement")
	g := namingGraph(t, append(syms, subj))

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(subj)})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckParamOrder,
		"`drawBadge` takes `row` before `host`; 5 of 5 functions in its scope that take both take `host` first",
		"`host` first: drawFold, drawGutter, drawHunk",
	)}}, got)
}

func TestConformanceSilentOnUndecidedParameterOrder(t *testing.T) {
	peers := []types.KnowledgeSymbol{
		cmdFunc("a1", "root string, out io.Writer"),
		cmdFunc("a2", "root string, out io.Writer"),
		cmdFunc("a3", "root string, out io.Writer"),
		cmdFunc("a4", "root string, out io.Writer"),
		cmdFunc("a5", "root string, out io.Writer"),
		cmdFunc("b1", "out io.Writer, root string"),
		cmdFunc("b2", "out io.Writer, root string"),
		cmdFunc("b3", "out io.Writer, root string"),
		cmdFunc("b4", "out io.Writer, root string"),
	}
	subj := cmdFunc("c1", "out io.Writer, root string")
	other := cmdFunc("c2", "root string, out io.Writer")
	g := namingGraph(t, append(peers, subj, other))

	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: subjectIDs(subj, other)}),
		"five one way and four the other is a workspace that has not decided")
}
