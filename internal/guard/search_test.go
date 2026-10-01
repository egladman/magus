package guard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestSymbolSearchDeniesEveryProvableShape widens the deny past one bare identifier: an
// alternation, a definition lookup, and a diagnostic code, whenever ANY name the pattern
// looks for is one the graph answers. The 2026-09-24 audit measured 45% of search patterns
// as alternations and 13% as definition lookups, and the single-name deny fired 0 times.
func TestSymbolSearchDeniesEveryProvableShape(t *testing.T) {
	indexed := map[string]bool{"HandleRequest": true, "ParseConfig": true, "Judge": true, "Verdict": true}
	deps := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], true }, GraphIDs: diagnosticGraph}
	stale := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], false }, GraphIDs: diagnosticGraph}

	for _, tt := range []struct {
		command string
		arg     string // the names the deny routes to; "" for no deny
	}{
		{`grep -rn 'HandleRequest\|ParseConfig' .`, "HandleRequest,ParseConfig"},
		{`grep -rn -e HandleRequest -e ParseConfig .`, "HandleRequest,ParseConfig"},
		{`grep -rnE 'HandleRequest|ParseConfig' internal/`, "HandleRequest,ParseConfig"},
		{`rg 'HandleRequest|ParseConfig'`, "HandleRequest,ParseConfig"},
		{`git grep -n 'HandleRequest\|ParseConfig'`, "HandleRequest,ParseConfig"},
		{`git --no-pager grep -n 'HandleRequest\|ParseConfig'`, "HandleRequest,ParseConfig"},
		// -C searches another tree, and -c can change the pattern dialect.
		{`git -C /tmp/other-repo grep -n 'HandleRequest\|ParseConfig'`, ""},
		{`git -c grep.patternType=perl grep -n 'HandleRequest\|ParseConfig'`, ""},
		{`rg '\bHandleRequest\b'`, "HandleRequest"},
		{`grep -rn 'func Judge' internal/`, "Judge"},
		{`grep -rn 'func Judge(' internal/`, "Judge"},
		{`grep -rn 'func (g \*Gate) Judge' internal/`, "Judge"},
		{`rg '^func \(.*\) Judge'`, "Judge"},
		{`rg 'type Verdict'`, "Verdict"},
		{`grep -rnE 'func\s+Judge|type Verdict' .`, "Judge,Verdict"},
		{`rg 'HandleRequest|MGS3010'`, "HandleRequest,diagnostic:MGS3010"},

		// One name is enough: the text alternative is served on its own.
		{`grep -rn 'HandleRequest\|someText' .`, "HandleRequest"},
		{`grep -rn 'HandleRequest\|NotIndexedHere' .`, "HandleRequest"},
		// Under BRE a bare `|` is a literal, so this looks for one string, not two names.
		{`grep -rn 'HandleRequest|ParseConfig' .`, ""},
		// Fixed strings have no alternation at all.
		{`grep -rnF 'HandleRequest\|ParseConfig' .`, ""},
		// -i widens a plain word into text, but a CamelCase name is still that name.
		{`grep -rni 'HandleRequest\|ParseConfig' .`, "HandleRequest,ParseConfig"},
		{`grep -rni '\bJudge\b' .`, ""},
		{`grep -rn 'func Unknown' .`, ""},
		// A stale index still knows the name, and the deny serves the rebuild first.
		{`grep -rn 'func Judge' internal/`, "stale"},
		// Another tree is not this workspace's graph.
		{`grep -rn 'func Judge' /tmp/other-repo`, ""},
		// With no workspace root a named file cannot be placed, so it is left alone.
		{`grep -n 'func Judge' internal/guard/guard.go`, ""},
	} {
		d := deps
		want := tt.arg
		if tt.arg == "stale" {
			d, want = stale, "Judge"
		}
		v := Evaluate(d, tt.command)
		if want == "" {
			assert.Empty(t, v.Deny, "%q must not deny", tt.command)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleSymbolSearch, Arg: want}, v.Rule, tt.command)
		assert.Equal(t, tt.arg == "stale", strings.Contains(v.Deny, "graph build --silent"), "%q: only a stale index serves the rebuild", tt.command)
		for _, name := range strings.Split(want, ",") {
			assert.Contains(t, v.Deny, name, "%q: one command per name", tt.command)
		}
	}
}

