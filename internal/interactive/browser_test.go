package interactive

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenViaBrowserEnv exercises the $BROWSER convention parsing: unset falls through
// (error), a bogus command errors, and a real command (with or without %s) launches.
func TestOpenViaBrowserEnv(t *testing.T) {
	t.Setenv("BROWSER", "")
	assert.Error(t, OpenViaBrowserEnv("http://x"), "unset BROWSER falls through to the platform opener")

	t.Setenv("BROWSER", "magus-no-such-browser-xyz")
	assert.Error(t, OpenViaBrowserEnv("http://x"), "a command that cannot start errors")

	// `true` exists on the PATH of every supported unix; it launches (Start succeeds),
	// which is all OpenViaBrowserEnv needs to consider the browser opened.
	t.Setenv("BROWSER", "true")
	require.NoError(t, OpenViaBrowserEnv("http://x"))

	t.Setenv("BROWSER", "true %s")
	require.NoError(t, OpenViaBrowserEnv("http://x"), "the URL is substituted for %s")

	// The first launchable entry wins even if an earlier one is missing.
	t.Setenv("BROWSER", "magus-no-such-browser-xyz:true")
	require.NoError(t, OpenViaBrowserEnv("http://x"))
}

func TestSafeBrowserURLOpensOnlyHTTPWithAHost(t *testing.T) {
	got, err := SafeBrowserURL(" https://example.test/a#b ")
	require.NoError(t, err)
	assert.Equal(t, "https://example.test/a#b", got)

	for _, raw := range []string{"-flag", "file:///etc/passwd", "https:///nohost"} {
		_, err := SafeBrowserURL(raw)
		assert.Error(t, err, raw)
	}
}
