package selfupdate

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseTrustAnchorMatchesInstallerAndCI(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	wantHex := hex.EncodeToString(PubKey)

	installer, err := os.ReadFile(filepath.Join(root, "docs", "gen", "install"))
	require.NoError(t, err)
	action, err := os.ReadFile(filepath.Join(root, ".github", "actions", "setup-magus", "action.yml"))
	require.NoError(t, err)
	guide, err := os.ReadFile(filepath.Join(root, "docs", "guides", "download.md"))
	require.NoError(t, err)

	assert.Equal(t, 1, strings.Count(string(installer), wantHex), "installer must embed the binary's release key once")
	assert.Equal(t, 1, strings.Count(string(action), wantHex), "setup action must use the binary's release key once")
	assert.Contains(t, string(guide), "/7uPpvNidN79EoiAk8ajIsJTK8VFAW9JWrSVXey2Z3k=", "download guide must publish the binary's release key")
}