// TestSymbolSearchOnNamedFiles pins the file half: a search of ONE named Go file for an
// indexed name is a read, and runs with refs advised; anything the index does not cover, or
// a flag that changes the question, gets no such advice. Two files or a glob is a search,
// pinned in TestSymbolSearchAnswersTreeSearches.
func TestSymbolSearchOnNamedFiles(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/api/handler.go": "package api\n\n// HandleRequest serves one request.\nfunc HandleRequest() {}\n\nfunc serve() { HandleRequest() }\n",
		"internal/api/config.go":  "package api\n\nfunc ParseConfig() {}\n",
		"docs/handler.md":         "HandleRequest is documented here.\n",
	})
	indexed := map[string]bool{"HandleRequest": true, "ParseConfig": true}
	deps := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], true }, scope: workspaceScope{root: root}}
	stale := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], false }, scope: workspaceScope{root: root}}

	for _, tt := range []struct {
		command string
		deps    Dependencies
		advice  string // the refs command advised, "" for none
	}{
		{`grep -n HandleRequest internal/api/handler.go`, deps, "refs HandleRequest --occurrences` answers this for every file."},
		{`rg -n 'HandleRequest\(' internal/api/handler.go`, deps, "refs HandleRequest --occurrences`"},
		{`grep -n 'func HandleRequest' internal/api/handler.go`, deps, "refs HandleRequest --definition --source`"},
		{`grep -n ParseConfig internal/api/handler.go`, deps, "refs ParseConfig --occurrences`"},
		// A pipe changes nothing: the search still runs as typed.
		{`grep -n HandleRequest internal/api/handler.go | head -1`, deps, "refs HandleRequest --occurrences`"},
		{`grep -n HandleRequest internal/api/handler.go | tee out.txt`, deps, "refs HandleRequest --occurrences`"},

		// A stale index proves nothing.
		{`grep -n HandleRequest internal/api/handler.go`, stale, ""},
		// Text, and a name the index does not hold.
		{`grep -n serve internal/api/handler.go`, deps, ""},
		{`grep -n 'HandleRequest\|serve' internal/api/handler.go`, deps, ""},
		// Prose is not what the symbol index covers.
		{`grep -n HandleRequest docs/handler.md`, deps, ""},
		{`grep -n HandleRequest internal/api/handler.go docs/handler.md`, deps, ""},
		// Context, count, list, invert and case flags ask a different question.
		{`grep -n -B2 HandleRequest internal/api/handler.go`, deps, ""},
		{`grep -c HandleRequest internal/api/handler.go`, deps, ""},
		{`grep -l HandleRequest internal/api/handler.go`, deps, ""},
		{`grep -v HandleRequest internal/api/handler.go`, deps, ""},
		{`grep -in handlerequest internal/api/handler.go`, deps, ""},
		// A missing file cannot be read.
		{`grep -n HandleRequest internal/api/missing.go`, deps, ""},
		// A pipe is not a file.
		{`cat internal/api/handler.go | grep -n HandleRequest`, deps, ""},
	} {
		v, _ := searchVerdictAt(tt.deps, root, parseForTest(t, tt.command))
		assert.Empty(t, v.Deny, "%q must not deny", tt.command)
		if tt.advice == "" {
			assert.NotContains(t, v.Brief, "this for every file", tt.command)
			continue
		}
		assert.Equal(t, advisoryPrecedent, v.Kind, tt.command)
		assert.Contains(t, v.Brief, tt.advice, tt.command)
		assert.Contains(t, v.Context, "The search runs as typed.", tt.command)
	}
}

