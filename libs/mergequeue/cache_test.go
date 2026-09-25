package mergequeue

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
)

// upstreamCall is one request the fake cache service received.
type upstreamCall struct {
	Path, Auth, ContentType, Body string
}

// fakeCacheService answers every method with a signed download URL and records what
// reached it.
func fakeCacheService(t *testing.T) (*httptest.Server, func() []upstreamCall) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []upstreamCall
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, upstreamCall{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Only", "leaks")
		_, _ = io.WriteString(w, `{"ok":true,"signed_download_url":"https://blob.example/entry?sp=r&sig=s"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []upstreamCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]upstreamCall(nil), calls...)
	}
}

func startProxy(t *testing.T, upstream string, log *HookLog) *CacheReadProxy {
	t.Helper()
	p, err := StartCacheReadProxy(upstream+"/", "real-runtime-token", log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func twirp(t *testing.T, p *CacheReadProxy, token, method, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, p.URL+strings.TrimPrefix(cacheService, "/")+method, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res, string(got)
}

func TestCacheReadProxyForwardsALookupWithTheRealTokenUpstreamOnly(t *testing.T) {
	up, calls := fakeCacheService(t)
	p := startProxy(t, up.URL, nil)
	require.True(t, strings.HasPrefix(p.URL, "http://127.0.0.1:"), "loopback only: %s", p.URL)
	require.True(t, strings.HasSuffix(p.URL, "/"), "a base URL, as the runner's is")
	assert.NotContains(t, p.Token, "real-runtime-token")

	lookup := `{"key":"magus-abc","version":"v"}`
	res, body := twirp(t, p, p.Token, "GetCacheEntryDownloadURL", lookup)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, `{"ok":true,"signed_download_url":"https://blob.example/entry?sp=r&sig=s"}`, body,
		"the download URL reaches the hook as the service sent it; the hook fetches the blob itself")
	assert.Empty(t, res.Header.Get("X-Upstream-Only"), "only the content type crosses back")
	assert.NotContains(t, body, "real-runtime-token")
	assert.Equal(t, []upstreamCall{{
		Path:        cacheService + "GetCacheEntryDownloadURL",
		Auth:        "Bearer real-runtime-token",
		ContentType: "application/json",
		Body:        lookup,
	}}, calls())
}

func TestCacheReadProxyRefusesEveryWriteAndAnythingUnknown(t *testing.T) {
	up, calls := fakeCacheService(t)
	var log bytes.Buffer
	p := startProxy(t, up.URL, NewHookLog(&log))
	for _, method := range []string{"CreateCacheEntry", "FinalizeCacheEntryUpload", "DeleteCacheEntry", "Unknown", "../CacheService/CreateCacheEntry", ""} {
		res, body := twirp(t, p, p.Token, method, `{"key":"magus-abc"}`)
		assert.Equal(t, http.StatusForbidden, res.StatusCode, method)
		assert.Contains(t, body, `"code":"permission_denied"`, method)
	}
	for _, path := range []string{"/", "/_apis/artifactcache/caches", "/twirp/other.Service/GetCacheEntryDownloadURL"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, strings.TrimSuffix(p.URL, "/")+path, strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+p.Token)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		assert.Equal(t, http.StatusForbidden, res.StatusCode, path)
	}
	res, _ := twirp(t, p, "real-runtime-token", "GetCacheEntryDownloadURL", `{}`)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode, "only the stand-in authenticates here")
	res, _ = twirp(t, p, "", "GetCacheEntryDownloadURL", `{}`)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode, "the port alone reads nothing")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.URL+strings.TrimPrefix(cacheService, "/")+"GetCacheEntryDownloadURL", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+p.Token)
	get, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = get.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, get.StatusCode)

	assert.Empty(t, calls(), "nothing refused reaches the service")
	assert.Contains(t, log.String(), "[remote cache read] refused POST "+cacheService+"CreateCacheEntry")
	assert.NotContains(t, log.String(), p.Token)
}

func TestStartCacheReadProxyNeedsTheRunnersCredentials(t *testing.T) {
	_, err := StartCacheReadProxy("", "t", nil)
	require.Error(t, err)
	_, err = StartCacheReadProxy("https://results.example/", "", nil)
	require.Error(t, err)
}

// The runner's cache credentials are scrubbed; the proxy's stand-ins take their names.
func TestHooksReadTheCacheThroughTheProxyAndNeverHoldTheRealToken(t *testing.T) {
	up, _ := fakeCacheService(t)
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "real-runtime-token")
	t.Setenv("ACTIONS_RESULTS_URL", up.URL+"/")
	p := startProxy(t, up.URL, nil)
	env := HookEnv{Fixed: p.Env()}
	dir, regenDir := t.TempDir(), t.TempDir()

	_, err := CommandGate(script(`env > seen`), env, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir}, hookUnits)
	require.NoError(t, err)
	require.NoError(t, CommandRegenerate(script(`env > seen`), env, nil)(context.Background(),
		types.Regeneration{Dir: regenDir, Change: hookChange, Paths: []string{"x"}, Units: hookUnits}))
	for _, d := range []string{dir, regenDir} {
		seen, err := os.ReadFile(filepath.Join(d, "seen"))
		require.NoError(t, err)
		assert.Contains(t, string(seen), "ACTIONS_RESULTS_URL="+p.URL+"\n")
		assert.Contains(t, string(seen), "ACTIONS_RUNTIME_TOKEN="+p.Token+"\n")
		assert.NotContains(t, string(seen), "real-runtime-token")
		assert.NotContains(t, string(seen), up.URL+"/\n")
	}
}
