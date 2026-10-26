package knowledge

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func commandFunc(name, params string) types.KnowledgeSymbol {
	return goSym("cmd/magus", fmt.Sprintf("%s().", name), name, "Function", fmt.Sprintf("func %s(%s) error", name, params), fmt.Sprintf("cmd/magus/%s.go", name))
}

// This tree's own case: five commands beside jobWatchFiles take the workspace root before the
// writer they report to, and it takes them the other way round.
func TestConformanceFindsParameterOrderDrift(t *testing.T) {
	peers := []types.KnowledgeSymbol{
		commandFunc("applyDoctorFixes", "ctx context.Context, root string, out io.Writer"),
		commandFunc("checkpointCmd", "ctx context.Context, root string, args []string, out io.Writer"),
		commandFunc("notifyCmd", "ctx context.Context, root string, out io.Writer"),
		commandFunc("printPlan", "root string, out io.Writer"),
		commandFunc("writeReport", "root string, name string, out io.Writer"),
		// Takes neither name, so it is no evidence either way.
		commandFunc("unrelated", "ctx context.Context, w io.Writer"),
	}
	subj := commandFunc("jobWatchFiles", "ctx context.Context, out io.Writer, root string, plan func() []types.Job")
	g := namingGraph(t, append(peers, subj))

	got := g.ConformanceChecks(ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(subj), Introduced: introduced(subj)})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckParamOrder,
		"`jobWatchFiles` takes `out` before `root`; 5 of 5 functions in its scope that take both take `root` first",
		"`root` first: applyDoctorFixes, checkpointCmd, notifyCmd",
	)}}, got)
}

func TestConformanceFindsParameterOrderDriftInTypeScript(t *testing.T) {
	sym := func(name, params string) types.KnowledgeSymbol {
		return tsSym("rows.ts", name, fmt.Sprintf("function %s(%s): void", name, params))
	}
	var syms []types.KnowledgeSymbol
	for _, name := range []string{"drawRow", "drawGutter", "drawHunk", "drawMarker", "drawFold"} {
		syms = append(syms, sym(name, "host: HTMLElement, row?: Row, ...rest: unknown[]"))
	}
	subj := sym("drawBadge", "row: Row, host: HTMLElement")
	g := namingGraph(t, append(syms, subj))

	got := g.ConformanceChecks(ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(subj)})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckParamOrder,
		"`drawBadge` takes `row` before `host`; 5 of 5 functions in its scope that take both take `host` first",
		"`host` first: drawFold, drawGutter, drawHunk",
	)}}, got)
}

func TestConformanceSilentOnUndecidedParameterOrder(t *testing.T) {
	peers := []types.KnowledgeSymbol{
		commandFunc("a1", "root string, out io.Writer"),
		commandFunc("a2", "root string, out io.Writer"),
		commandFunc("a3", "root string, out io.Writer"),
		commandFunc("a4", "root string, out io.Writer"),
		commandFunc("a5", "root string, out io.Writer"),
		commandFunc("b1", "out io.Writer, root string"),
		commandFunc("b2", "out io.Writer, root string"),
		commandFunc("b3", "out io.Writer, root string"),
		commandFunc("b4", "out io.Writer, root string"),
	}
	subj := commandFunc("c1", "out io.Writer, root string")
	other := commandFunc("c2", "root string, out io.Writer")
	g := namingGraph(t, append(peers, subj, other))

	assert.Empty(t, g.ConformanceChecks(ConformanceChange{Conformance: testConformance(), Subjects: subjectIDs(subj, other)}),
		"five one way and four the other is a workspace that has not decided")
}
