package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchTranslationDiagnostics pins the code-shaped arm: every row that denies names
// the exact query, and each near miss is a pattern whose answer the graph does not hold.
func TestSearchTranslationDiagnostics(t *testing.T) {
	t.Chdir("../..") // relative operands name this repository's tree
	const mgs30 = `query kind=diagnostic 'id=~^diagnostic:MGS30[23]\d$' -o name`
	for _, tt := range []struct {
		command string
		rule    denyRule // zero for no deny
	}{
		// The owner-observed command.
		{`grep -n "MGS30[23]" docs/reference/codes/sandbox/README.md; git add types/diagnostic.go`, denyRule{Name: denyRuleSearchTranslation, Arg: mgs30}},
		{`grep -rn 'MGS30[23]' .`, denyRule{Name: denyRuleSearchTranslation, Arg: mgs30}},
		{`rg 'MGS30[23]' types/diagnostic.go`, denyRule{Name: denyRuleSearchTranslation, Arg: mgs30}},
		{`git grep -n 'MGS30[23]'`, denyRule{Name: denyRuleSearchTranslation, Arg: mgs30}},
		{`grep -n 'MGS30..' docs/reference/codes/sandbox/README.md`, denyRule{Name: denyRuleSearchTranslation, Arg: `query kind=diagnostic 'id=~^diagnostic:MGS30\d{2}$' -o name`}},
		{`grep -rn 'MGS302[0-9]\|MGS303[0-9]' docs/`, denyRule{Name: denyRuleSearchTranslation, Arg: `query kind=diagnostic 'id=~^diagnostic:(?:MGS302[0-9]|MGS303[0-9])$' -o name`}},
		{`grep -rnE 'MGS302[0-9]|MGS303[0-9]' docs/`, denyRule{Name: denyRuleSearchTranslation, Arg: `query kind=diagnostic 'id=~^diagnostic:(?:MGS302[0-9]|MGS303[0-9])$' -o name`}},
		{`grep -rnw 'MGS30[0-9]\{2\}' .`, denyRule{Name: denyRuleSearchTranslation, Arg: `query kind=diagnostic 'id=~^diagnostic:MGS30[0-9][0-9]$' -o name`}},
		{`rg '\bMGS10\d\d\b'`, denyRule{Name: denyRuleSearchTranslation, Arg: `query kind=diagnostic 'id=~^diagnostic:MGS10\d{2}$' -o name`}},
		{`grep -o 'MGS[0-9]' docs/reference/codes/README.md`, denyRule{Name: denyRuleSearchTranslation, Arg: `query kind=diagnostic 'id=~^diagnostic:MGS[0-9]\d{3}$' -o name`}},
		// A literal code keeps symbol-search's per-code answer, on one file too.
		{`grep -n MGS1046 types/diagnostic.go`, denyRule{Name: denyRuleSymbolSearch, Arg: "diagnostic:MGS1046"}},
		{`grep -n 'MGS1046\|MGS3020' docs/reference/codes/README.md`, denyRule{Name: denyRuleSymbolSearch, Arg: "diagnostic:MGS1046,diagnostic:MGS3020"}},

		// Case-insensitive, inverted, counted, listed or with context: a different question.
		{`grep -in 'MGS30[23]' docs/reference/codes/sandbox/README.md`, denyRule{}},
		{`grep -v 'MGS30[23]' docs/reference/codes/sandbox/README.md`, denyRule{}},
		{`grep -c 'MGS30[23]' docs/reference/codes/sandbox/README.md`, denyRule{}},
		{`grep -rl 'MGS30[23]' docs/`, denyRule{}},
		{`grep -n -A3 'MGS30[23]' docs/reference/codes/sandbox/README.md`, denyRule{}},
		{`grep -x 'MGS3020' docs/reference/codes/sandbox/README.md`, denyRule{}},
		// A line anchor asks about layout, not about codes.
		{`grep -n '^MGS30[23]' docs/reference/codes/sandbox/README.md`, denyRule{}},
		// BZZ codes have no graph node.
		{`grep -rn 'BZZ10[0-9][0-9]' .`, denyRule{}},
		// An unregistered code may be a retired one a changelog still mentions.
		{`grep -rn 'MGS9999' .`, denyRule{}},
		{`grep -rn 'MGS30[23]\|MGS99[0-9][0-9]' .`, denyRule{}},
		// -w over three positions matches no whole code.
		{`grep -rnw 'MGS30[23]' .`, denyRule{}},
		// Five positions is not a code.
		{`grep -rn 'MGS30[23][0-9][0-9]' .`, denyRule{}},
		// Anything past the digits is text.
		{`grep -rn 'MGS30[23].*sandbox' .`, denyRule{}},
		// In BRE a bare `|` is a literal.
		{`grep -rn 'MGS3020|MGS3021' .`, denyRule{}},
		// grep reads `\d` as a literal d outside -P.
		{`grep -rn 'MGS30\d\d' .`, denyRule{}},
		// A log holds what one run emitted, which no node answers.
		{`grep -n 'MGS30[23]' out.log`, denyRule{}},
		{`grep -n 'MGS30[23]' docs/active.urls.lock`, denyRule{}},
		// A revision is not the tree the graph was built from.
		{`git grep -n 'MGS30[23]' origin/main -- types`, denyRule{}},
		{`git grep -n 'MGS30[23]' no-such-rev`, denyRule{}},
		{`git --no-pager grep -n 'MGS30[23]' origin/main -- types`, denyRule{}},
		// A pipe's stdin is not the tree.
		{`cat notes.txt | grep 'MGS30[23]'`, denyRule{}},
		// Another tree is not this workspace's graph.
		{`grep -rn 'MGS30[23]' /tmp/other-repo`, denyRule{}},
	} {
		v := Evaluate(Dependencies{}, tt.command)
		if tt.rule == (denyRule{}) {
			assert.Empty(t, v.Deny, "%q must not deny", tt.command)
			continue
		}
		assert.Equal(t, tt.rule, v.Rule, tt.command)
	}
}

