package guard

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
)

func TestParseReadCall(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		command string
		want    readCall
		ok      bool
	}{
		{`cat a.go`, readCall{path: "a.go"}, true},
		{`cat -n a.go`, readCall{path: "a.go"}, true},
		{`nl -ba a.go`, readCall{path: "a.go"}, true},
		{`less a.go`, readCall{path: "a.go"}, true},
		{`head a.go`, readCall{path: "a.go", first: 1, last: 10}, true},
		{`head -n 30 a.go`, readCall{path: "a.go", first: 1, last: 30}, true},
		{`head -n30 a.go`, readCall{path: "a.go", first: 1, last: 30}, true},
		{`head -30 a.go`, readCall{path: "a.go", first: 1, last: 30}, true},
		{`head --lines=30 a.go`, readCall{path: "a.go", first: 1, last: 30}, true},
		{`tail -n 5 a.go`, readCall{path: "a.go", tail: 5}, true},
		{`tail -n +40 a.go`, readCall{path: "a.go", first: 40}, true},
		{`sed -n 10,40p a.go`, readCall{path: "a.go", first: 10, last: 40}, true},
		{`sed -n '10,40p' a.go`, readCall{path: "a.go", first: 10, last: 40}, true},
		{`sed -n -e '10,40p' a.go`, readCall{path: "a.go", first: 10, last: 40}, true},
		{`sed -ne '10,40p' a.go`, readCall{path: "a.go", first: 10, last: 40}, true},
		{`sed -n 10p a.go`, readCall{path: "a.go", first: 10, last: 10}, true},
		{`sed -n '10,$p' a.go`, readCall{path: "a.go", first: 10}, true},
		{`sed -n '10,+5p' a.go`, readCall{path: "a.go", first: 10, last: 15}, true},
		{`sed --quiet '10,40p' a.go`, readCall{path: "a.go", first: 10, last: 40}, true},

		// Two files, stdin, bytes, a follow, a regex address, an unquiet or in-place sed.
		{`cat a.go b.go`, readCall{}, false},
		{`cat -`, readCall{}, false},
		{`cat`, readCall{}, false},
		{`head -c 100 a.go`, readCall{}, false},
		{`tail -f a.go`, readCall{}, false},
		{`sed -n '/func/p' a.go`, readCall{}, false},
		{`sed '10,40p' a.go`, readCall{}, false},
		{`sed -n '10,40p;50p' a.go`, readCall{}, false},
		{`sed -i 's/a/b/' a.go`, readCall{}, false},
		{`bat -r 10:40 a.go`, readCall{}, false},
		{`grep -n func a.go`, readCall{}, false},
	} {
		cmds := parseForTest(t, tt.command)
		got, ok := parseReadCall(cmds[0])
		assert.Equal(t, tt.ok, ok, tt.command)
		assert.Equal(t, tt.want, got, tt.command)
	}
}

// TestReadCallsSkipConsumedOutput pins that only a read whose output reaches the reader
// counts: piped, redirected and substituted reads feed something else.
func TestReadCallsSkipConsumedOutput(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		command string
		paths   []string
	}{
		{`cat a.go`, []string{"a.go"}},
		{`time cat a.go`, []string{"a.go"}},
		{`cat a.go && sed -n 1,5p b.md`, []string{"a.go", "b.md"}},
		{`cat a.go 2>/dev/null`, []string{"a.go"}},
		{`cat a.go | grep x`, nil},
		{`cat a.go | head`, nil},
		{`cat a.go > out.txt`, nil},
		{`cat a.go >> out.txt`, nil},
		{`x=$(cat a.go)`, nil},
		{`diff <(cat a.go) b.go`, nil},
		{`echo x | cat a.go`, []string{"a.go"}},
	} {
		var got []string
		for _, rc := range readCalls(tt.command, DialectBash) {
			got = append(got, rc.path)
		}
		assert.Equal(t, tt.paths, got, tt.command)
	}
}

