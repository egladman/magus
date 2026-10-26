package buzz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImport_AliasKeepsAnExportThatShadowsTheImporter covers an aliased module
// exporting a name the importer already bound. The parent globals are copied into
// the module's sub-session so it can see host APIs, and the alias map used to skip
// every one of those names, so the module's own export was missing and the
// qualified call read null. A second alias of the same file reads the cached map.
func TestImport_AliasKeepsAnExportThatShadowsTheImporter(t *testing.T) {
	dir := writeTxtar(t, `
-- cursor.buzz --
export fun id() > str { return "spell"; }
-- lib.buzz --
export fun cursor(n: int) > int { return n + 1; }
export fun other(n: int) > int { return n + 2; }
`)

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	require.NoError(t, sess.Exec(ctx, `
import "cursor";
import "lib" as lib;
import "lib" as again;
final fromLib = lib\cursor(1);
final fromAgain = again\cursor(1);
final other = lib\other(1);
final spell = cursor\id();
`))
	globals := sess.Globals()
	for name, want := range map[string]string{
		"fromLib":   "2",
		"fromAgain": "2",
		"other":     "3",
		"spell":     "spell",
	} {
		got, ok := globals[name]
		require.True(t, ok, "%s not set", name)
		assert.Equal(t, want, got.String(), name)
	}
}
