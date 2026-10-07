package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestNativeSearchLine(t *testing.T) {
	for _, tt := range []struct {
		input map[string]any
		want  string
	}{
		{map[string]any{"pattern": "HandleRequest"}, "rg -l -e HandleRequest"},
		{map[string]any{"pattern": "HandleRequest", "path": "internal", "output_mode": "content", "-i": true, "-A": 3.0}, "rg -n -i -A 3 -e HandleRequest internal"},
		{map[string]any{"pattern": `func \(s \*Store\) Get`, "glob": "*.go", "output_mode": "count"}, `rg -c -g '*.go' -e 'func \(s \*Store\) Get'`},
		{map[string]any{"pattern": "it's", "type": "go"}, `rg -l -t go -e 'it'\''s'`},
		{map[string]any{"pattern": "**/*.go"}, "find . -type f -name '*.go'"},
		{map[string]any{"pattern": "spells/**/spell.buzz", "path": "/w"}, "find /w/spells -type f -name spell.buzz"},
		{map[string]any{"pattern": "*.md", "path": "docs"}, "find docs -maxdepth 1 -type f -name '*.md'"},
		{map[string]any{"pattern": "internal/guard/*.go"}, "find internal/guard -maxdepth 1 -type f -name '*.go'"},
		{map[string]any{"pattern": "src/**/test/*.ts"}, "find . -path 'src/**/test/*.ts'"},
		// A regex using `.*` is a content search; a leading `*` is only ever a glob.
		{map[string]any{"pattern": "func.*Summary"}, "rg -l -e 'func.*Summary'"},
		{map[string]any{"content": "no pattern"}, ""},
	} {
		assert.Equal(t, tt.want, nativeSearchLine(tt.input), "%v", tt.input)
	}
}

// TestJudgeRoutesNativeSearchTools feeds the host's own content and file search tools
// through Judge as a PreToolUse payload arrives, and pins that each reaches the rule its
// shell spelling would.
func TestJudgeRoutesNativeSearchTools(t *testing.T) {
	testkit.Isolate(t)
	root := writeTree(t, map[string]string{
		"internal/api/handler.go":     "package api\n\nfunc HandleRequest() {}\n",
		"internal/api/gen/handler.go": "package gen\n",
	})
	deps := strict(testDependencies())
	deps.SymbolDefined = func(name string) (bool, bool) { return name == "HandleRequest", true }
	deps.GraphIDs = graphOf(map[string][]string{types.KindFile: {"file:internal/api/handler.go", "file:internal/api/gen/handler.go"}})
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: root})
	envelope := func(tool string, input map[string]any) string {
		raw, err := json.Marshal(map[string]any{
			"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": tool, "cwd": root, "tool_input": input,
		})
		require.NoError(t, err)
		return string(raw)
	}

	grep := Judge(ctx, deps, Request{Input: envelope("Grep", map[string]any{"pattern": "HandleRequest", "path": "internal", "output_mode": "content"})})
	grepShape := verdictWithRule("deny", string(denyRuleSymbolSearch))
	grepShape.Next = grep.Next // the served remedy is the search rule's own, tested there
	assert.Equal(t, grepShape, unworded(grep))
	assert.Contains(t, grep.Reason, "refs HandleRequest --occurrences")

	glob := Judge(ctx, deps, Request{Input: envelope("Glob", map[string]any{"pattern": "**/handler.go", "path": "internal/api"})})
	globShape := verdictWithRule("deny", string(denyRuleSearchTranslation))
	globShape.Next = glob.Next
	assert.Equal(t, globShape, unworded(glob))
	assert.Contains(t, glob.Reason, "file:internal/api/gen/handler.go")

	text := Judge(ctx, deps, Request{Input: envelope("Grep", map[string]any{"pattern": "serves one request", "path": "internal"})})
	assert.NotEqual(t, "deny", text.Decision, text.Reason)
}
