package dry

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dry compiles to js/wasm for the playground, so it must never reach the host-only
// engine: one transitive import of these breaks that build.
func TestDryReachesNoHostOnlyPackage(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.Output()
	require.NoError(t, err, "go list -deps")

	deps := strings.Fields(string(out))
	require.Contains(t, deps, "github.com/egladman/magus/internal/dry")
	// TODO: ban internal/cache too once internal/spell stops reaching it through
	// internal/observability.
	for _, banned := range []string{
		"github.com/egladman/magus",
		"github.com/egladman/magus/internal/proc",
	} {
		assert.NotContains(t, deps, banned)
	}
}
