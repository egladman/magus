package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestBriefFormatNamesTheFormatAndItsSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()

	got := (&runner{root: root}).checkBriefFormat(nil)
	assert.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "markdown, agents.brief_format unset")

	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("agents:\n  brief_format: buzz\n"), 0o644))
	got = (&runner{root: root}).checkBriefFormat(nil)
	assert.Contains(t, got.Message, "buzz, from magus.yaml")

	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("agents:\n  brief_format: json\n"), 0o644))
	got = (&runner{root: root}).checkBriefFormat(nil)
	assert.Equal(t, types.CheckFail, got.Status, "an unknown format is refused, never read as markdown")
}
