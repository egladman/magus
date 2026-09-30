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
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestSymbolSearchDeniesEveryProvableShape widens the deny past one bare identifier: an
// alternation, a definition lookup, and a diagnostic code, each only when the graph can
// answer EVERY name the pattern looks for. The 2026-09-24 audit measured 45% of search
// patterns as alternations and 13% as definition lookups, and the single-name deny fired
// 0 times.
func TestSymbolSearchDeniesEveryProvableShape(t *testing.T) {
	indexed := map[string]bool{"HandleRequest": true, "ParseConfig": true, "Judge": true, "Verdict": true}
	deps := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], true }}
	stale := Dependencies{SymbolDefined: func(name string) (bool, bool) { return indexed[name], false }}

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

		// One alternative the index does not hold is text grep may be right about.
		{`grep -rn 'HandleRequest\|someText' .`, ""},
		{`grep -rn 'HandleRequest\|NotIndexedHere' .`, ""},
		// Under BRE a bare `|` is a literal, so this looks for one string, not two names.
		{`grep -rn 'HandleRequest|ParseConfig' .`, ""},
		// Fixed strings have no alternation at all.
		{`grep -rnF 'HandleRequest\|ParseConfig' .`, ""},
		// A case-insensitive search asks a wider question than refs answers.
		{`grep -rni 'HandleRequest\|ParseConfig' .`, ""},
		{`grep -rn 'func Unknown' .`, ""},
		// A lookup the index cannot vouch for stays advice.
		{`grep -rn 'func Judge' internal/`, "stale"},
		// Another tree is not this workspace's graph.
		{`grep -rn 'func Judge' /tmp/other-repo`, ""},
		// With no workspace root a named file cannot be placed, so it is left alone.
		{`grep -n 'func Judge' internal/guard/guard.go`, ""},
	} {
		d := deps
		want := tt.arg
		if tt.arg == "stale" {
			d, want = stale, ""
		}
		v := Evaluate(d, tt.command)
		if want == "" {
			assert.Empty(t, v.Deny, "%q must not deny", tt.command)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleSymbolSearch, Arg: want}, v.Rule, tt.command)
		for _, name := range strings.Split(want, ",") {
			assert.Contains(t, v.Deny, name, "%q: one command per name", tt.command)
		}
	}
}

// TestSymbolSearchOnNamedFiles pins the file half: a search of named Go files for an
// indexed name runs with refs advised, and anything the index does not cover, or a flag
// that changes the question, gets no such advice.
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
		{`grep -n 'HandleRequest\|ParseConfig' internal/api/handler.go internal/api/config.go`, deps,
			"refs HandleRequest --occurrences`, `magus refs ParseConfig --occurrences` answer this for every file."},
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
		// A glob or a missing file cannot be read.
		{`grep -n HandleRequest internal/api/*.go`, deps, ""},
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
		// A search no parser models, or an index with no sites, keeps the routing deny alone.
		{`grep -rl HandleRequest internal`, deps, "refs HandleRequest --occurrences", "", false},
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
		GraphIDs:      graphOf(map[string][]string{types.KindDocSection: {"docsection:docs/guide.md#guide", "docsection:docs/guide.md#setup"}}),
		scope:         workspaceScope{root: root},
		callDir:       root,
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
		assert.Contains(t, v.Brief, "the graph is stale, a merge, rebase or cherry-pick is in progress.", tt.command)
		assert.Contains(t, v.Brief, "graph build` refreshes it once that is finished", tt.command)
	}
}

// An index built at another revision than HEAD advises in place of every graph-backed
// deny; one built at HEAD, named by its full id or an abbreviation, still denies.
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
		assert.Empty(t, v.Deny, tt.command)
		assert.Equal(t, advisoryGraphStale, v.Kind, tt.command)
		assert.Contains(t, v.Brief, "the graph is stale, the index was built at 0123456789ab and the checkout is at fedcba9.", tt.command)
		assert.NotContains(t, v.Brief, "once that is finished", tt.command)
	}
}
