package console

import (
	"io/fs"
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
// high (console.css, theme.js and patternfly.css all 404 at the site root), so the surface
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

// The canonical form Link mints must be served directly: a redirect loop here would take the
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

// KnownSurfaces is the contract the server, the link minters, and the console's boot router
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

// The redirect target is assembled from the allow-listed segment, so a request that reaches it
// by an odd-but-legal path lands on the canonical one rather than echoing what was asked for.
func TestRedirectNormalizesAndCannotEchoTheRequestPath(t *testing.T) {
	h := StaticHandler(consoleDir(t))
	w := get(t, h, "/console//diff")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/console/diff/", w.Header().Get("Location"))
}

// The handler is unauthenticated and the console dir is not all shell: the build copies the
// hosted demo's graph JSON (a whole workspace's knowledge graph, notes included) in beside it.
func TestStaticHandlerServesOnlyTheShell(t *testing.T) {
	dir := consoleDir(t)
	for name, body := range map[string]string{
		"sw.js":                         "self",
		"manifest.webmanifest":          "{}",
		"assets/icon.svg":               "<svg/>",
		"assets/icon-192.png":           "png",
		"graph/explorer.js":             "js",
		"graph/scaffold.html":           "<html></html>",
		"graph/knowledge-graph.json":    `{"nodes":[{"kind":"note"}]}`,
		"graph/target-graph.json":       `{"projects":[]}`,
		"console.js.map":                "{}",
		".env":                          "SECRET=1",
		"nested/.hidden/leak.js":        "js",
		"listing/only-a-data-file.json": "{}",
		"help/index.html":               "<html></html>",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	h := StaticHandler(dir)

	for _, p := range []string{
		"/console/", "/console/console.css", "/console/sw.js", "/console/manifest.webmanifest",
		"/console/assets/icon.svg", "/console/assets/icon-192.png",
		"/console/graph/explorer.js", "/console/graph/scaffold.html", "/console/graph/",
		// Not a surface route: a directory holding an index.html serves that page.
		"/console/help/",
	} {
		assert.Equal(t, http.StatusOK, get(t, h, p).Code, "shell file %s", p)
	}
	for _, p := range []string{
		"/console/graph/knowledge-graph.json",
		"/console/graph/target-graph.json",
		"/console/graph/KNOWLEDGE-GRAPH.JSON",
		"/console/graph/knowledge-graph.json/",
		"/console/assets/../graph/knowledge-graph.json",
		"/console/console.js.map",
		"/console/.env",
		"/console/nested/.hidden/leak.js",
		"/console/assets/",
		"/console/listing/",
		"/console/assets",
		"/console/missing.js",
	} {
		w := get(t, h, p)
		assert.Equal(t, http.StatusNotFound, w.Code, "%s must be refused", p)
		assert.NotContains(t, w.Body.String(), "note", p)
		assert.Contains(t, w.Body.String(), `"reason":"MGS9010"`, "%s answers the one structured refusal", p)
	}
}

// The filesystem layer itself never lists, so a FileServer reaching a directory with no
// index.html (one that vanished after the handler's check, say) names none of its files.
func TestShellDirNeverLists(t *testing.T) {
	dir := consoleDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "listing"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "listing", "graph.json"), []byte("{}"), 0o644))

	w := get(t, http.FileServer(shellDir{http.Dir(dir)}), "/listing/")
	assert.NotEqual(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "graph.json")

	f, err := shellDir{http.Dir(dir)}.Open("/listing")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	_, err = f.Readdir(-1)
	require.ErrorIs(t, err, fs.ErrPermission)
}