// TestSearchTranslationShowsTheQuery pins the owner's command end to end: the deny leads
// with the runnable query, says why it is the same answer, and carries the answer.
func TestSearchTranslationShowsTheQuery(t *testing.T) {
	t.Chdir("../..")
	v := Evaluate(Dependencies{}, `grep -n "MGS30[23]" docs/reference/codes/sandbox/README.md; git add types/diagnostic.go`)
	assert.Contains(t, v.Deny, `query kind=diagnostic 'id=~^diagnostic:MGS30[23]\d$' -o name`+"` answers this search exactly.")
	assert.Contains(t, v.Deny, "can match nothing but a diagnostic code")
	assert.Contains(t, v.Deny, "MGS3020, MGS3021")
	assert.Contains(t, v.Deny, "Its answer (")
	assert.Contains(t, v.Deny, "\n  diagnostic:MGS3020\n")
}

// writeTree lays files out under a fresh root and returns it with symlinks resolved.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for rel, body := range files {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return root
}

func graphOf(ids map[string][]string) func(context.Context, string) ([]string, bool) {
	return func(_ context.Context, kind string) ([]string, bool) { return ids[kind], true }
}

// TestSearchTranslationHeadings pins the Markdown arm against a real tree: a deny needs
// the selected lines to match the graph's section nodes file for file.
func TestSearchTranslationHeadings(t *testing.T) {
	root := writeTree(t, map[string]string{
		"docs/a.md":      "# Alpha\n\ntext\n\n## Beta\n",
		"docs/b.md":      "# Gamma\n",
		"docs/fenced.md": "# Top\n\n```sh\n# a shell comment\n```\n",
		"docs/notes.txt": "# not markdown\n",
	})
	graph := graphOf(map[string][]string{"docsection": {
		"docsection:docs/a.md#alpha", "docsection:docs/a.md#beta",
		"docsection:docs/b.md#gamma", "docsection:docs/fenced.md#top",
	}})
	deps := Dependencies{GraphIDs: graph, scope: workspaceScope{root: root}}
	stale := Dependencies{GraphIDs: func(context.Context, string) ([]string, bool) { return nil, false }, scope: workspaceScope{root: root}}
	missing := Dependencies{GraphIDs: graphOf(map[string][]string{"docsection": {"docsection:docs/a.md#alpha"}}), scope: workspaceScope{root: root}}

	for _, tt := range []struct {
		command string
		deps    Dependencies
		arg     string // "" for no deny
	}{
		{`grep -n '^#' docs/a.md`, deps, `query kind=docsection 'id=~^docsection:docs/a\.md#' -o name`},
		{`grep -n '^#\+' docs/a.md docs/b.md`, deps, `query kind=docsection 'id=~^docsection:(?:docs/a\.md|docs/b\.md)#' -o name`},
		{`grep -nE '^#{1,6} ' docs/a.md`, deps, `query kind=docsection 'id=~^docsection:docs/a\.md#' -o name`},
		{`rg '^#+' docs/b.md`, deps, `query kind=docsection 'id=~^docsection:docs/b\.md#' -o name`},
		{`grep -rn --include='*.md' '^#' docs`, stale, ""},

		// A level-specific pattern asks what a section id does not say.
		{`grep -n '^## ' docs/a.md`, deps, ""},
		{`grep -n '^##.*Beta' docs/a.md`, deps, ""},
		// Under BRE `+` is a literal, so this selects no heading.
		{`grep -n '^#+' docs/a.md`, deps, ""},
		// A `#` in a fence is text the graph does not hold.
		{`grep -n '^#' docs/fenced.md`, deps, ""},
		// grep -r without --include reads every file, not just Markdown.
		{`grep -rn '^#' docs`, deps, ""},
		// Not Markdown.
		{`grep -n '^#' docs/notes.txt`, deps, ""},
		// The graph lacks a section the file has.
		{`grep -n '^#' docs/a.md`, missing, ""},
		// A cold or stale graph proves nothing.
		{`grep -n '^#' docs/a.md`, stale, ""},
	} {
		v, ok := translateSearches(tt.deps, root, parseForTest(t, tt.command))
		if tt.arg == "" {
			assert.False(t, ok && v.Deny != "", "%q must not deny", tt.command)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleSearchTranslation, Arg: tt.arg}, v.Rule, tt.command)
	}

	// The answer is the section ids the query prints.
	v, _ := translateSearches(deps, root, parseForTest(t, `grep -n '^#' docs/a.md`))
	assert.Contains(t, v.Deny, "Its answer (2 results):\n  docsection:docs/a.md#alpha\n  docsection:docs/a.md#beta\n")

	// A directory walk proves over every Markdown file grep would read.
	full := Dependencies{GraphIDs: graphOf(map[string][]string{"docsection": {
		"docsection:docs/a.md#alpha", "docsection:docs/a.md#beta", "docsection:docs/b.md#gamma",
	}}), scope: workspaceScope{root: root}}
	walkRoot := writeTree(t, map[string]string{"docs/a.md": "# Alpha\n## Beta\n", "docs/b.md": "# Gamma\n"})
	full.scope.root = walkRoot
	v, _ = translateSearches(full, walkRoot, parseForTest(t, `grep -rn --include='*.md' '^#' docs`))
	assert.Equal(t, denyRule{Name: denyRuleSearchTranslation, Arg: `query kind=docsection 'id=~^docsection:docs/[^#]*\.md#' -o name`}, v.Rule)
	assert.Contains(t, v.Deny, "3 heading lines across 2 files")
}

