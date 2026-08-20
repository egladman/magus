package types

// TestNodeKindPaletteDrift locks the browser's node-kind palette to the kinds declared here.
//
// The Graph Explorer colors every node by kind, and a kind with no entry falls through to a flat
// #888. That failure is SILENT and it compounds: the legend is built from the kinds present in
// the graph that happens to be loaded, so a kind the demo does not emit is invisible until a
// workspace that does emit it shows up. The palette drifted to 14 entries against 17 emitted
// kinds that way, and dir/package/tool/note drew as one anonymous grey mass a quarter of the
// graph wide - nothing was red, and the graph just looked washed out.
//
// Both console files are checked because they fail differently: tokens.css missing an entry
// means no color exists, graph.css missing the alias means main.ts reads an undefined property
// and gets the fallback anyway. The KINDS array is checked because it fixes legend order.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNodeKindPaletteDrift(t *testing.T) {
	// Every kind the graph builder can emit. Add a Kind* constant above, add it here, and the
	// three console sites below will tell you they are missing it.
	kinds := []string{
		KindProject, KindTarget, KindSpell, KindOp, KindTool, KindCharm, KindModule,
		KindMethod, KindDiagnostic, KindDoc, KindFile, KindDir, KindFunction, KindImport,
		KindRationale, KindOwner, KindSymbol, KindAuthor, KindNote, KindPackage,
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..")

	read := func(parts ...string) string {
		p := filepath.Join(append([]string{repoRoot}, parts...)...)
		data, err := os.ReadFile(p)
		require.NoError(t, err, "could not read %s", p)
		return string(data)
	}

	tokens := read("console", "src", "styles", "tokens.css")
	graphCSS := read("console", "src", "console", "graph", "graph.css")
	mainTS := read("console", "src", "console", "graph", "main.ts")

	// KINDS is a plain array literal; slice it out so a kind named in a comment elsewhere in
	// the file cannot satisfy the check.
	start := strings.Index(mainTS, "const KINDS = [")
	require.GreaterOrEqual(t, start, 0, "main.ts no longer declares a KINDS array")
	end := strings.Index(mainTS[start:], "];")
	require.Greater(t, end, 0, "main.ts KINDS array is not terminated")
	kindsArray := mainTS[start : start+end]

	for _, k := range kinds {
		t.Run(k, func(t *testing.T) {
			require.Contains(t, tokens, "--console-node-"+k+":",
				"tokens.css has no --console-node-%s; the kind would paint as the #888 fallback", k)
			require.Contains(t, graphCSS, "--gk-"+k+": var(--console-node-"+k+")",
				"graph.css does not alias --gk-%s; main.ts reads the alias, not the token", k)
			require.Contains(t, kindsArray, `"`+k+`"`,
				"main.ts KINDS omits %q; the legend would not list it", k)
		})
	}

	// The dark override is a separate block that has gone stale on its own before, so every
	// kind must appear TWICE in tokens.css: once light, once under .pf-v6-theme-dark.
	for _, k := range kinds {
		require.Equal(t, 2, strings.Count(tokens, "--console-node-"+k+":"),
			"tokens.css must define --console-node-%s in both the light and dark blocks", k)
	}
}