// TestGrepReaderDeniesContextReads pins grep-reader: a definition lookup with a context
// flag is a read of the body, even of one named file, and is served refs where the index
// vouches for the name and the declaration's own lines where it does not.
func TestGrepReaderDeniesContextReads(t *testing.T) {
	root := writeTree(t, map[string]string{
		// 3-4 HandleRequest, 6 serve, 8-10 Config.Load.
		"internal/api/handler.go": "package api\n\n// HandleRequest serves one request.\nfunc HandleRequest() {}\n\nfunc serve() { HandleRequest() }\n\nfunc (c *Config) Load() {\n\tserve()\n}\n",
		"internal/api/config.go":  "package api\n\ntype Config struct{}\n",
	})
	indexed := map[string]bool{"HandleRequest": true, "Config": true}
	sites := map[string][]types.KnowledgeRefSite{"Load": {{File: "internal/api/handler.go"}}}
	deps := Dependencies{
		SymbolDefined: func(name string) (bool, bool) { return indexed[name], true },
		SymbolSites:   func(name string) ([]types.KnowledgeRefSite, bool) { return sites[name], false },
		scope:         workspaceScope{root: root},
		callDir:       root,
	}
	stale := deps
	stale.SymbolDefined = func(name string) (bool, bool) { return indexed[name], false }

	for _, tt := range []struct {
		command string
		deps    Dependencies
		serves  []string // the commands served, nil for no deny
	}{
		{`grep -n 'func HandleRequest' -A 20 internal/api/handler.go`, deps, []string{"magus refs HandleRequest --definition --source"}},
		{`grep -nA20 'func HandleRequest' internal/api/handler.go`, deps, []string{"magus refs HandleRequest --definition --source"}},
		{`grep -rn --after-context=40 '^type Config struct' internal/`, deps, []string{"magus refs Config --definition --source"}},
		{`rg -C5 'func HandleRequest|type Config' internal/api`, deps, []string{"magus refs HandleRequest --definition --source", "magus refs Config --definition --source"}},
		{`grep -n -5 'func HandleRequest' internal/api/handler.go`, deps, []string{"magus refs HandleRequest --definition --source"}},
		{`grep -n 'func HandleRequest' -A20 internal/api/handler.go | grep serve`, deps, []string{"magus refs HandleRequest --definition --source"}},
		// A stale index gets the declaration's lines from a parse of the file.
		{`grep -n 'func HandleRequest' -A 20 internal/api/handler.go`, stale, []string{"sed -n 3,4p internal/api/handler.go"}},
		{`grep -n 'func HandleRequest' -A 20 internal/api/*.go`, stale, []string{"sed -n 3,4p internal/api/handler.go"}},
		// A method the index does not vouch for, found under a directory through the files
		// the index last saw name it.
		{`grep -rn 'func (c \*Config) Load' -A10 internal/`, deps, []string{"sed -n 8,10p internal/api/handler.go"}},

		// No context flag: the search is a lookup, symbol-search's to judge.
		{`grep -n 'func HandleRequest' internal/api/handler.go`, deps, nil},
		// A use, not a definition; a case-insensitive search; one alternative that is text.
		{`grep -n -A5 'HandleRequest' internal/api/handler.go`, deps, nil},
		{`grep -in -A5 'func handlerequest' internal/api/handler.go`, deps, nil},
		{`grep -n -A5 'func HandleRequest\|TODO' internal/api/handler.go`, deps, nil},
		// No declaration of the name anywhere the search reads.
		{`grep -n -A5 'func Missing' internal/api/handler.go`, deps, nil},
		// A context flag's value is not a pattern, and -e's value is not a flag.
		{`grep -n -e 'A' internal/api/handler.go`, deps, nil},
	} {
		v, ok := grepReaderVerdict(tt.deps, parseForTest(t, tt.command))
		if tt.serves == nil {
			assert.False(t, ok, "%q: %s", tt.command, v.Deny)
			continue
		}
		require.True(t, ok, tt.command)
		assert.Equal(t, denyRuleGrepReader, v.Rule.Name, tt.command)
		for _, run := range tt.serves {
			assert.Contains(t, v.Deny, run+"`", tt.command)
		}
		require.Len(t, v.Next, len(tt.serves), tt.command)
	}

	piped, _ := grepReaderVerdict(deps, parseForTest(t, `grep -n 'func HandleRequest' -A20 internal/api/handler.go | grep serve`))
	assert.Contains(t, piped.Deny, "The pipe after the search is not reproduced")
	// Evaluate reaches it ahead of symbol-search's single-file advice.
	assert.Equal(t, denyRuleGrepReader, Evaluate(deps, `grep -n 'func HandleRequest' -A 20 internal/api/handler.go`).Rule.Name)
}

// TestJudgeResolvesSearchPathsFromTheCallCwd pins that a relative path resolves from the
// envelope's cwd, not the hook process's: the file exists only under the call's directory.
func TestJudgeResolvesSearchPathsFromTheCallCwd(t *testing.T) {
	testkit.Isolate(t)
	root := writeTree(t, map[string]string{
		"internal/api/handler.go": "package api\n\nfunc HandleRequest() {}\n\nfunc serve() {}\n",
	})
	t.Chdir(t.TempDir())
	deps := testDependencies()
	deps.SymbolDefined = func(name string) (bool, bool) { return name == "HandleRequest" || name == "serve", true }
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: root})
	envelope := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"` + filepath.Join(root, "internal", "api") +
		`","tool_input":{"command":"grep -n '^func ' handler.go"}}`

	v := Judge(ctx, deps, Request{Input: envelope})

	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, string(denyRuleSearchTranslation), v.Rule)
	assert.Contains(t, v.Reason, "explain file:internal/api/handler.go")
}