// TestSearchTranslationTargets pins the magusfile arm: every selected line must declare a
// target the graph holds, so one call or comment among the hits keeps it silent.
func TestSearchTranslationTargets(t *testing.T) {
	root := writeTree(t, map[string]string{
		"magusfile.buzz": "// lint everything\nexport fun lint(ctx: magus\\Context) > void {}\n" +
			"export fun lint_build(ctx: magus\\Context) > void {}\n" +
			"export fun build(ctx: magus\\Context) > void {\n    go[\"go-build\"](ctx);\n}\n",
		"docs/magusfile.buzz": "export fun render(ctx: magus\\Context) > void {}\n",
	})
	deps := Dependencies{GraphIDs: graphOf(map[string][]string{"target": {
		"target:.:lint", "target:.:lint-build", "target:.:build", "target:docs:render",
	}}), scope: workspaceScope{root: root}}

	for _, tt := range []struct {
		command string
		arg     string
	}{
		{`grep -n 'fun lint' magusfile.buzz`, `query kind=target 'id=~^(?:target:\.:lint|target:\.:lint-build)$' -o name`},
		{`grep -n 'fun lint(' magusfile.buzz`, "explain target:.:lint"},
		{`grep -nE 'fun lint\(' magusfile.buzz`, "explain target:.:lint"},
		{`grep -n 'fun render' docs/magusfile.buzz`, "explain target:docs:render"},
		{`grep -n 'export fun build' magusfile.buzz`, "explain target:.:build"},

		// The comment line is text.
		{`grep -n 'lint' magusfile.buzz`, ""},
		// go-build here is a spell op call, not a target of this file.
		{`grep -n 'go-build' magusfile.buzz`, ""},
		// A tree search reads files this proof has not.
		{`grep -rn 'fun lint' .`, ""},
	} {
		v, ok := translateSearches(deps, root, parseForTest(t, tt.command))
		if tt.arg == "" {
			assert.False(t, ok && v.Deny != "", "%q must not deny", tt.command)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleSearchTranslation, Arg: tt.arg}, v.Rule, tt.command)
		assert.Contains(t, v.Deny, "Its answer (")
	}
}