// readFixture is a tree with one Go file and one page over the threshold, and a Go file
// under it. Line numbers are asserted below, so the layout is spelled out here.
func readFixture(t *testing.T) string {
	t.Helper()
	body := func(n int) string { return strings.Repeat("\t_ = 0\n", n) }
	// 1 package, 3 comment, 4 type, 6 comment, 7-30 Open, 32-130 the Close method.
	store := "package store\n\n// Store holds nothing.\ntype Store struct{}\n\n// Open opens.\nfunc Open() error {\n" +
		body(22) + "}\n\nfunc (s *Store) Close() error {\n" + body(97) + "}\n"
	small := "package store\n\nfunc helper() {}\n"
	// 1 Alpha, 4 Beta, a fenced # on 6, filler, 131 Gamma.
	page := "# Alpha\n\nintro\n## Beta\n```sh\n# not a heading\n```\n" + strings.Repeat("text\n", 123) + "## Gamma\ntext\n"
	root := writeTree(t, map[string]string{
		"internal/store/store.go": store,
		"internal/store/small.go": small,
		"docs/a.md":               page,
		"tools/x.buzz":            strings.Repeat("fun f() {}\n", 130),
	})
	require.Equal(t, 130, strings.Count(store, "\n"))
	require.Equal(t, 132, strings.Count(page, "\n"))
	return root
}

func readDeps(root string) Dependencies {
	indexed := map[string]bool{"Store": true, "Open": true, "Close": true, "helper": true}
	return Dependencies{
		SymbolDefined: func(name string) (bool, bool) { return indexed[name], true },
		GraphIDs: graphOf(map[string][]string{"docsection": {
			"docsection:docs/a.md#alpha", "docsection:docs/a.md#beta", "docsection:docs/a.md#gamma",
		}}),
		scope: workspaceScope{root: root},
	}
}

func TestReadNavigationDeniesWholeReads(t *testing.T) {
	t.Parallel()
	root := readFixture(t)
	deps := readDeps(root)

	for _, tt := range []struct {
		command string
		answer  []string
	}{
		{`cat internal/store/store.go`, []string{
			"`" + hint.Explain.With("file:internal/store/store.go") + "` maps this file",
			"130 lines, 3 declarations",
			hint.Refs.With("<name>", "--definition", "--source"),
			"What the file holds (3 declarations):\n  3-4: Store\n  6-30: Open\n  32-130: Store.Close",
		}},
		{`cat -n internal/store/store.go`, nil},
		{`head -n 500 internal/store/store.go`, nil},
		{`sed -n '1,$p' internal/store/store.go`, nil},
		{`sed -n 1,200p internal/store/store.go`, nil},
		{`cat ./internal/store/store.go`, nil},
		{`cat ` + root + `/internal/store/store.go`, nil},
		{`cat docs/a.md`, []string{
			"query kind=docsection 'id=~^docsection:docs/a\\.md#' -o name` maps this file",
			"132 lines, 3 headings",
			"read that section by its line range",
			"What the file holds (3 headings):\n  1-3: # Alpha\n  4-130: ## Beta\n  131-132: ## Gamma",
		}},
	} {
		v, ok := readVerdictAt(deps, root, tt.command, DialectBash)
		require.True(t, ok, tt.command)
		assert.Equal(t, denyRuleReadNavigation, v.Rule.Name, tt.command)
		assert.Empty(t, v.Context, tt.command)
		for _, want := range tt.answer {
			assert.Contains(t, v.Deny, want, tt.command)
		}
	}
	v, _ := readVerdictAt(deps, root, `cat docs/a.md`, DialectBash)
	assert.NotContains(t, v.Deny, "refs", "no verb prints one section yet, so none is named")
}

func TestReadNavigationStaysSilent(t *testing.T) {
	t.Parallel()
	root := readFixture(t)
	deps := readDeps(root)
	stale := readDeps(root)
	stale.SymbolDefined = func(name string) (bool, bool) { return true, false }
	stale.GraphIDs = func(context.Context, string) ([]string, bool) { return nil, false }
	partial := readDeps(root)
	partial.SymbolDefined = func(name string) (bool, bool) { return name == "Open", true }
	partial.GraphIDs = graphOf(map[string][]string{"docsection": {"docsection:docs/a.md#alpha"}})
	noRoot := readDeps(root)
	noRoot.scope = workspaceScope{}

	for _, tt := range []struct {
		command string
		deps    Dependencies
	}{
		// Under the threshold.
		{`cat internal/store/small.go`, deps},
		// A kind no index maps.
		{`cat tools/x.buzz`, deps},
		// Outside the workspace, absent, a glob, unresolved.
		{`cat /etc/hosts`, deps},
		{`cat ../store.go`, deps},
		{`cat internal/store/missing.go`, deps},
		{`cat internal/store/*.go`, deps},
		{`cat $FILE`, deps},
		// The output feeds something else.
		{`cat internal/store/store.go | grep Open`, deps},
		{`cat internal/store/store.go > /tmp/copy.go`, deps},
		// A stale index or a page the graph disagrees with proves nothing.
		{`cat internal/store/store.go`, stale},
		{`cat docs/a.md`, stale},
		{`cat internal/store/store.go`, partial},
		{`cat docs/a.md`, partial},
		{`cat internal/store/store.go`, noRoot},
		// A bounded read across two declarations, or before the first.
		{`sed -n 1,5p internal/store/store.go`, deps},
		{`sed -n 6,40p internal/store/store.go`, deps},
		{`head -n 20 internal/store/store.go`, deps},
		// Between two declarations, which belongs to neither.
		{`sed -n 31p internal/store/store.go`, deps},
		// Inside a method: refs cannot name one method among same-named ones.
		{`sed -n '40,$p' internal/store/store.go`, deps},
		{`tail -n 5 internal/store/store.go`, deps},
		// A bounded read of a page: no verb prints one section.
		{`sed -n 4,20p docs/a.md`, deps},
		{`grep -n Open internal/store/store.go`, deps},
	} {
		v, ok := readVerdictAt(tt.deps, root, tt.command, DialectBash)
		assert.False(t, ok, "%s: %s%s", tt.command, v.Deny, v.Context)
	}
}

