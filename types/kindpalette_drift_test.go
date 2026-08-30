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
	"fmt"
	"math"
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
		KindMethod, KindDiagnostic, KindDoc, KindDocSection, KindFile, KindDir, KindFunction, KindImport,
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
	shapesTS := read("console", "src", "console", "graph", "shapes.ts")

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
			// Shape is the second channel color cannot carry (SC 1.4.1). A kind missing here
			// falls back to a circle, which silently files it with the Code family.
			require.Contains(t, shapesTS, "  "+k+": \"",
				"shapes.ts SHAPE_BY_KIND omits %s; it would draw as an unexplained circle", k)
		})
	}

	// The dark override is a separate block that has gone stale on its own before, so every
	// kind must appear TWICE in tokens.css: once light, once under .pf-v6-theme-dark.
	for _, k := range kinds {
		require.Equal(t, 2, strings.Count(tokens, "--console-node-"+k+":"),
			"tokens.css must define --console-node-%s in both the light and dark blocks", k)
	}

	t.Run("contrast", func(t *testing.T) { requireNodeContrast(t, tokens, kinds) })
}

// requireNodeContrast measures every node color against the canvas it is painted on, which is the
// check the structural ones above cannot make: "the token is defined" was true of a palette where
// 18 of 20 kinds sat below 3:1 on white and owner was 1.72:1 on the dark ground.
//
// The floor is WCAG 2.1 SC 1.4.11, 3:1 for a graphical object you must see to understand the
// content - which a node whose color IS its kind plainly is.
func requireNodeContrast(t *testing.T, tokens string, kinds []string) {
	t.Helper()

	const darkSelector = ":root.pf-v6-theme-dark {"
	split := strings.Index(tokens, darkSelector)
	require.Greater(t, split, 0, "tokens.css no longer has a %s block to read the dark palette from", darkSelector)

	for _, theme := range []struct {
		name   string
		css    string
		ground string
	}{
		{"light", tokens[:split], "#ffffff"},
		{"dark", tokens[split:], "#292929"},
	} {
		for _, k := range kinds {
			t.Run(theme.name+"/"+k, func(t *testing.T) {
				hex, ok := nodeTokenValue(theme.css, k)
				require.True(t, ok,
					"no literal hex for --console-node-%s in the %s block; the palette is written as hex "+
						"so this gate can measure it", k, theme.name)
				ratio := contrastRatio(hex, theme.ground)
				require.GreaterOrEqualf(t, ratio, 3.0,
					"--console-node-%s is %s on the %s canvas (%s): %.2f:1, below the 3:1 floor of WCAG "+
						"SC 1.4.11. Re-derive it at a lightness that clears the ground.",
					k, hex, theme.name, theme.ground, ratio)
			})
		}
	}
}

// nodeTokenValue pulls the hex literal off `--console-node-<kind>: #rrggbb;`.
func nodeTokenValue(css, kind string) (string, bool) {
	i := strings.Index(css, "--console-node-"+kind+":")
	if i < 0 {
		return "", false
	}
	rest := css[i:]
	h := strings.Index(rest, "#")
	if h < 0 || h > 40 { // the value is on the same line; a distant # is the next rule's
		return "", false
	}
	if len(rest) < h+7 {
		return "", false
	}
	return rest[h : h+7], true
}

// contrastRatio is the WCAG 2.1 ratio between two sRGB hex colors.
func contrastRatio(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(hex string) float64 {
	ch := func(off int) float64 {
		var v int
		_, err := fmt.Sscanf(hex[off:off+2], "%02x", &v)
		if err != nil {
			return 0
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}