// TestSearchTranslationDeclarations pins the Go declaration arm: a listing of one file's
// declarations is answered by its file node and the names inline, a lookup of one name
// stays the symbol-search rule's, and a hit that is not a declaration keeps it silent.
func TestSearchTranslationDeclarations(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/store/store.go": "package store\n\n// func helper is documented here\ntype Store struct{}\n\n" +
			"func (s *Store) Open() error { return nil }\n\nfunc (s *Store) Close() error { return nil }\n\n" +
			"func helper() {}\n\nvar (\n\tdefaultStore = &Store{}\n)\n\nfunc use() { helper() }\n",
		"internal/store/store_test.go": "package store\n\nfunc TestOpen(t *testing.T) {}\n\nfunc TestClose(t *testing.T) {}\n\nfunc setup() {}\n",
	})
	indexed := map[string]bool{"Store": true, "Open": true, "Close": true, "helper": true, "use": true, "TestOpen": true, "TestClose": true, "setup": true}
	deps := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], true }, scope: workspaceScope{root: root}}
	stale := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], false }, scope: workspaceScope{root: root}}
	partial := Dependencies{SymbolDefined: func(name string) (bool, bool) { return name == "Open", true }, scope: workspaceScope{root: root}}

	for _, tt := range []struct {
		command string
		deps    Dependencies
		rule    denyRule
		answer  string
	}{
		{`grep -n '^func ' internal/store/store.go`, deps, denyRule{Name: denyRuleSearchTranslation, Arg: "explain file:internal/store/store.go"},
			"Its answer (4 results):\n  6: Open\n  8: Close\n  10: helper\n  16: use\n"},
		{`grep -n '^func Test' internal/store/store_test.go`, deps, denyRule{Name: denyRuleSearchTranslation, Arg: "explain file:internal/store/store_test.go"},
			"Its answer (2 results):\n  3: TestOpen\n  5: TestClose\n"},
		{`grep -n 'func (s \*Store)' internal/store/store.go`, deps, denyRule{Name: denyRuleSearchTranslation, Arg: "explain file:internal/store/store.go"},
			"  6: Open\n  8: Close\n"},
		{`grep -n '^func \|^type ' internal/store/store.go`, deps, denyRule{Name: denyRuleSearchTranslation, Arg: "explain file:internal/store/store.go"},
			"  4: Store\n  6: Open\n"},
		// One name is a definition lookup, which symbol-search advises rather than denies.
		{`grep -n 'func helper' internal/store/store.go`, deps, denyRule{}, ""},

		// The comment mentions `func helper` and the call site names helper: text.
		{`grep -n 'helper' internal/store/store.go`, deps, denyRule{}, ""},
		{`grep -n 'func' internal/store/store.go`, deps, denyRule{}, ""},
		// A block opener declares nothing.
		{`grep -n '^var' internal/store/store.go`, deps, denyRule{}, ""},
		// A name the index cannot vouch for.
		{`grep -n '^func ' internal/store/store.go`, partial, denyRule{}, ""},
		{`grep -n '^func ' internal/store/store.go`, stale, denyRule{}, ""},
		// Context, count and list flags ask a different question.
		{`grep -n -A2 '^func ' internal/store/store.go`, deps, denyRule{}, ""},
		{`grep -c '^func ' internal/store/store.go`, deps, denyRule{}, ""},
		// Two files, a glob, or a tree is not one file node.
		{`grep -n '^func ' internal/store/store.go internal/store/store_test.go`, deps, denyRule{}, ""},
		{`grep -n '^func ' internal/store/*.go`, deps, denyRule{}, ""},
		{`grep -rn '^func ' internal/store`, deps, denyRule{}, ""},
	} {
		cmds := parseForTest(t, tt.command)
		v, ok := translateSearches(tt.deps, root, cmds)
		if !ok {
			v, ok = searchVerdictAt(tt.deps, root, cmds)
		}
		if tt.rule == (denyRule{}) {
			assert.False(t, ok && v.Deny != "", "%q must not deny: %s", tt.command, v.Deny)
			continue
		}
		assert.Equal(t, tt.rule, v.Rule, tt.command)
		assert.Contains(t, v.Deny, tt.answer, tt.command)
	}
}

