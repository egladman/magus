// cross-cutting: every mount in daemon.go and share.go against auth's verifier and httpx's guard

package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/config"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/proto/gen/go/magus/activity/v1alpha1/activityv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1/graphv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/insight/v1alpha1/insightv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1/jobv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/memory/v1alpha1/memoryv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/metrics/v1alpha1/metricsv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/notes/v1alpha1/notesv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/status/v1alpha1/statusv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1/tokenv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/tool/v1alpha1/toolv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1/viewerv1alpha1connect"
	"github.com/egladman/magus/types"
)

var (
	read  = types.Need{Surface: types.SurfaceConsole, Level: types.LevelRead}
	write = types.Need{Surface: types.SurfaceConsole, Level: types.LevelWrite}
)

// pinnedNeeds is every guarded route of a loaded daemon and the Need it holds bearers to,
// written out by hand. It is the reviewer's view: a mount that changes tier, or a new mount,
// changes this table in the same diff, and a mount the table does not name fails the matrix.
var pinnedNeeds = map[string]types.Need{
	"/mcp":            {Surface: types.SurfaceMCP, Level: types.LevelWrite},
	"/api/":           write, // mixed: it carries the diff review's writes
	"/api/v1/events":  read,
	"/api/v1/insight": read,
	"/api/v1/share":   write,
	"/" + tokenv1alpha1connect.TokenServiceName + "/":       {Surface: types.SurfaceTokens, Level: types.LevelWrite},
	"/" + jobv1alpha1connect.JobServiceName + "/":           write,
	"/" + memoryv1alpha1connect.MemoryServiceName + "/":     write,
	"/" + graphv1alpha1connect.GraphServiceName + "/":       write, // one body of data with /api/v1/graph
	"/" + activityv1alpha1connect.ActivityServiceName + "/": read,
	"/" + statusv1alpha1connect.StatusServiceName + "/":     read,
	"/" + toolv1alpha1connect.ToolServiceName + "/":         read,
	"/" + insightv1alpha1connect.InsightServiceName + "/":   read,
	"/" + viewerv1alpha1connect.ViewerServiceName + "/":     read,
	"/" + notesv1alpha1connect.NotesServiceName + "/":       read,
	"/" + metricsv1alpha1connect.MetricsServiceName + "/":   read,
}

// mountedOnlyWhen names the pinned routes a daemon mounts conditionally.
var mountedOnlyWhen = map[string]string{
	"/" + metricsv1alpha1connect.MetricsServiceName + "/": "the workspace collects metrics",
}

// bearer is one presented token and the grant it should verify as; ok false means it must
// not verify at all.
type bearer struct {
	token string
	grant types.Grant
	ok    bool
}

// plant writes a token record straight into tokens.d, standing in for anything that could
// put a record there: it lets the matrix carry an expired token and a record forged to hold
// another class's hash.
func plant(t *testing.T, name, secret string, grant types.Grant, expires time.Time) {
	t.Helper()
	dir, err := auth.StateDir()
	require.NoError(t, err)
	sum := auth.Fingerprint(secret)
	full := sha256Hex(secret)
	require.Equal(t, sum, full[:8])
	rec := map[string]any{
		"version": 2, "id": sum, "name": name, "sha256": full,
		"grant": grant, "created": time.Now().UTC(), "expires": expires.UTC(),
	}
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	path := filepath.Join(dir, "tokens.d", name+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// bootConsoleDaemon starts a loaded daemon with a built console, returning its base URL, its
// mounted patterns and the needs it recorded, and the buffer its log writes to.
func bootConsoleDaemon(t *testing.T) (base string, patterns []string, needs map[string]types.Need, logs *syncBuffer, cacheDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := fixtureWorkspace(t)
	consoleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(consoleDir, "index.html"), []byte("<html><head>\n</head><body>shell</body></html>"), 0o600))
	t.Setenv("MAGUS_CONSOLE_DIR", consoleDir)

	ctx, cancel := context.WithCancel(context.Background())
	m, err := magus.Open(ctx, root)
	require.NoError(t, err)
	port := freePort(t)
	addr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	logs = &syncBuffer{}
	d := New(mcp.Options{Magus: m, Version: "test", HTTPAddr: addr, Logger: slog.New(slog.NewTextHandler(logs, nil)),
		HealthRoutes: map[string]http.Handler{"/readyz": ok}})
	type mounted struct {
		patterns []string
		needs    map[string]types.Need
	}
	got := make(chan mounted, 1)
	d.onMounted = func(p []string, n map[string]types.Need) { got <- mounted{p, n} }
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveErr:
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down")
		}
	})
	base = fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, base+"/readyz")
	mnt := <-got
	return base, mnt.patterns, mnt.needs, logs, m.CacheDir()
}

