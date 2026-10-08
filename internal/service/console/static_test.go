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

// consoleDir is a built console as the build leaves it: the shell, a stylesheet, and the stub
// console/scripts/surface-stubs.mjs writes for every surface segment.
func consoleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "index.html", "<html><head>\n</head><body>shell</body></html>")
	write(t, dir, "console.css", ".a{}")
	for _, s := range KnownApps {
		write(t, dir, s+"/index.html", "<html><head>\n  <base href=\"../\"></head><body>stub</body></html>")
	}
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
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
func TestAppRouteWithoutTrailingSlashRedirects(t *testing.T) {
	h := StaticHandler(consoleDir(t))

	for _, app := range KnownApps {
		w := get(t, h, "/console/"+app)
		assert.Equal(t, http.StatusFound, w.Code, "%s must canonicalize", app)
		assert.Equal(t, "/console/"+app+"/", w.Header().Get("Location"), app)
	}
}

func TestAppRouteRedirectKeepsTheQuery(t *testing.T) {
	h := StaticHandler(consoleDir(t))
	w := get(t, h, "/console/diff?scope=a.go&x=1")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/console/diff/?scope=a.go&x=1", w.Header().Get("Location"))
}

// The canonical form Link mints must be served directly: a redirect loop here would take the
// whole console down.
func TestCanonicalAppRouteServesTheShell(t *testing.T) {
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

func TestUnknownSegmentIsNotAnAppRoute(t *testing.T) {
	h := StaticHandler(consoleDir(t))
	w := get(t, h, "/console/not-an-app")
	assert.NotEqual(t, http.StatusFound, w.Code, "only a known app canonicalizes")
}

// KnownApps is the contract the server, the link minters, and the console's boot router
// all read. A surface added to the console without being added here is deep-linkable in
// exactly one direction, which is the kind of gap nobody notices until someone shares a URL.
func TestKnownAppsCoversTheDiffApp(t *testing.T) {
	assert.True(t, IsAppRoute("diff"))
	assert.False(t, IsAppRoute("review"), "the surface was renamed; the old segment is gone")
	assert.False(t, IsAppRoute(""), "the console root is not a surface route")
	assert.False(t, IsAppRoute("diff/diff.js"), "a sub-path is a file, not a route")
	for _, s := range KnownApps {
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

// The server's routes are the stubs the console build wrote, not a list here: a segment the
// console adds is served as soon as its stub exists, and one without a stub is not a route even
// when magus knows the name.
func TestAppRoutesComeFromTheBundle(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "<html><head>\n</head><body>shell</body></html>")
	write(t, dir, "newapp/index.html", "stub")
	write(t, dir, "wasm/buzz.wasm", "\x00asm")
	write(t, dir, ".hidden/index.html", "stub")
	h := StaticHandler(dir)

	w := get(t, h, "/console/newapp")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/console/newapp/", w.Header().Get("Location"))
	w = get(t, h, "/console/newapp/")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "shell", "a route answers with the shell, not the stub")

	for _, seg := range []string{"diff", "wasm", ".hidden"} {
		assert.NotEqual(t, http.StatusFound, get(t, h, "/console/"+seg).Code, "%s has no stub", seg)
	}
}

func TestAppRoute(t *testing.T) {
	dir := consoleDir(t)
	write(t, dir, "assets/icon.svg", "<svg/>")

	for _, tc := range []struct {
		seg  string
		want string
		ok   bool
	}{
		{"diff", "/console/diff/", true},
		{"plan", "/console/plan/", true},
		{"assets", "", false},
		{"DIFF", "", false},
		{"diff/diff.js", "", false},
		{"console.css", "", false},
		{"", "", false},
		{"..", "", false},
	} {
		got, ok := appRoute(dir, tc.seg)
		assert.Equal(t, tc.ok, ok, tc.seg)
		assert.Equal(t, tc.want, got, tc.seg)
	}
	_, ok := appRoute(filepath.Join(dir, "missing"), "diff")
	assert.False(t, ok, "an unreadable console dir has no routes")
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
		// A directory holding an index.html is a surface route, so the shell answers.
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

// The Graph's Figures mode compiles the playground's Buzz runtime in the page: the wasm is served
// typed for WebAssembly.instantiateStreaming, and the CSP admits compiling it and nothing more.
func TestConsoleServesTheBuzzRuntime(t *testing.T) {
	dir := consoleDir(t)
	p := filepath.Join(dir, "wasm", "buzz.wasm")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("\x00asm\x01\x00\x00\x00"), 0o600))
	h := StaticHandler(dir)

	w := get(t, h, "/console/wasm/buzz.wasm")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/wasm", w.Header().Get("Content-Type"))

	csp := get(t, h, "/console/").Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "script-src 'self' 'wasm-unsafe-eval';")
	assert.NotContains(t, csp, "'unsafe-eval'", "wasm compilation only, never eval")
}