// TestSearchPipelines pins a search's pipe: each filter runs over the rows the proof read,
// so the deny answers the pipeline's question, and a filter that asks something the rows
// cannot answer keeps the rule silent.
func TestSearchPipelines(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/store/store.go": "package store\n\ntype Store struct{}\n\nfunc (s *Store) Open() error { return nil }\n\n" +
			"func (s *Store) Close() error { return nil }\n\nfunc helper() {}\n\nfunc use() { helper() }\n",
	})
	indexed := map[string]bool{"Store": true, "Open": true, "Close": true, "helper": true, "use": true}
	deps := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], true }, scope: workspaceScope{root: root}}
	const explain = "explain file:internal/store/store.go"

	for _, tt := range []struct {
		command string
		answer  string // "" for no deny
	}{
		{`grep -n '^func ' internal/store/store.go | head -2`, "Its answer, after `| head -2` (2 results):\n  5:func (s *Store) Open() error { return nil }\n  7:func (s *Store) Close() error { return nil }"},
		{`grep -n '^func ' internal/store/store.go | head -n 1`, "Its answer, after `| head -n 1` (1 result):\n  5:func"},
		{`grep -n '^func ' internal/store/store.go | tail -1`, "Its answer, after `| tail -1` (1 result):\n  11:func use() { helper() }"},
		{`grep -n '^func ' internal/store/store.go | wc -l`, "Its answer, after `| wc -l`: 4"},
		{`grep -n '^func ' internal/store/store.go | grep -v Store | wc -l`, "Its answer, after `| grep -v Store | wc -l`: 2"},
		{`grep -n '^func ' internal/store/store.go | grep -c helper`, "Its answer, after `| grep -c helper`: 2"},
		{`grep -n '^func ' internal/store/store.go | grep -v Store | head -1`, "Its answer, after `| grep -v Store | head -1` (1 result):\n  9:func helper() {}"},
		{`grep -n '^func ' internal/store/store.go | grep -iE 'OPEN|close'`, "(2 results):\n  5:func (s *Store) Open() error { return nil }\n  7:func (s *Store) Close() error { return nil }"},
		{`grep -n '^func ' internal/store/store.go | cut -d: -f1 | tr '\n' ' '`, "Its answer, after `| cut -d: -f1 | tr '\\n' ' '` (1 result):\n  5 7 9 11 "},
		{`grep -n '^func ' internal/store/store.go | wc -l | tr -d ' '`, "Its answer, after `| wc -l | tr -d ' '` (1 result):\n  4"},
		{`grep -n '^func ' internal/store/store.go | cut -c1-6`, "(4 results):\n  5:func\n  7:func\n  9:func\n  11:fun"},
		{`grep -n '^func ' internal/store/store.go | awk -F: '{print $1}' | sort -rn`, "(4 results):\n  11\n  9\n  7\n  5"},
		{`grep -n '^func ' internal/store/store.go | sed -n '2,3p'`, "(2 results):\n  7:func (s *Store) Close() error { return nil }\n  9:func helper() {}"},
		{`grep -n '^func ' internal/store/store.go | sed -E 's/^([0-9]+):.*/\1/' | sort -u | wc -l`, "Its answer, after `| sed -E 's/^([0-9]+):.*/\\1/' | sort -u | wc -l`: 4"},
		{`grep -n '^func ' internal/store/store.go | sed 's|func |fn |'`, "(4 results):\n  5:fn (s *Store) Open() error { return nil }"},
		{`grep -n '^func ' internal/store/store.go | sed '/Store/d'`, "(2 results):\n  9:func helper() {}\n  11:func use() { helper() }"},
		{`grep '^func ' internal/store/store.go | awk '{print $2}' | sort | uniq -c | sort -rn | head -1`, "(1 result):\n        2 (s"},
		{`grep -n '^func ' internal/store/store.go | cat`, "Its answer, after `| cat` (4 results):"},
		// A consumer that names its own input is a second command, not a filter.
		{`grep -n '^func ' internal/store/store.go; head -2 internal/store/store.go`, "Its answer (4 results):\n  5: Open"},

		// Filters that change the question in a way the rows cannot answer.
		{`grep -n '^func ' internal/store/store.go | tee out.txt`, ""},
		{`grep -n '^func ' internal/store/store.go | less`, ""},
		{`grep -n '^func ' internal/store/store.go | column -t`, ""},
		{`grep -n '^func ' internal/store/store.go | while read l; do echo "$l"; done`, ""},
		{`grep -n '^func ' internal/store/store.go | awk -F: '$1 > 5'`, ""},
		{`grep -n '^func ' internal/store/store.go | sort -k2`, ""},
		{`grep -n '^func ' internal/store/store.go | head -c 100`, ""},
		{`grep -n '^func ' internal/store/store.go | wc`, ""},
		{`grep -n '^func ' internal/store/store.go | grep -n helper`, ""},
		{`grep -n '^func ' internal/store/store.go | grep -o 'func [a-z]*'`, ""},
		{`grep -n '^func ' internal/store/store.go | sed 's/a/b/2'`, ""},
		{`grep -n '^func ' internal/store/store.go | tr -s ' '`, ""},
		{`grep -n '^func ' internal/store/store.go | cut -d: -f2 --complement`, ""},
		{`grep -n '^func ' internal/store/store.go | uniq -d`, ""},
		{`grep -n '^func ' internal/store/store.go | jq -R .`, ""},
	} {
		v, ok := translateSearches(deps, root, parseForTest(t, tt.command))
		if tt.answer == "" {
			assert.False(t, ok && v.Deny != "", "%q must not deny: %s", tt.command, v.Deny)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleSearchTranslation, Arg: explain}, v.Rule, tt.command)
		assert.Contains(t, v.Deny, tt.answer, tt.command)
	}

	// Without rows, a proof answers a truncation or an ordering over its entities and
	// nothing else.
	t.Chdir("../..")
	v := Evaluate(Dependencies{}, `grep -rn 'MGS30[23]' . | head -3`)
	assert.Equal(t, denyRuleSearchTranslation, v.Rule.Name)
	assert.Contains(t, v.Deny, "Its answer, after `| head -3` (3 results):\n  diagnostic:MGS3020\n")
	assert.Empty(t, Evaluate(Dependencies{}, `grep -rn 'MGS30[23]' . | wc -l`).Deny)
	assert.Empty(t, Evaluate(Dependencies{}, `grep -rn 'MGS30[23]' . | cut -d: -f1 | sort -u`).Deny)
	assert.Equal(t, denyRuleSymbolSearch, Evaluate(Dependencies{}, `grep -rn MGS1046 . | head`).Rule.Name)
	assert.Empty(t, Evaluate(Dependencies{}, `grep -rn MGS1046 . | wc -l`).Deny)
}

