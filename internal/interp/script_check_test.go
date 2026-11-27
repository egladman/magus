package interp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// checkRunner installs no host: the fixtures import only plain modules.
func checkRunner() *ScriptRunner {
	return &ScriptRunner{
		Phase: func(string) func() { return func() {} },
		Host: ScriptHost{
			RegisterModules:        func(context.Context, *buzz.Session, io.Writer) {},
			RegisterSpellDecls:     func(*buzz.Session) {},
			RegisterMagusNamespace: func(context.Context, *buzz.Session) {},
			DeclareMagusTypes:      func(*buzz.Session) error { return nil },
			RegisterSpellImports:   func(*buzz.Session) {},
		},
	}
}

// writeProjectFixture builds a root with a magusfile, hack/lib.buzz, and
// docs/x.buzz importing hack/lib by its project-relative path.
func writeProjectFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"magusfile.buzz": "",
		"hack/lib.buzz":  "export fun answer() > int { return 42; }\n",
		"docs/x.buzz":    "import \"hack/lib\";\n",
	} {
		full := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	return root
}

func TestCheckFileResolvesProjectRelativeImportFromRoot(t *testing.T) {
	root := writeProjectFixture(t)
	t.Chdir(root)

	diags, err := checkRunner().CheckFile(t.Context(), "docs/x.buzz", false)
	require.NoError(t, err)
	assert.Empty(t, diags)
}

func TestCheckFileResolvesProjectRelativeImportFromSubdirectory(t *testing.T) {
	root := writeProjectFixture(t)
	t.Chdir(filepath.Join(root, "docs"))

	diags, err := checkRunner().CheckFile(t.Context(), "x.buzz", false)
	require.NoError(t, err)
	assert.Empty(t, diags)
}

// A checker error carries the code in Code; a resolution failure renders it into Msg.
// Either names the handle, so the note can list the spells that resolve.
func TestUnresolvedSpellHandleReadsEitherPosition(t *testing.T) {
	handle, ok := unresolvedSpellHandle(buzz.Diagnostic{Code: unresolvedImportCode, Msg: `cannot resolve import "magus/spell/gooo"`})
	require.True(t, ok)
	assert.Equal(t, "gooo", handle)

	handle, ok = unresolvedSpellHandle(buzz.Diagnostic{Msg: unresolvedImportCode + `: cannot resolve import "magus/spell/gooo"`})
	require.True(t, ok)
	assert.Equal(t, "gooo", handle)

	_, ok = unresolvedSpellHandle(buzz.Diagnostic{Code: unresolvedImportCode, Msg: `cannot resolve import "lib"`})
	assert.False(t, ok, "only a spell import names a handle")
}

func TestCheckFileResolvesImportFromWorkspaceRootPastANestedMagusfile(t *testing.T) {
	root := writeProjectFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", "magusfile.buzz"), nil, 0o644))
	t.Chdir(filepath.Join(root, "docs"))

	diags, err := checkRunner().CheckFile(t.Context(), "x.buzz", false)
	require.NoError(t, err)
	assert.Empty(t, diags)
}