func TestReadNavigationSkipsGeneratedOutput(t *testing.T) {
	t.Parallel()
	root := readFixture(t)
	ws := mocks.NewMockWorkspaceRepository(t)
	ws.EXPECT().ClassifyFiles(mock.Anything, []string{"internal/store/store.go"}).
		Return([]types.FileEntry{{Path: "internal/store/store.go", Role: "output"}}, nil)
	deps := readDeps(root)
	deps.Inspect = func(context.Context, string) (types.WorkspaceRepository, error) { return ws, nil }

	_, ok := readVerdictAt(deps, root, `cat internal/store/store.go`, DialectBash)
	assert.False(t, ok)
}

// TestGoFileMapSpans pins each declaration's span to its own doc comment and closing
// line: a grouped var's members are separate entries, a method is named by its type, and
// a blank-identifier assertion neither counts nor voids the map.
func TestGoFileMapSpans(t *testing.T) {
	t.Parallel()
	src := "package store\n\nvar (\n\ta = 1\n\t// b is two.\n\tb = 2\n)\n\nvar _ = a\n\n" +
		"func F() {\n\t_ = b\n}\n\nfunc (s *S[T]) M() {}\ntype S[T any] struct{}\n"
	root := writeTree(t, map[string]string{"s.go": src})
	indexed := map[string]bool{"a": true, "b": true, "F": true, "M": true, "S": true}
	deps := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], true }}

	m, ok := goFileMap(deps, root+"/s.go", "s.go")

	require.True(t, ok)
	assert.Equal(t, 16, m.lines)
	assert.Equal(t, []mapEntry{
		{name: "a", symbol: "a", first: 4, last: 4},
		{name: "b", symbol: "b", first: 5, last: 6},
		{name: "F", symbol: "F", first: 11, last: 13},
		{name: "S.M", first: 15, last: 15},
		{name: "S", symbol: "S", first: 16, last: 16},
	}, m.entries)
}

// TestReadVerdictResolvesFromTheCallCwd pins that a relative read resolves from the
// judged call's directory, not the hook process's.
func TestReadVerdictResolvesFromTheCallCwd(t *testing.T) {
	root := readFixture(t)
	t.Chdir(t.TempDir())
	deps := readDeps(root)
	deps.callDir = root + "/internal/store"

	v, ok := readVerdict(deps, `cat store.go`, DialectBash)

	require.True(t, ok)
	assert.Equal(t, denyRule{Name: denyRuleReadNavigation, Arg: "internal/store/store.go"}, v.Rule)
}

func TestReadSymbolAdvisesBoundedReads(t *testing.T) {
	t.Parallel()
	root := readFixture(t)
	deps := readDeps(root)

	for _, tt := range []struct {
		command string
		name    string
		lines   string
	}{
		{`sed -n 7,30p internal/store/store.go`, "Open", "lines 7-30 of internal/store/store.go sit inside `Open` (6-30)"},
		{`sed -n 10,12p internal/store/store.go`, "Open", "lines 10-12"},
		{`sed -n 3,4p internal/store/store.go`, "Store", "lines 3-4"},
	} {
		v, ok := readVerdictAt(deps, root, tt.command, DialectBash)
		require.True(t, ok, tt.command)
		assert.Equal(t, denyRule{Name: advisoryReadSymbol, Arg: tt.name}, v.Rule, tt.command)
		assert.Empty(t, v.Deny, tt.command)
		assert.Contains(t, v.Context, tt.lines, tt.command)
		assert.Contains(t, v.Context, hint.Refs.With(tt.name, "--definition", "--source"), tt.command)
	}
}