// TestFindTranslation pins the find arm: a name search whose files are, one for one, the
// file nodes the graph holds under the searched paths.
func TestFindTranslation(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/store/store.go":      "package store\n",
		"internal/store/store_test.go": "package store\n",
		"internal/store/sub/deep.go":   "package sub\n",
		"docs/a.md":                    "# A\n",
	})
	graph := graphOf(map[string][]string{"file": {
		"file:internal/store/store.go", "file:internal/store/store_test.go", "file:internal/store/sub/deep.go",
	}})
	deps := Dependencies{GraphIDs: graph, scope: workspaceScope{root: root}}
	stale := Dependencies{GraphIDs: func(context.Context, string) ([]string, bool) { return nil, false }, scope: workspaceScope{root: root}}
	partial := Dependencies{GraphIDs: graphOf(map[string][]string{"file": {"file:internal/store/store.go"}}), scope: workspaceScope{root: root}}

	for _, tt := range []struct {
		command string
		deps    Dependencies
		arg     string // "" for no deny
		answer  string
	}{
		{`find internal -name '*.go'`, deps, `query kind=file 'id=~^file:internal/(?:.*/)?[^/]*\.go$' -o name`,
			"Its answer (3 results):\n  file:internal/store/store.go\n  file:internal/store/store_test.go\n  file:internal/store/sub/deep.go"},
		{`find internal/store -name '*_test.go' -type f | wc -l`, deps, `query kind=file 'id=~^file:internal/store/(?:.*/)?[^/]*_test\.go$' -o name`, "Its answer, after `| wc -l`: 1"},
		{`find . -name '*.go' | head -1`, deps, `query kind=file 'id=~^file:(?:.*/)?[^/]*\.go$' -o name`, "Its answer, after `| head -1` (1 result):\n  ./internal/store/store.go"},
		{`find internal/store/ -maxdepth 1 -name '*.go' | sort`, deps, `query kind=file 'id=~^file:internal/store/(?:[^/]+/){0,0}[^/]*\.go$' -o name`,
			"(2 results):\n  internal/store/store.go\n  internal/store/store_test.go"},
		{`find internal -name 'deep.go' -print`, deps, `query kind=file 'id=~^file:internal/(?:.*/)?deep\.go$' -o name`, "file:internal/store/sub/deep.go"},

		// Markdown has no file node, so the sets differ.
		{`find docs -name '*.md'`, deps, "", ""},
		{`find . -name '*'`, deps, "", ""},
		// A predicate the file nodes do not answer.
		{`find internal -iname '*.go'`, deps, "", ""},
		{`find internal -name '*.go' -newer go.mod`, deps, "", ""},
		{`find internal -name '*.go' -not -path '*/sub/*'`, deps, "", ""},
		{`find internal -name '*.go' -exec cat {} \;`, deps, "", ""},
		{`find internal -type f`, deps, "", ""},
		// A pipe the rows cannot answer.
		{`find internal -name '*.go' | xargs grep -l package`, deps, "", ""},
		// The graph lacks a file, or cannot be trusted.
		{`find internal -name '*.go'`, partial, "", ""},
		{`find internal -name '*.go'`, stale, "", ""},
		// Another tree.
		{`find /tmp/other -name '*.go'`, deps, "", ""},
	} {
		v, ok := translateSearches(tt.deps, root, parseForTest(t, tt.command))
		if tt.arg == "" {
			assert.False(t, ok && v.Deny != "", "%q must not deny: %s", tt.command, v.Deny)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleSearchTranslation, Arg: tt.arg}, v.Rule, tt.command)
		assert.Contains(t, v.Deny, tt.answer, tt.command)
	}
}