// TestSymbolSearchAnswersTreeSearches pins the inline answer of a tree search: the sites
// the index holds under the searched paths, labeled as refs' answer and never filtered by
// a pipe, and the routing deny alone when the index cannot say.
func TestSymbolSearchAnswersTreeSearches(t *testing.T) {
	root := writeTree(t, map[string]string{
		"internal/api/handler.go":      "package api\n\n// HandleRequest serves one request.\nfunc HandleRequest() {}\n\nfunc serve() { HandleRequest() }\n",
		"internal/api/handler_test.go": "package api\n\nfunc TestHandle(t *testing.T) { HandleRequest() }\n",
		"internal/api/config.go":       "package api\n\nfunc ParseConfig() {}\n",
	})
	sites := map[string][]types.KnowledgeRefSite{
		"HandleRequest": {{File: "internal/api/handler.go", Count: 2, Lines: []int{4, 6}}, {File: "internal/api/handler_test.go", Count: 1, Lines: []int{3}}, {File: "cmd/x.go", Count: 1, Lines: []int{9}}},
		"ParseConfig":   {{File: "internal/api/config.go", Count: 1, Lines: []int{3}}},
	}
	indexed := func(name string) (bool, bool) { return sites[name] != nil, true }
	deps := Dependencies{SymbolDefined: indexed, SymbolSites: func(name string) ([]types.KnowledgeRefSite, bool) { return sites[name], true }, scope: workspaceScope{root: root}}
	blind := Dependencies{SymbolDefined: indexed, scope: workspaceScope{root: root}}
	const handler = "Its answer (2 results):\n  internal/api/handler.go  (2)  lines 4,6\n  internal/api/handler_test.go  (1)  lines 3"
	const notReproduced = "\nThe pipe after the search is not reproduced: run it over the command's output."

	for _, tt := range []struct {
		command string
		deps    Dependencies
		run     string
		answer  string // "" for the routing deny alone
		piped   bool
	}{
		{`grep -rn HandleRequest internal`, deps, "refs HandleRequest --occurrences", handler, false},
		{`grep -rn HandleRequest ./internal/`, deps, "refs HandleRequest --occurrences", handler, false},
		{`grep -rn HandleRequest internal | grep -v _test`, deps, "refs HandleRequest --occurrences", handler, true},
		{`grep -rn HandleRequest internal | cut -d: -f1 | sort | uniq -c`, deps, "refs HandleRequest --occurrences", handler, true},
		{`grep -rn HandleRequest internal | tee sites.txt`, deps, "refs HandleRequest --occurrences", handler, true},
		{`grep -rn --include='*_test.go' HandleRequest internal`, deps, "refs HandleRequest --occurrences",
			"Its answer (1 result):\n  internal/api/handler_test.go  (1)  lines 3", false},
		{`rg 'HandleRequest|ParseConfig' internal/api`, deps, "refs ParseConfig --occurrences",
			"Its answer (3 results):\n  internal/api/config.go  (1)  lines 3\n  internal/api/handler.go  (2)  lines 4,6", false},
		// Two named files are a search, not a read.
		{`grep -n 'HandleRequest\|ParseConfig' internal/api/handler.go internal/api/config.go`, deps, "refs ParseConfig --occurrences",
			"Its answer (2 results):\n  internal/api/config.go  (1)  lines 3\n  internal/api/handler.go  (2)  lines 4,6", false},
		// A search no parser models, a glob, or an index with no sites keeps the routing deny alone.
		{`grep -rl HandleRequest internal`, deps, "refs HandleRequest --occurrences", "", false},
		{`grep -n HandleRequest internal/api/*.go`, deps, "refs HandleRequest --occurrences", "", false},
		{`grep -rn HandleRequest internal | wc -l`, blind, "refs HandleRequest --occurrences", "", true},
	} {
		v, ok := searchVerdictAt(tt.deps, root, parseForTest(t, tt.command))
		require.True(t, ok && v.Deny != "", tt.command)
		assert.Equal(t, denyRuleSymbolSearch, v.Rule.Name, tt.command)
		assert.Contains(t, v.Deny, tt.run, tt.command)
		assert.NotContains(t, v.Deny, "after `|", tt.command)
		if tt.answer == "" {
			assert.NotContains(t, v.Deny, "Its answer", tt.command)
			continue
		}
		assert.Contains(t, v.Deny, tt.answer, tt.command)
		assert.Equal(t, tt.piped, strings.HasSuffix(v.Deny, notReproduced), tt.command)
	}
}

// TestSymbolSearchStaysSilentOutsideTheWorkspace pins the scope half against a real root:
// a search whose every path lies outside the workspace has no graph answer here.
func TestSymbolSearchStaysSilentOutsideTheWorkspace(t *testing.T) {
	deps := Dependencies{
		SymbolDefined: func(string) (bool, bool) { return true, true },
		scope:         workspaceScope{root: "/work/repo", home: "/home/me"},
	}
	assert.NotEmpty(t, Evaluate(deps, "grep -rn HandleRequest /work/repo/internal").Deny)
	assert.NotEmpty(t, Evaluate(deps, "grep -rn HandleRequest internal").Deny)
	assert.Empty(t, Evaluate(deps, "grep -rn HandleRequest /work/other").Deny)
	v := Evaluate(deps, "grep -rn HandleRequest ~/.claude/projects")
	assert.Empty(t, v.Deny)
	assert.Empty(t, v.Context, "nor is it advised")
}

// staleGraphTree is a workspace whose index vouches for HandleRequest, for the rules that
// deny in favor of a graph answer: a tree search, a diagnostic code, a declaration read
// through a context flag, and a translated heading search.
func staleGraphTree(t *testing.T) (root string, deps Dependencies) {
	t.Helper()
	root = writeTree(t, map[string]string{
		"internal/api/handler.go": "package api\n\n// HandleRequest serves one request.\nfunc HandleRequest() {}\n",
		"docs/guide.md":           "# Guide\n\n## Setup\n",
		"notes.txt":               "one\n",
	})
	return root, Dependencies{
		SymbolDefined: func(name string) (bool, bool) { return name == "HandleRequest", true },
		GraphIDs: graphOf(map[string][]string{
			types.KindDocSection: {"docsection:docs/guide.md#guide", "docsection:docs/guide.md#setup"},
			types.KindDiagnostic: {"diagnostic:MGS2011"},
		}),
		scope:   workspaceScope{root: root},
		callDir: root,
	}
}

