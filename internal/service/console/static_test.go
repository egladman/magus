package console

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func consoleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte("<html><head>\n</head><body>shell</body></html>"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "console.css"), []byte(".a{}"), 0o600))
	return dir
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// The shell is served with <base href="../">, which resolves to /console/ ONLY when the URL
// already ends in a slash. Served at a bare /console/diff, every asset resolves one level too
// high - console.css, theme.js and patternfly.css all 404 at the site root - so the surface
// renders unstyled and never boots. Canonicalize instead.
func TestSurfaceRouteWithoutTrailingSlashRedirects(t *testing.T) {
	h := StaticHandler(consoleDir(t))

	for _, surface := range KnownSurfaces {
		w := get(t, h, "/console/"+surface)
		assert.Equal(t, http.StatusFound, w.Code, "%s must canonicalize", surface)
		assert.Equal(t, "/console/"+surface+"/", w.Header().Get("Location"), surface)
	}
}

func TestSurfaceRouteRedirectKeepsTheQuery(t *testing.T) {
	h := StaticHandler(consoleDir(t))
	w := get(t, h, "/console/diff?scope=a.go&x=1")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/console/diff/?scope=a.go&x=1", w.Header().Get("Location"))
}

// The canonical form Link mints must be served directly - a redirect loop here would take the
// whole console down.
func TestCanonicalSurfaceRouteServesTheShell(t *testing.T) {
	h := StaticHandler(consoleDir(t))

	w := get(t, h, "/console/diff/")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "shell")
	assert.Contains(t, w.Body.String(), `<base href="../">`,
		"the relative base is what lets the shell be served from a prefix it does not know")
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
}

// A real file must never be mistaken for a surface, in either direction.
func TestAssetsAreStillServed(t *testing.T) {
	h := StaticHandler(consoleDir(t))

	w := get(t, h, "/console/console.css")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), ".a{}")

	// A sub-path under a surface segment is a file request, not a route.
	assert.Equal(t, http.StatusNotFound, get(t, h, "/console/diff/diff.js").Code)
}

func TestUnknownSegmentIsNotASurfaceRoute(t *testing.T) {
	h := StaticHandler(consoleDir(t))
	w := get(t, h, "/console/not-a-surface")
	assert.NotEqual(t, http.StatusFound, w.Code, "only a known surface canonicalizes")
}

// KnownSurfaces is the contract the daemon, the link minters, and the console's boot router
// all read. A surface added to the console without being added here is deep-linkable in
// exactly one direction, which is the kind of gap nobody notices until someone shares a URL.
func TestKnownSurfacesCoversTheDiffSurface(t *testing.T) {
	assert.True(t, IsSurfaceRoute("diff"))
	assert.False(t, IsSurfaceRoute("review"), "the surface was renamed; the old segment is gone")
	assert.False(t, IsSurfaceRoute(""), "the console root is not a surface route")
	assert.False(t, IsSurfaceRoute("diff/diff.js"), "a sub-path is a file, not a route")
	for _, s := range KnownSurfaces {
		assert.False(t, strings.Contains(s, "/"), "a surface segment is one path element: %q", s)
	}
}