func TestGlobRegexp(t *testing.T) {
	for glob, want := range map[string]string{
		`*.go`:        `[^/]*\.go`,
		`*_test.go`:   `[^/]*_test\.go`,
		`magusfile.?`: `magusfile\.[^/]`,
		`[ab]*.md`:    `[ab][^/]*\.md`,
		`a+b`:         `a\+b`,
	} {
		assert.Equal(t, want, globRegexp(glob), glob)
	}
}

// TestSearchTranslationAuditShapes runs the most frequent search shapes of the
// 2026-09-24 audit through Evaluate. Only the code-shaped ones have a provable answer
// without a graph; the rest are text the translator must leave alone.
func TestSearchTranslationAuditShapes(t *testing.T) {
	t.Chdir("../..")
	for _, tt := range []struct {
		command string
		deny    bool
	}{
		{`grep -n "MGS30[23]" docs/reference/codes/sandbox/README.md`, true},
		{`grep -rn "MGS10[0-9][0-9]" docs/reference/codes/`, true},
		{`grep -rn "someFunc\|otherFunc" internal/`, false},
		{`grep -n "func Judge" internal/guard/guard.go`, false},
		{`grep -rn "TODO" internal/`, false},
		{`grep -n "error" build.log`, false},
		{`grep -rn "magus run" docs/`, false},
		{`grep -rln "denyRule" internal/guard/`, false},
		{`grep -c "PASS" out.txt`, false},
		{`grep -n "^## " CHANGELOG.md`, false},
		{`grep -rn "go-build" .`, false},
		{`grep -rniE "timeout|deadline" internal/`, false},
		{`rg -n "type Verdict"`, false},
		{`grep -rn --include="*.go" "context.Background()" .`, false},
		{`grep -A5 "func Evaluate" internal/guard/shell.go`, false},
		{`git grep -n "hint.Classify"`, false},
		{`grep -rn "MAGUS_NO_WAIT" .`, false},
		{`grep -E "^(ok|FAIL)" test.log`, false},
		{`grep -n "schema_version" types/knowledge.go`, false},
		{`grep -rn "\"kind\": \"diagnostic\"" docs/`, false},
		{`rg -l "MGS30[23]"`, false},
	} {
		v := Evaluate(Dependencies{}, tt.command)
		isTranslation := v.Rule.Name == denyRuleSearchTranslation
		assert.Equal(t, tt.deny, isTranslation, tt.command)
	}
}

