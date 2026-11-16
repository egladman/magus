package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// UnknownVersion's doc says to keep it in sync with proc.devVersionSentinel (see
// TestDevVersionSentinelMatchesWard in internal/proc). This package can see proc's package
// but not its unexported sentinel, so it proves its half through the same shared anchor,
// ward.DevVersion, rather than comparing the two directly.
func TestUnknownVersionMatchesWardDevVersion(t *testing.T) {
	assert.Equal(t, ward.DevVersion, UnknownVersion)
}

// The digest is what diagrams-generate keys on, so it must name the figure.buzz this
// binary was built from.
func TestVersionOutputDigestsTheEmbeddedFigureSource(t *testing.T) {
	disk, err := os.ReadFile(filepath.Join("..", "..", "libs", "figure", "figure.buzz"))
	require.NoError(t, err)
	sum := sha256.Sum256(disk)
	assert.Equal(t, hex.EncodeToString(sum[:]), NewVersionOutput(types.MagusBuild{}, "").Embedded.FigureSHA256)
}

func TestServerLine(t *testing.T) {
	cases := []struct {
		name   string
		probe  ServerProbe
		client string
		want   string
	}{
		{"no server answered", ServerProbe{}, "v1.2.3", "server: not running"},
		{"same build on both ends", ServerProbe{Version: "v1.2.3"}, "v1.2.3", "server: v1.2.3"},
		{
			"a server left running across an upgrade",
			ServerProbe{Version: "v1.2.0"}, "v1.2.3",
			"server: v1.2.0 (differs from this client)",
		},
		{
			// The case a plain comparison got wrong: an unstamped client reports the same
			// sentinel, so "server: unknown" read as two matching builds.
			"a server that did not report a version, against an unstamped client",
			ServerProbe{Version: UnknownVersion}, UnknownVersion,
			"server: running, version not reported",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ServerLine(c.probe, c.client))
		})
	}
}

// A source lookup fetches by commit, so a build whose tree was modified must say so:
// that commit is not the code it runs.
func TestBuiltDirty(t *testing.T) {
	assert.True(t, BuiltDirty(types.MagusBuild{Version: "v0.5.0-rc.2-2-gd32c55d67-dirty"}))
	assert.True(t, BuiltDirty(types.MagusBuild{Version: "v0.5.0-dirty"}))
	assert.False(t, BuiltDirty(types.MagusBuild{Version: "v0.5.0"}))
	assert.False(t, BuiltDirty(types.MagusBuild{Version: "v0.5.0-rc.2-2-gd32c55d67"}))
}
