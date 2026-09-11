package hint

import (
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runs projects a breadcrumb set onto (id, run) pairs. The Why is prose and moves;
// the id is what a measurement counts and the run is what a reader pastes, so those
// two are what the assertions pin.
func runs(next []Next) [][2]string {
	out := make([][2]string, len(next))
	for i, n := range next {
		out[i] = [2]string{n.ID, n.Run}
	}
	return out
}

func TestNextForQuery(t *testing.T) {
	next := NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "spell:go", Kind: "spell", Label: "go"},
		{ID: "symbol:RunAll", Kind: types.KindSymbol, Label: "RunAll"},
		{ID: "spell:gomod", Kind: "spell", Label: "gomod"},
	}})

	assert.Equal(t, [][2]string{
		{"query-explain", "magus explain spell:go"},
		{"query-path", "magus path spell:go spell:gomod"},
		{"query-refs", "magus refs RunAll"},
	}, runs(next))
}

// The path breadcrumb needs two matches of ONE kind; a set with no repeated kind has
// no pair to connect and must not invent one.
func TestNextForQueryWithoutASharedKind(t *testing.T) {
	next := NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "spell:go", Kind: "spell", Label: "go"},
		{ID: "target:.:build", Kind: "target", Label: "build"},
	}})

	assert.Equal(t, [][2]string{{"query-explain", "magus explain spell:go"}}, runs(next))
}

// A page-level answer leaves a whole file to scan, so the sections it holds are the
// breadcrumb, ranked above the pair and symbol entries.
func TestNextForQueryOpensAMatchedDocPagesSections(t *testing.T) {
	next := NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "doc:docs/concepts/cache.md", Kind: types.KindDoc, Label: "docs/concepts/cache.md"},
		{ID: "doc:docs/concepts/sandbox.md", Kind: types.KindDoc, Label: "docs/concepts/sandbox.md"},
	}})

	assert.Equal(t, [][2]string{
		{"query-explain", "magus explain doc:docs/concepts/cache.md"},
		{"query-doc-sections", "magus query kind=docsection id=docs/concepts/cache.md"},
		{"query-path", "magus path doc:docs/concepts/cache.md doc:docs/concepts/sandbox.md"},
	}, runs(next))
}

// A result that already carries sections has led the reader to the passage; pointing
// at what is on the screen is the kind of breadcrumb nobody takes.
func TestNextForQuerySkipsDocSectionsWhenOneAlreadyMatched(t *testing.T) {
	next := NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "doc:docs/concepts/cache.md", Kind: types.KindDoc, Label: "docs/concepts/cache.md"},
		{ID: "docsection:docs/concepts/cache.md#key-inputs", Kind: types.KindDocSection, Label: "Key inputs"},
	}})

	assert.Equal(t, [][2]string{{"query-explain", "magus explain doc:docs/concepts/cache.md"}}, runs(next))
}

// Absence has to be measurable: nothing matched means no field at all, never an empty
// list a consumer would count as a suggestion nobody took.
func TestNextIsNilWhenThereIsNothingToSuggest(t *testing.T) {
	assert.Nil(t, NextForQuery(types.KnowledgeQueryOutput{}))
	assert.Nil(t, NextForExplain(types.KnowledgeExplainOutput{}))
	assert.Nil(t, NextForFiles(nil))
	assert.Nil(t, NextForAffected("ci", nil))
	assert.Nil(t, NextForFailure("", "", ""))
}

func TestNextForExplain(t *testing.T) {
	next := NextForExplain(types.KnowledgeExplainOutput{
		Node: types.KnowledgeNode{ID: "symbol:RunAll", Kind: types.KindSymbol, Label: "RunAll", Source: "internal/cache/run.go:42"},
		Out:  []types.KnowledgeEdgeRef{{Other: "spell:go"}, {Other: "spell:gomod"}},
		In:   []types.KnowledgeEdgeRef{{Other: "spell:gomod"}},
	})

	assert.Equal(t, [][2]string{
		{"explain-path", "magus path symbol:RunAll spell:gomod"},
		{"explain-refs", "magus refs RunAll"},
		{"explain-describe-file", "magus describe file internal/cache/run.go"},
	}, runs(next))
}

// A node reached by one edge each is a tie, and a tie goes to the card's own order
// rather than to whatever the map iterated first.
func TestNextForExplainBreaksNeighborTiesByCardOrder(t *testing.T) {
	card := types.KnowledgeExplainOutput{
		Node: types.KnowledgeNode{ID: "spell:go", Kind: "spell", Label: "go"},
		Out:  []types.KnowledgeEdgeRef{{Other: "op:go:go-build"}, {Other: "op:go:go-test"}},
	}
	for range 20 {
		require.Equal(t, "magus path spell:go op:go:go-build", NextForExplain(card)[0].Run)
	}
}

func TestNextForFiles(t *testing.T) {
	next := NextForFiles([]types.FileEntry{
		{Path: "MAGUS.md", OutputOf: []string{"."}, SourceOf: []string{"docs"}},
		{Path: "internal/hint/hint.go", SourceOf: []string{"."}},
	})

	assert.Equal(t, [][2]string{
		{"file-impact", "magus affected --impact"},
		{"file-regenerate", "magus run generate:rw ."},
	}, runs(next))
}

