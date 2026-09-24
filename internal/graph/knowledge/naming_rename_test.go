package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// files is a Read over fixed contents.
func files(contents map[string]string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		text, ok := contents[path]
		return text, ok
	}
}

func referencedFrom(s types.KnowledgeSymbol, paths ...string) types.KnowledgeSymbol {
	for _, p := range paths {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: p, Count: 1})
	}
	return s
}

// The miss TestCommentsNameSymbolsThatExist found on its first run: TrackDependencyWait became
// WithDependencyWait, and comments in files the rename had otherwise touched kept the old name.
func TestConformanceFindsARenameLeftover(t *testing.T) {
	subj := referencedFrom(goSym("types", "WithDependencyWait().", "WithDependencyWait", "Function",
		"func WithDependencyWait(ctx context.Context, d time.Duration) context.Context", "types/target.go"),
		"internal/run/wait.go", "internal/run/wait_test.go")
	g := namingGraph(t, []types.KnowledgeSymbol{subj})
	read := files(map[string]string{
		"types/target.go":           "func WithDependencyWait(ctx context.Context, d time.Duration) context.Context {",
		"internal/run/wait.go":      "// TrackDependencyWait bounds the wait.\nctx = types.WithDependencyWait(ctx, d)\n",
		"internal/run/wait_test.go": "ctx := types.WithDependencyWait(ctx, 0) // trackDependencyWait was here\n",
	})

	got := g.Conformance(ConformanceChange{
		Subjects: subjectIDs(subj), Introduced: introduced(subj), Read: read,
		Renamed: map[string]string{symID(subj): "TrackDependencyWait"},
	})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckRenameLeftover,
		"`WithDependencyWait` replaced `TrackDependencyWait`, which 2 lines in 2 of the 3 files referencing it still name",
		"internal/run/wait.go:1", "internal/run/wait_test.go:1",
	)}}, got)
}

func TestConformanceFindsARenameLeftoverInTypeScript(t *testing.T) {
	subj := referencedFrom(tsSym("plan.ts", "applyPlan", "function applyPlan(p: Plan): void"), "web/src/queue.ts")
	g := namingGraph(t, []types.KnowledgeSymbol{subj})
	read := files(map[string]string{
		"web/src/plan.ts":  "export function applyPlan(p: Plan): void {}\n",
		"web/src/queue.ts": "import { applyPlan } from './plan';\n\n// landPlan runs once per queue entry.\nconst LAND_PLAN_RETRIES = 3;\n",
	})

	got := g.Conformance(ConformanceChange{
		Subjects: subjectIDs(subj), Read: read, Renamed: map[string]string{symID(subj): "landPlan"},
	})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckRenameLeftover,
		"`applyPlan` replaced `landPlan`, which 2 lines in 1 of the 2 files referencing it still name",
		"web/src/queue.ts:3", "web/src/queue.ts:4",
	)}}, got)
}

func TestConformanceSilentOnRenamesItCannotTellApart(t *testing.T) {
	longer := referencedFrom(ctxReader("internal/trail", "EntryPointFromContext"), "internal/cmd/run.go")
	oneWord := referencedFrom(goFunc("internal/queue", "Apply", "func Apply(p Plan) error"), "internal/cmd/run.go")
	shared := referencedFrom(goFunc("internal/vcs", "ReadFileAt", "func ReadFileAt(path string) string"), "internal/cmd/run.go")
	// Another casing of the old name is still a live symbol, which a mention may mean.
	survivor := goFunc("internal/other", "openFileAt", "func openFileAt(path string) string")
	g := namingGraph(t, []types.KnowledgeSymbol{longer, oneWord, shared, survivor})
	read := files(map[string]string{
		"internal/cmd/run.go": "ep := trail.EntryPointFromContext(ctx)\nqueue.Apply(p) // Land was the old name\nvcs.ReadFileAt(p) // OpenFileAt too\n",
	})

	got := g.Conformance(ConformanceChange{
		Subjects: subjectIDs(longer, oneWord, shared), Read: read,
		Renamed: map[string]string{
			symID(longer):  "EntryPointFrom", // every use of the new name spells the old one inside it
			symID(oneWord): "Land",           // one word is a word everybody uses
			symID(shared):  "OpenFileAt",     // still defined elsewhere, so a mention may mean that one
		},
	})

	assert.Empty(t, got)
	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: subjectIDs(longer), Renamed: map[string]string{symID(longer): "EntryPointFrom"}}),
		"no reader, no search")
}