// TestGrantMatrix walks every route the daemon mounted crossed with every kind of bearer, and
// checks each cell against the verdict DERIVED from the bearer's grant and the route's Need:
// 401 MGS9011 with no token, 401 MGS9001 for a token that is not a credential here, 403
// MGS9015 for a credential whose grant is below the need, and admitted otherwise. The needs
// themselves are pinned by hand in pinnedNeeds.
func TestGrantMatrix(t *testing.T) {
	base, patterns, needs, _, _ := bootConsoleDaemon(t)

	store, err := auth.LoadStore()
	require.NoError(t, err)
	mint := func(name string, g types.Grant) string {
		secret, _, err := store.Mint(types.GrantOperator, auth.MintRequest{Name: name, Grant: g, Expires: time.Now().Add(time.Hour)})
		require.NoError(t, err)
		return secret
	}
	op, err := auth.Load()
	require.NoError(t, err)
	connector := mint("connector", types.GrantConnector)
	console := mint("console", types.GrantConsole)
	viewer := mint("viewer", types.GrantViewer)
	revoked := mint("revoked", types.GrantConsole)
	_, err = store.Revoke("revoked")
	require.NoError(t, err)

	// Expired: a real mgs_ token whose record's expiry has passed.
	expired, err := func() (string, error) {
		s := mint("expired-src", types.GrantConsole)
		_, e := store.Revoke("expired-src")
		return s, e
	}()
	require.NoError(t, err)
	plant(t, "expired", expired, types.GrantConsole, time.Now().Add(-time.Minute))

	// Wrong class on loopback: a share secret, with a forged record holding its hash.
	share, _, err := auth.MintShare(types.GrantOperator, time.Hour)
	require.NoError(t, err)
	plant(t, "forged-share", share, types.GrantOperator, time.Now().Add(time.Hour))
	// A forged viewer record holding the operator secret's hash: the operator still verifies
	// as the operator, since an mgo_ string never reaches the store.
	plant(t, "forged-operator", op, types.GrantViewer, time.Now().Add(time.Hour))
	// The operator body restamped as a stored token: well-formed, and in no store.
	restamped := "mgs_" + strings.TrimPrefix(op, "mgo_")

	broken := []byte(connector)
	broken[len(broken)-1] ^= 1

	bearers := map[string]bearer{
		"operator":             {op, types.GrantOperator, true},
		"connector":            {connector, types.GrantConnector, true},
		"console":              {console, types.GrantConsole, true},
		"viewer":               {viewer, types.GrantViewer, true},
		"share on loopback":    {share, types.Grant{}, false},
		"expired":              {expired, types.Grant{}, false},
		"revoked":              {revoked, types.Grant{}, false},
		"checksum broken":      {string(broken), types.Grant{}, false},
		"operator as mgs_":     {restamped, types.Grant{}, false},
		"not a magus token":    {"dGhpcyBpcyBub3QgYSB0b2tlbg", types.Grant{}, false},
		"anonymous (no token)": {"", types.Grant{}, false},
	}
	// The verifier agrees with the expectations the verdicts are derived from.
	for name, b := range bearers {
		cred, ok := auth.Verify(b.token)
		require.Equal(t, b.ok, ok, name)
		if ok {
			require.Equal(t, b.grant, cred.Grant, name)
		}
	}

	// The needs the daemon recorded are the pinned ones, route for route.
	guarded := map[string]bool{}
	for _, p := range patterns {
		if p == "/readyz" || p == "/console/" {
			continue
		}
		want, pinned := pinnedNeeds[p]
		if !assert.True(t, pinned, "route %s is mounted but not pinned in pinnedNeeds", p) {
			continue
		}
		assert.Equal(t, want, needs[p], "route %s changed tier", p)
		guarded[p] = true
	}
	for p := range pinnedNeeds {
		if _, optional := mountedOnlyWhen[p]; !optional {
			assert.True(t, guarded[p], "pinned route %s is not mounted", p)
		}
	}

	require.GreaterOrEqual(t, len(guarded), len(pinnedNeeds)-len(mountedOnlyWhen), "the walk must see every guarded route")

	client := &http.Client{Timeout: 10 * time.Second}
	cells := 0
	defer func() { t.Logf("probed %d cells over %d routes", cells, len(guarded)) }()
	for p := range guarded {
		need := pinnedNeeds[p]
		for name, b := range bearers {
			status, reason := probe(t, client, base, p, b.token)
			cells++
			cell := fmt.Sprintf("%s x %s (needs %s)", p, name, need)
			switch {
			case b.token == "":
				assert.Equal(t, [2]any{http.StatusUnauthorized, "MGS9011"}, [2]any{status, reason}, cell)
			case !b.ok:
				assert.Equal(t, [2]any{http.StatusUnauthorized, "MGS9001"}, [2]any{status, reason}, cell)
			case !b.grant.Allows(need):
				assert.Equal(t, [2]any{http.StatusForbidden, "MGS9015"}, [2]any{status, reason}, cell)
			default:
				assert.NotEqual(t, http.StatusUnauthorized, status, cell)
				assert.NotContains(t, []string{"MGS9001", "MGS9011", "MGS9015"}, reason, cell)
			}
		}
	}
}