func TestToGoRegexp(t *testing.T) {
	for _, tt := range []struct {
		in   string
		mode regexMode
		perl bool
		out  string
		ok   bool
	}{
		{`a\|b`, regexBasic, false, `a|b`, true},
		{`a|b`, regexBasic, false, `a\|b`, true},
		{`\(x\)\{2\}`, regexBasic, false, `(x){2}`, true},
		{`#+`, regexBasic, false, `#\+`, true},
		{`*x`, regexBasic, false, `\*x`, true},
		{`a$b`, regexBasic, false, `a\$b`, true},
		{`[\]`, regexBasic, false, `[\\]`, true},
		{`[[:digit:]]x`, regexExtended, false, `[[:digit:]]x`, true},
		{`\<x\>`, regexExtended, false, `\bx\b`, true},
		{`\d`, regexExtended, false, ``, false},
		{`\d`, regexExtended, true, `\d`, true},
		{`(a)\1`, regexExtended, false, ``, false},
		{`a.b`, regexFixed, false, `a\.b`, true},
	} {
		got, ok := toGoRegexp(tt.in, tt.mode, tt.perl)
		assert.Equal(t, tt.ok, ok, tt.in)
		if tt.ok {
			assert.Equal(t, tt.out, got, tt.in)
		}
	}
}

func parseForTest(t *testing.T, command string) []hint.Invocation {
	t.Helper()
	cmds, ok := ParseCommands(command)
	require.True(t, ok, command)
	return cmds
}
