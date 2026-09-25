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
		// Reading one file is not a search of the tree.
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