// A cross-project output lands in the owner's tree but only the declaring project's
// target regenerates it, so the breadcrumb names the declarer.
func TestNextForFilesRegeneratesFromTheDeclarer(t *testing.T) {
	next := NextForFiles([]types.FileEntry{{
		Path:     "docs/gen/index.html",
		OutputOf: []string{"docs"},
		Claims:   []types.FileClaim{{Project: "tools/site", Target: "render", Role: "output", Glob: "docs/gen/**"}},
	}})

	assert.Equal(t, [][2]string{{"file-regenerate", "magus run generate:rw tools/site"}}, runs(next))
}

func TestNextForAffected(t *testing.T) {
	next := NextForAffected("ci", []string{"libs/gopherbuzz", "docs"})

	assert.Equal(t, [][2]string{
		{"affected-plan", "magus affected ci --plan"},
		{"affected-explain", "magus affected --explain libs/gopherbuzz"},
	}, runs(next))
}

func TestNextForFailure(t *testing.T) {
	next := NextForFailure("libs/gopherbuzz", "test", "out1a2b3c")

	assert.Equal(t, [][2]string{
		{"run-output", "magus query output out1a2b3c"},
		{"run-explain-target", "magus explain target:libs/gopherbuzz:test"},
	}, runs(next))
}

// A failure that captured nothing still has a target to explain, and a ref line
// pointing at "" would be a command that cannot run.
func TestNextForFailureWithoutARef(t *testing.T) {
	next := NextForFailure("libs/gopherbuzz", "test", "")

	assert.Equal(t, [][2]string{{"run-explain-target", "magus explain target:libs/gopherbuzz:test"}}, runs(next))
}

// The cap is the context budget. No builder can exceed it today, which is exactly why
// it is asserted on the shared trim rather than through one of them: the next builder
// added is the one that would.
func TestNextCapsAtThree(t *testing.T) {
	assert.Equal(t, 3, nextCap, "three, one line each: a longer list is a wall nobody reads")

	long := []Next{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	assert.Equal(t, []Next{{ID: "a"}, {ID: "b"}, {ID: "c"}}, capNext(long))

	for _, build := range []func() []Next{
		func() []Next {
			return NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
				{ID: "symbol:A", Kind: types.KindSymbol, Label: "A"},
				{ID: "symbol:B", Kind: types.KindSymbol, Label: "B"},
				{ID: "symbol:C", Kind: types.KindSymbol, Label: "C"},
			}})
		},
		func() []Next {
			return NextForExplain(types.KnowledgeExplainOutput{
				Node: types.KnowledgeNode{ID: "symbol:A", Kind: types.KindSymbol, Label: "A", Source: "a.go:1"},
				Out:  []types.KnowledgeEdgeRef{{Other: "spell:go"}},
			})
		},
	} {
		assert.LessOrEqual(t, len(build()), nextCap)
	}
}

// Provenance is "path" or "path:line", and describe file classifies paths. A target's
// provenance is its project DIRECTORY, which has no classification worth asking for.
func TestNextSourcePath(t *testing.T) {
	assert.Equal(t, "internal/cache/run.go", sourcePath("internal/cache/run.go:42"))
	assert.Equal(t, "internal/cache/run.go", sourcePath("internal/cache/run.go"))
	assert.Equal(t, "spells/go/spell.buzz:main", sourcePath("spells/go/spell.buzz:main"))
	assert.Empty(t, sourcePath(""))
	assert.Empty(t, sourcePath("."), "a project directory is not a file to classify")
	assert.Empty(t, sourcePath("libs/gopherbuzz"))
}

// The role is read off the row, and a worker's lane comes back with it so the filter
// can place a write.
func TestRoleForGradesTheActingRow(t *testing.T) {
	rows := []types.Lease{
		{ID: "harness/worker", WritePaths: []string{"internal/hint/**"}},
		{ID: "harness/reviewer", ReadOnly: true, ReadPaths: []string{"cmd/magus/**"}},
		{ID: "harness/watcher"},
	}
	for _, tc := range []struct {
		id   string
		role Role
		lane []string
	}{
		{"", RoleUnbound, nil},
		{"harness/worker", RoleWorker, []string{"internal/hint/**"}},
		{"harness/reviewer", RoleReviewer, nil},
		{"harness/watcher", RoleReviewer, nil},
		{"harness/gone", RoleWorker, nil},
	} {
		role, lane := RoleFor(rows, tc.id)
		assert.Equal(t, tc.role, role, "id %q", tc.id)
		assert.Equal(t, tc.lane, lane, "id %q", tc.id)
	}
}