// probe sends one request to route as bearer token and returns the status and the MGS reason
// of a refusal, "" for anything else. It asks each route something it answers without
// side effects: a POST to an unknown procedure under a Connect service, a GET to the share
// trigger (which only a POST acts on), a POST everywhere else.
func probe(t *testing.T, client *http.Client, base, route, token string) (int, string) {
	t.Helper()
	method, path := http.MethodPost, route
	switch {
	case route == "/api/v1/share":
		method = http.MethodGet
	case strings.HasSuffix(route, "/"):
		path = route + "Probe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	for _, code := range []string{"MGS9001", "MGS9011", "MGS9015"} {
		if bytes.Contains(body, []byte(code)) {
			return resp.StatusCode, code
		}
	}
	return resp.StatusCode, ""
}

// Secrets rest nowhere but with the caller: mint and revoke through the CLI's store and the
// TokenService, then search every file under the state and trail dirs, the daemon log, and
// every List response for each secret's bytes. The one file allowed to hold a secret is the
// operator token's own.
func TestSecretsNeverRestInTheTrailLogsOrLists(t *testing.T) {
	base, _, _, logs, cacheDir := bootConsoleDaemon(t)
	op, err := auth.Load()
	require.NoError(t, err)

	store, err := auth.LoadStore()
	require.NoError(t, err)
	cliSecret, _, err := store.Mint(types.GrantOperator, auth.MintRequest{Name: "from-cli", Grant: types.GrantConnector, Expires: time.Now().Add(time.Hour)})
	require.NoError(t, err)

	call := func(method, body string) []byte {
		req, err := http.NewRequest(http.MethodPost, base+"/"+tokenv1alpha1connect.TokenServiceName+"/"+method, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+op)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", method, out)
		return out
	}
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var minted struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(call("CreateToken", `{"name":"from-console","scope":"TOKEN_SCOPE_CONSOLE","expireTime":"`+exp+`"}`), &minted))
	require.NotEmpty(t, minted.Secret)
	var doomed struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(call("CreateToken", `{"name":"doomed","scope":"TOKEN_SCOPE_CONSOLE_READ","expireTime":"`+exp+`"}`), &doomed))
	call("RevokeToken", `{"name":"doomed"}`)
	list := call("ListTokens", `{}`)

	secrets := map[string]string{"operator": op, "cli mint": cliSecret, "console mint": minted.Secret, "revoked mint": doomed.Secret}
	for name, secret := range secrets {
		assert.NotContains(t, string(list), secret, "ListTokens carries the %s secret", name)
		assert.NotContains(t, logs.String(), secret, "the daemon log carries the %s secret", name)
	}
	opFile, err := auth.Path()
	require.NoError(t, err)
	stateDir, err := auth.StateDir()
	require.NoError(t, err)
	searched := 0
	for _, dir := range []string{stateDir, cacheDir} {
		require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || path == opFile {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			searched++
			for name, secret := range secrets {
				assert.NotContains(t, string(b), secret, "%s holds the %s secret", path, name)
			}
			return nil
		}))
	}
	assert.Positive(t, searched)

	// The trail did record the mint and the revoke, by id.
	events, err := os.ReadFile(filepath.Join(cacheDir, "activity", "events.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(events), `"action":"CreateToken"`)
	assert.Contains(t, string(events), `"action":"RevokeToken"`)
	assert.Contains(t, string(events), `"class":"operator"`, "each record names the credential that acted")
}

// A non-loopback bind serves bearer tokens in cleartext, so without mcp.insecure_bind the
// daemon refuses to start, before it mints or serves anything.
func TestNonLoopbackBindWithoutOptInIsAnError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	d := New(mcp.Options{Version: "test", HTTPAddr: netip.MustParseAddrPort("0.0.0.0:0")})
	_, _, err := d.prepare(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp.insecure_bind")
	_, err = auth.Load()
	assert.ErrorIs(t, err, auth.ErrNoToken, "a refused start mints nothing")

	var cfg config.Config
	cfg.MCP.InsecureBind = true
	d = New(mcp.Options{Version: "test", HTTPAddr: netip.MustParseAddrPort("0.0.0.0:0"), Config: cfg})
	_, _, err = d.prepare(context.Background())
	assert.NoError(t, err, "the opt-in is honored")
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// syncBuffer is a bytes.Buffer safe for the daemon's concurrent log writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