var staleGraphCommands = []struct {
	command string
	rule    denyRuleName
}{
	{`grep -rn HandleRequest internal`, denyRuleSymbolSearch},
	{`grep -rn MGS2011 .`, denyRuleSymbolSearch},
	{`grep -n 'func HandleRequest' -A20 internal/api/handler.go`, denyRuleGrepReader},
	{`grep -n '^#' docs/guide.md`, denyRuleSearchTranslation},
}

// A graph-backed deny with the tree mid-rebase would send the reader from a grep over the
// real tree to an index describing another one, so each advises that the graph is stale
// and names the rebuild for after the rebase instead.
func TestGraphBackedDeniesAdviseWhileARebaseLeavesTheIndexStale(t *testing.T) {
	root, deps := staleGraphTree(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_EDITOR=true")
		out, err := cmd.CombinedOutput()
		if args[0] != "rebase" {
			require.NoError(t, err, "git %v: %s", args, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	for _, side := range []string{"topic", "main"} {
		if side == "topic" {
			git("checkout", "-q", "-b", "topic")
		} else {
			git("checkout", "-q", "main")
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte(side+"\n"), 0o644))
		git("commit", "-q", "-am", side)
	}
	git("checkout", "-q", "topic")
	git("rebase", "main")

	for _, tt := range staleGraphCommands {
		v := Evaluate(deps, tt.command)
		assert.Empty(t, v.Deny, tt.command)
		assert.Equal(t, advisoryGraphStale, v.Kind, tt.command)
		assert.Contains(t, v.Brief, "the graph is stale, a rebase is in progress.", tt.command)
		assert.Contains(t, v.Brief, "graph build` refreshes it once that is finished", tt.command)
	}
}

// An index built at another revision than HEAD advises in place of the translation and
// reader denies, and turns symbol-search's deny into one that serves the rebuild first;
// one built at HEAD, named by its full id or an abbreviation, denies as usual.
func TestGraphBackedDeniesAdviseOnAnIndexFromAnotherRevision(t *testing.T) {
	root, deps := staleGraphTree(t)
	cacheDir := t.TempDir()
	const built = "0123456789abcdef0123"
	require.NoError(t, knowledge.WriteGuardIndex(cacheDir, root, knowledge.NewGraph(), true, knowledge.GuardCheckout{Revision: built}))
	deps.CacheDir = func(string) (string, error) { return cacheDir, nil }

	for _, head := range []string{built, "0123456"} {
		deps.Revision = func(context.Context, string, string) string { return head }
		for _, tt := range staleGraphCommands {
			assert.Equal(t, tt.rule, Evaluate(deps, tt.command).Rule.Name, "%s at %s", tt.command, head)
		}
	}

	deps.Revision = func(context.Context, string, string) string { return "fedcba9" }
	for _, tt := range staleGraphCommands {
		v := Evaluate(deps, tt.command)
		if tt.rule == denyRuleSymbolSearch {
			assert.Equal(t, denyRuleSymbolSearch, v.Rule.Name, tt.command)
			assert.Contains(t, v.Deny, "graph build --silent`, then ", tt.command)
			assert.Contains(t, v.Deny, "The symbol index describes another tree (the index was built at 0123456789ab and the checkout is at fedcba9)", tt.command)
			continue
		}
		assert.Empty(t, v.Deny, tt.command)
		assert.Equal(t, advisoryGraphStale, v.Kind, tt.command)
		assert.Contains(t, v.Brief, "the graph is stale, the index was built at 0123456789ab and the checkout is at fedcba9.", tt.command)
		assert.NotContains(t, v.Brief, "once that is finished", tt.command)
	}
}

// A code search is denied for the codes the graph holds, not the ones this binary
// registers: mid-rebase the index may describe a tree without the code, or one the binary
// has never heard of.
func TestDiagnosticSearchFollowsTheGraph(t *testing.T) {
	const registered = "grep -rn MGS2011 docs/"
	withCode := graphOf(map[string][]string{types.KindDiagnostic: {"diagnostic:MGS2011", "diagnostic:MGS9901"}})
	assert.Equal(t, denyRule{Name: denyRuleSymbolSearch, Arg: "diagnostic:MGS2011"}, Evaluate(Dependencies{GraphIDs: withCode}, registered).Rule)
	assert.Equal(t, denyRule{Name: denyRuleSymbolSearch, Arg: "diagnostic:MGS9901"}, Evaluate(Dependencies{GraphIDs: withCode}, "grep -rn MGS9901 docs/").Rule)

	doubted := func(ctx context.Context, kind string) ([]string, bool) {
		ids, _ := withCode(ctx, kind)
		return ids, false
	}
	without := graphOf(map[string][]string{types.KindDiagnostic: {"diagnostic:MGS9901"}})
	for name, deps := range map[string]Dependencies{"no graph": {}, "a graph it doubts": {GraphIDs: doubted}, "another tree's": {GraphIDs: without}} {
		assert.Empty(t, Evaluate(deps, registered).Deny, name)
	}
}

// recordedSearchTree is a workspace laid out like this one, for the recorded commands: Go
// packages, a Markdown-only skills directory, CI config under a dot-directory.
func recordedSearchTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"internal/api/handler.go":                    "package api\n",
		"internal/cache/lock.go":                     "package cache\n",
		"internal/cache/eviction.go":                 "package cache\n",
		"internal/job/store.go":                      "package job\n",
		"internal/agent/skills/magus-query/SKILL.md": "# query\n",
		"cmd/magus/main.go":                          "package main\n",
		"types/job.go":                               "package types\n",
		"types/describe.go":                          "package types\n",
		"vcs/git.go":                                 "package vcs\n",
		"vcs/hg.go":                                  "package vcs\n",
		"std/http.go":                                "package std\n",
		"console/src/plan.ts":                        "export {}\n",
		"tools/pull-requests.buzz":                   "fun main() > void {}\n",
		"hack/split.buzz":                            "fun main() > void {}\n",
		"docs/conventions.md":                        "# conventions\n",
		".github/workflows/ci.yaml":                  "on: push\n",
		"node_modules/x/index.js":                    "module.exports = {}\n",
		"magusfile.buzz":                             "export fun spell_publish() > void {}\n",
	})
}