// Every template, graded per role. A reviewer is handed no write at all; a worker
// keeps the regeneration of its own project and loses everybody else's.
func TestServableToDropsWritesOutsideTheLane(t *testing.T) {
	all := []Next{
		breadcrumb("query-explain", Explain, "why", "spell:go"),
		breadcrumb("file-impact", Affected, "why", "--impact"),
		breadcrumb("file-regenerate", Run, "why", "generate:rw", "docs"),
	}
	for _, tc := range []struct {
		name string
		role Role
		lane []string
		want []string
	}{
		{"unbound", RoleUnbound, nil, []string{"query-explain", "file-impact", "file-regenerate"}},
		{"unset", "", nil, []string{"query-explain", "file-impact", "file-regenerate"}},
		{"worker in its lane", RoleWorker, []string{"docs/**"}, []string{"query-explain", "file-impact", "file-regenerate"}},
		{"worker out of its lane", RoleWorker, []string{"internal/hint/**"}, []string{"query-explain", "file-impact"}},
		{"worker with no lane", RoleWorker, nil, []string{"query-explain", "file-impact"}},
		{"reviewer owning the path anyway", RoleReviewer, []string{"docs/**"}, []string{"query-explain", "file-impact"}},
	} {
		var ids []string
		for _, n := range ServableTo(tc.role, tc.lane, all) {
			ids = append(ids, n.ID)
		}
		assert.Equal(t, tc.want, ids, tc.name)
	}
}

// A bare `magus run` writes wherever the workspace declares default charms, so the
// charm token is not what decides it; `affected` reads unless it is asked to run.
func TestMutatesTreeJudgesTheCommand(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{[]string{"magus", "run", "generate:rw", "."}, true},
		{[]string{"magus", "run", "test", "."}, true},
		{[]string{"magus", "affected", "ci"}, true},
		{[]string{"magus", "affected", "ci", "--plan"}, false},
		{[]string{"magus", "affected", "--impact"}, false},
		{[]string{"magus", "affected", "--explain", "libs/textsearch"}, false},
		{[]string{"magus", "vcs", "add", "."}, true},
		{[]string{"magus", "vcs", "checkpoint"}, false},
		{[]string{"magus", "explain", "spell:go"}, false},
		{[]string{"magus", "query", "kind=target"}, false},
		{[]string{"magus", "query", "output", "out1a2b3c"}, false},
		{[]string{"magus", "ledger", "accept", "harness/worker"}, true},
		{[]string{"magus", "ledger", "brief"}, false},
		{[]string{"magus", "memory", "put", "a", "b"}, true},
		{[]string{"magus", "notes", "edit", "a"}, true},
		{[]string{"magus", "clean"}, true},
		{[]string{"magus", "self", "update"}, true},
		{[]string{"magus", "config", "token", "generate"}, true},
		{[]string{"magus", "session", "lease", "harness/worker"}, true},
		{[]string{"magus", "brand-new-verb"}, true},
		{[]string{"magus"}, true},
		{nil, true},
	} {
		assert.Equal(t, tc.want, mutatesTree(tc.argv), "%v", tc.argv)
	}
}

// Every declared command has to be a verb the classifier KNOWS: one that resolves to
// itself rather than to a prefix of itself or to the deny-by-default fallthrough.
//
// `ledger accept` opening with the readable `ledger` is the case this catches. A
// shortest-prefix match graded it a read, which is how a reviewer would have been
// served a command that accepts its own work.
func TestEveryDeclaredCommandIsClassified(t *testing.T) {
	for _, c := range AllCommands {
		ran, ok := longestCommand(c.Argv()[1:])
		require.True(t, ok, "%s resolves to no declared command", c)
		assert.Equal(t, c.String(), ran.String(), "%s is graded as a different command", c)
	}

	for _, c := range readCommands {
		assert.True(t, slices.ContainsFunc(AllCommands, func(d Command) bool { return d.String() == c.String() }),
			"%s is graded readable and is not declared in AllCommands", c)
	}
}

// A breadcrumb renders twice: quoted for a shell, raw for an exec.
func TestBreadcrumbRendersRunAndArgv(t *testing.T) {
	n := breadcrumb("query-doc-sections", Query, "why", "kind=docsection", "id=docs/a b.md")
	assert.Equal(t, `magus query kind=docsection "id=docs/a b.md"`, n.Run)
	assert.Equal(t, []string{"magus", "query", "kind=docsection", "id=docs/a b.md"}, n.Argv)
}

// Both doors print one layout, so a reader who meets a breadcrumb over MCP and on a
// terminal meets the same two lines. A silenced Why leaves the command standing.
func TestRenderIsTheOneTwoLineLayout(t *testing.T) {
	next := []Next{
		{Run: "magus explain spell:go", Why: "explain names a node's edges."},
		{Run: "magus path spell:go spell:gomod"},
	}
	assert.Equal(t, "\nnext:\n"+
		"  magus explain spell:go\n"+
		"      explain names a node's edges.\n"+
		"  magus path spell:go spell:gomod\n",
		Render(next, func(n Next) string { return n.Why }))
	assert.Equal(t, "\nnext:\n"+
		"  magus explain spell:go\n"+
		"  magus path spell:go spell:gomod\n",
		Render(next, func(Next) string { return "" }))
	assert.Empty(t, Render(nil, func(n Next) string { return n.Why }))
}
