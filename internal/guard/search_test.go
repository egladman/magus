package guard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
// indexed name is denied with the lines it would have printed, and anything the index
// does not cover, or a flag that changes the question, is left alone.
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
		arg     string // "" for no deny
		answer  string
	}{
		{`grep -n HandleRequest internal/api/handler.go`, deps, "HandleRequest",
			"What this search selects (3 results):\n  internal/api/handler.go:3:// HandleRequest serves one request.\n  internal/api/handler.go:4:func HandleRequest() {}\n  internal/api/handler.go:6:func serve() { HandleRequest() }"},
		{`grep -n 'HandleRequest\|ParseConfig' internal/api/handler.go internal/api/config.go`, deps, "HandleRequest,ParseConfig",
			"  internal/api/config.go:3:func ParseConfig() {}"},
		{`rg -n 'HandleRequest\(' internal/api/handler.go`, deps, "HandleRequest", "  internal/api/handler.go:6:func serve() { HandleRequest() }"},
		{`grep -n 'func HandleRequest' internal/api/handler.go`, deps, "HandleRequest", "refs HandleRequest --definition --source"},
		{`grep -n ParseConfig internal/api/handler.go`, deps, "ParseConfig", "What this search selects: nothing."},

		// A stale index proves nothing.
		{`grep -n HandleRequest internal/api/handler.go`, stale, "", ""},
		// Text, and a name the index does not hold.
		{`grep -n serve internal/api/handler.go`, deps, "", ""},
		{`grep -n 'HandleRequest\|serve' internal/api/handler.go`, deps, "", ""},
		// Prose is not what the symbol index covers.
		{`grep -n HandleRequest docs/handler.md`, deps, "", ""},
		{`grep -n HandleRequest internal/api/handler.go docs/handler.md`, deps, "", ""},
		// Context, count, list, invert and case flags ask a different question.
		{`grep -n -B2 HandleRequest internal/api/handler.go`, deps, "", ""},
		{`grep -c HandleRequest internal/api/handler.go`, deps, "", ""},
		{`grep -l HandleRequest internal/api/handler.go`, deps, "", ""},
		{`grep -v HandleRequest internal/api/handler.go`, deps, "", ""},
		{`grep -in handlerequest internal/api/handler.go`, deps, "", ""},
		// A glob or a missing file cannot be read.
		{`grep -n HandleRequest internal/api/*.go`, deps, "", ""},
		{`grep -n HandleRequest internal/api/missing.go`, deps, "", ""},
		// A pipe is not a file.
		{`cat internal/api/handler.go | grep -n HandleRequest`, deps, "", ""},
	} {
		v, ok := searchVerdictAt(tt.deps, root, parseForTest(t, tt.command))
		if tt.arg == "" {
			assert.False(t, ok && v.Deny != "", "%q must not deny: %s", tt.command, v.Deny)
			continue
		}
		assert.Equal(t, denyRule{Name: denyRuleSymbolSearch, Arg: tt.arg}, v.Rule, tt.command)
		assert.Contains(t, v.Deny, tt.answer, tt.command)
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