// TestSearchIntentOnRecordedCommands is the classification table, built from search calls
// recorded by the guard over the week before 2026-09-30 and classified by hand. Each row
// names what the call was reaching for and what the guard must do: refuse with the graph
// command for a symbol or a declaration, and let text, prose, a single-file read, stdin,
// another tree and a revision through. Precision first: every "text" row is a call the old
// rule passed and a careless one would refuse.
func TestSearchIntentOnRecordedCommands(t *testing.T) {
	root := recordedSearchTree(t)
	indexed := map[string]bool{}
	for _, name := range []string{
		"WithPreflight", "trivialRebase", "ChainStep", "UpdateRef", "CheckStatus", "newPendingSet", "canonicalPath",
		"covers", "Provider", "WriteFileAtomic", "CreateTemp", "Rename", "Mkdir", "worker", "read", "glob", "command",
		"NewGate", "OutputRecord", "CleanReport", "Member", "globalCfg", "Magus", "Append", "Tool",
		"KnowledgeRefsOutput", "Origin", "file_count", "field", "main", "WritePaths", "UnresolvedRisks",
		"Unattributed", "InstallRefreshHook", "DeclareMagusTypes", "AutoResolve", "RawMessage", "Marshal", "UnixMilli",
		"Inspect", "Walk",
	} {
		indexed[name] = true
	}
	deps := Dependencies{
		SymbolDefined: func(name string) (bool, bool) { return indexed[name], true },
		GraphIDs:      graphOf(map[string][]string{types.KindDiagnostic: {"diagnostic:MGS3010"}}),
		scope:         workspaceScope{root: root},
		callDir:       root,
	}

	for _, tt := range []struct {
		command string
		class   string // what the call reaches for
		arg     string // the names the deny routes to; "" lets it through
	}{
		// The evidence the job opened with.
		{`rg -n WithPreflight`, "symbol", "WithPreflight"},
		{`grep -rn "func trivialRebase" internal/queue`, "declaration", "trivialRebase"},
		{`grep -rli "rebind\|Origin\b\|CORS" --include=*.go internal/api | grep -v _test | head`, "text: -i widens a plain word", ""},
		{`grep -n -i "rebind\|origin\|cors" internal/api/handler.go internal/job/store.go | head -20`, "text: -i over plain words", ""},
		{`git grep -n 'coverage\.buzz' | head -30`, "text: a file name", ""},
		{`grep -rn "sibling-checkout" --include=*.go --include=*.buzz . | grep -v _test | head -8`, "text: hyphenated", ""},

		// Names and declarations the graph answers.
		{`grep -rn "type ChainStep\|type UpdateRef" internal/`, "declaration", "ChainStep,UpdateRef"},
		{`grep -rln "CheckStatus\b" internal/ std/ --include=*.buzz --include=*.go`, "symbol: word search", "CheckStatus"},
		{`grep -rn "newPendingSet\|\.covers(\|canonicalPath(" --include=*.go .`, "symbol; a lowercase call is text", "newPendingSet,canonicalPath"},
		{`grep -rn "Provider\b\|provider" cmd/magus/*.go`, "symbol beside a plain word", "Provider"},
		{`grep -rln 'WriteFileAtomic\|os.Rename\|CreateTemp\|flock' internal/cache internal/job/*.go`, "symbol; a stdlib member is text", "WriteFileAtomic,CreateTemp"},
		{`grep -rn 'hint.NewGate(' --include=*.go internal cmd`, "symbol: qualified call", "NewGate"},
		{`grep -rn "types.OutputRecord\|types\.CleanReport" --include=*.go .`, "symbol: qualified", "OutputRecord,CleanReport"},
		{`grep -rn "\.Member\b" --include=*.go internal/api cmd/magus`, "symbol: member", "Member"},
		{`grep -n "globalCfg = \|globalCfg, " cmd/magus/*.go`, "symbol: assignment", "globalCfg"},
		{`grep -n '^var Magus\b\|^var Magus =' std/*.go`, "declaration", "Magus"},
		{`grep -n "func Append\b\|func Append(" internal/job/*.go`, "declaration", "Append"},
		{`grep -rln 'type Tool struct' --include=*.go .`, "declaration", "Tool"},
		{`grep -n "Defs \|type KnowledgeRefsOutput" -A3 types/*.go`, "declaration beside text", "KnowledgeRefsOutput"},
		{`grep -rn 'unresolved_risks\|UnresolvedRisks\|forked_by\|Unattributed\b' --include=*.go types internal/job`, "symbols; snake_case is a JSON tag", "UnresolvedRisks,Unattributed"},
		{`grep -rn "WritePaths\|Heartbeat" --include=*.go internal/job/ types/`, "symbol beside a plain word", "WritePaths"},
		{`grep -rn 'InstallRefreshHook' types/*.go`, "symbol over a glob", "InstallRefreshHook"},
		{`grep -rln "func DeclareMagusTypes" internal/`, "declaration, files only", "DeclareMagusTypes"},
		{`grep -n 'MergeDriver\|AutoResolve' vcs/git.go vcs/hg.go`, "symbol over two files", "AutoResolve"},
		{`grep -n "RawMessage\|^func Marshal\|^import\|\"encoding/json\"" types/*.go`, "symbol and declaration beside text", "RawMessage,Marshal"},
		{`grep -rni 'WithPreflight' internal`, "symbol: CamelCase survives -i", "WithPreflight"},
		{`rg -n 'CheckStatus' -g '*.go'`, "symbol under a source filter", "CheckStatus"},
		{`grep -rn 'MGS3010' internal`, "diagnostic", "diagnostic:MGS3010"},

		// Text: no symbol index holds it.
		{`grep -rln "clientbuzz" --include=*.go .`, "text: a plain word", ""},
		{`grep -rn "package worker\|/worker\"" --include=*.go .`, "text", ""},
		{`grep -rn "\"origin\"\|Origin()" --include=*.go internal/api cmd/magus`, "text: a string literal and a full call", ""},
		{`grep -rln --include=*.go -E 'shell\.command|"thinking"|Transcript ' internal types std cmd`, "text: an event name and a string", ""},
		{`grep -rlw "worker" --include=*.go internal cmd`, "text: a lowercase word, even with -w", ""},
		{`grep -n '^func\|flock\|Flock\|O_EXCL\|Mkdir(' internal/cache/lock.go internal/cache/eviction.go`, "text: Mkdir( is os.Mkdir as often", ""},
		{`grep -rn "secret.read\|\"read\"" internal/api/*.go`, "text: a lowercase member", ""},
		{`grep -rn "ctx.glob(" --include=*.buzz .`, "text: a host method", ""},
		{`rg -n 'time.UnixMilli'`, "text: a standard-library member", ""},
		{`grep -rln 'SubagentStop\|SessionEnd' --include=*.go --include=*.buzz --include=*.md .`, "text: hook event names the index does not hold", ""},
		{`grep -n "Origin" types/*.go`, "text: a plain word over a glob", ""},
		{`grep -rn 'func main' cmd`, "text: a lowercase declaration many share", ""},
		{`grep -rn "object ChainStep" internal/`, "text: a Buzz declaration, which no index reads", ""},
		{`grep -rn 'CheckStatus' hack`, "text: a directory of Buzz", ""},
		{`grep -rn 'ast.Inspect\|ast.Walk' --include=*.go .`, "text: go/ast, not the workspace's Inspect", ""},
		{`grep -rni 'origin' internal`, "text", ""},

		// Not code: the graph has no answer there.
		{`grep -rn -E "match_count|file_count|duration_ms|blast_radius" internal/agent/skills`, "prose: a directory of Markdown", ""},
		{`grep -n "queue\|provider github\|--provider" .github/workflows/*.y*ml`, "config under a dot-directory", ""},
		{`git grep -n 'spell-publish\|spell_publish' -- .github/`, "config under a dot-directory", ""},
		{`git grep -n 'spell_publish\|publish_spells' -- '.github/workflows/*.yaml' magusfile.buzz`, "text: Buzz function names", ""},
		{`grep -rn "summary" docs --include=*.md`, "prose", ""},
		{`grep -n -A8 "missing-path" docs/conventions.md`, "prose", ""},
		{`rg -l 'Origin' -t md`, "prose filter", ""},
		{`grep -rn 'CheckStatus' node_modules`, "dependencies", ""},
		{`grep -rn 'CheckStatus' /tmp/other-repo`, "another tree", ""},
		{`git grep -l 'CheckStatus' HEAD -- internal/job`, "a revision", ""},
		{`echo CheckStatus | grep CheckStatus`, "stdin", ""},

		// One named file is a read.
		{`grep -n "type TargetGraphProject struct" types/describe.go`, "read", ""},
		{`grep -c 'CheckStatus' internal/job/store.go`, "read", ""},
		{`grep -n "^function field\|^function pathField" console/src/plan.ts`, "read", ""},
		{`grep -rn "fun main\|\"status\"" tools/pull-requests.buzz`, "read", ""},
	} {
		v := Evaluate(deps, tt.command)
		if tt.arg == "" {
			assert.NotEqual(t, denyRuleSymbolSearch, v.Rule.Name, "%q (%s) must run: %s", tt.command, tt.class, v.Deny)
			continue
		}
		require.Equal(t, denyRuleSymbolSearch, v.Rule.Name, "%q (%s): %s", tt.command, tt.class, v.Deny+v.Context)
		assert.Equal(t, tt.arg, v.Rule.Arg, "%q (%s)", tt.command, tt.class)
		assert.Contains(t, v.Deny, "Classified: ", "%q: the deny names what it classified", tt.command)
	}
}

// TestSymbolSearchOnAStaleIndex pins the rows the index state decides: a name the stale
// index holds and a declaration it has not reached yet are refused with the rebuild served
// first; a bare CamelCase word it does not hold is as often a string, and runs; with no
// index at all the search runs, advised.
func TestSymbolSearchOnAStaleIndex(t *testing.T) {
	root := recordedSearchTree(t)
	cacheDir := t.TempDir()
	require.NoError(t, knowledge.WriteGuardIndex(cacheDir, root, knowledge.NewGraph(), true, knowledge.GuardCheckout{}))
	held := map[string]bool{"CheckStatus": true}
	stale := Dependencies{
		SymbolDefined: func(name string) (bool, bool) { return held[name], false },
		CacheDir:      func(string) (string, error) { return cacheDir, nil },
		scope:         workspaceScope{root: root},
		callDir:       root,
	}
	cold := stale
	cold.CacheDir = func(string) (string, error) { return t.TempDir(), nil }

	for _, tt := range []struct {
		command string
		deps    Dependencies
		arg     string // "" lets it through
		advised bool
	}{
		{`grep -rn CheckStatus internal`, stale, "CheckStatus", false},
		{`grep -rn 'func settleTarget' cmd/magus`, stale, "settleTarget", false},
		{`grep -rn 'workspaceOutputGlobs(' cmd/magus`, stale, "workspaceOutputGlobs", false},
		{`grep -rn 'SubagentStop' internal`, stale, "", false},
		{`grep -rn 'func settleTarget' cmd/magus`, cold, "", true},
	} {
		v := Evaluate(tt.deps, tt.command)
		if tt.arg == "" {
			assert.Empty(t, v.Deny, tt.command)
			assert.Equal(t, tt.advised, v.Kind == advisoryPrecedent, tt.command)
			continue
		}
		require.Equal(t, denyRuleSymbolSearch, v.Rule.Name, "%q: %s", tt.command, v.Context)
		assert.Equal(t, tt.arg, v.Rule.Arg, tt.command)
		require.NotEmpty(t, v.Next, tt.command)
		assert.Equal(t, hint.GraphBuild.With("--silent"), v.Next[0].Run, "%q: the rebuild comes first", tt.command)
	}
}

// TestMixedSearchServesTheTextOnItsOwn pins the half of a mixed alternation that is not a
// symbol: a literal is served as magus's own literal search over the same paths.
func TestMixedSearchServesTheTextOnItsOwn(t *testing.T) {
	root := recordedSearchTree(t)
	deps := Dependencies{
		SymbolDefined: func(name string) (bool, bool) { return name == "WritePaths", true },
		scope:         workspaceScope{root: root},
		callDir:       root,
	}
	v := Evaluate(deps, `grep -rn 'WritePaths\|Heartbeat' --include=*.go internal/job`)
	require.Equal(t, denyRuleSymbolSearch, v.Rule.Name)
	assert.Contains(t, v.Deny, "The text alternatives (`Heartbeat`) are not symbols: search them on their own.")
	runs := make([]string, len(v.Next))
	for i, n := range v.Next {
		runs[i] = n.Run
	}
	assert.Contains(t, runs, hint.Refs.With("WritePaths", "--occurrences"))
	assert.Contains(t, runs, hint.BinaryName()+" refs --text Heartbeat internal/job")
}
