// cross-cutting: every mount in server.go and share.go against auth's verifier and httpx's guard

package server

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
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/config"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1/tokenv1alpha1connect"
	"github.com/egladman/magus/types"
)

var (
	read   = types.Need{Surface: types.SurfaceConsole, Level: types.LevelRead}
	write  = types.Need{Surface: types.SurfaceConsole, Level: types.LevelWrite}
	tokens = types.Need{Surface: types.SurfaceTokens, Level: types.LevelWrite}
)

// pinnedNeeds is every guarded path of a server, each Connect procedure and each /api route,
// and the Need it holds bearers to, written out by hand. It is the reviewer's view: a path
// that changes tier, or a new one, changes this table in the same diff, and a path the table
// does not name fails the matrix.
var pinnedNeeds = map[string]types.Need{
	"/mcp": {Surface: types.SurfaceMCP, Level: types.LevelWrite},

	"/api/":                 read, // the not-found fallback
	"/api/v1/events":        read,
	"/api/v1/insight":       read,
	"/api/v1/graph":         read,
	"/api/v1/diff":          write,
	"/api/v1/diff/patch":    write,
	"/api/v1/diff/context":  write,
	"/api/v1/diff/session":  write,
	"/api/v1/diff/review":   write,
	"/api/v1/diff/branches": write,
	"/api/v1/diff/run":      write,
	"/api/v1/plan":          write,
	"/api/v1/attention":     write,
	"/api/v1/share":         write,

	"/magus.activity.v1alpha1.ActivityService/ListActivityEvents":  read,
	"/magus.activity.v1alpha1.ActivityService/GetPayload":          read,
	"/magus.activity.v1alpha1.ActivityService/WatchActivityEvents": read,
	"/magus.graph.v1alpha1.GraphService/QueryNodes":                read,
	"/magus.graph.v1alpha1.GraphService/ResolveNodes":              read,
	"/magus.graph.v1alpha1.GraphService/ExplainNode":               read,
	"/magus.graph.v1alpha1.GraphService/FindPath":                  read,
	"/magus.graph.v1alpha1.GraphService/FindDependents":            read,
	"/magus.graph.v1alpha1.GraphService/FindAffected":              read,
	"/magus.graph.v1alpha1.GraphService/GetGraphStats":             read,
	"/magus.insight.v1alpha1.InsightService/GetInsight":            read,
	"/magus.job.v1alpha1.JobService/ListJobs":                      read,
	"/magus.job.v1alpha1.JobService/RunJob":                        write,
	"/magus.memory.v1alpha1.MemoryService/ListMemories":            write,
	"/magus.memory.v1alpha1.MemoryService/GetCursor":               write,
	"/magus.memory.v1alpha1.MemoryService/UpdateMemory":            write,
	"/magus.memory.v1alpha1.MemoryService/DeleteMemory":            write,
	"/magus.memory.v1alpha1.MemoryService/UpdateCursor":            write,
	"/magus.metrics.v1alpha1.MetricsService/GetMetrics":            read,
	"/magus.metrics.v1alpha1.MetricsService/StreamMetrics":         read,
	"/magus.notes.v1alpha1.NotesService/ListNotes":                 read,
	"/magus.notes.v1alpha1.NotesService/GetNote":                   read,
	"/magus.status.v1alpha1.StatusService/GetStatus":               read,
	"/magus.status.v1alpha1.StatusService/StreamStatus":            read,
	"/magus.token.v1alpha1.TokenService/ListTokens":                tokens,
	"/magus.token.v1alpha1.TokenService/CreateToken":               tokens,
	"/magus.token.v1alpha1.TokenService/RevokeToken":               tokens,
	"/magus.tool.v1alpha1.ToolService/ListTools":                   read,
	"/magus.viewer.v1alpha1.ViewerService/GetInvocation":           read,
	"/magus.viewer.v1alpha1.ViewerService/ListEvents":              read,
	"/magus.viewer.v1alpha1.ViewerService/StreamEvents":            read,
	"/magus.viewer.v1alpha1.ViewerService/ListOutputs":             read,
	"/magus.viewer.v1alpha1.ViewerService/GetOutput":               read,
	"/magus.viewer.v1alpha1.ViewerService/ListInvocations":         read,
	"/magus.viewer.v1alpha1.ViewerService/GetJournal":              read,
	"/magus.viewer.v1alpha1.ViewerService/GetSessionActivity":      read,
}

// mountedOnlyWhen names the pinned paths a loaded server mounts conditionally.
var mountedOnlyWhen = map[string]string{
	"/magus.metrics.v1alpha1.MetricsService/GetMetrics":    "the workspace collects metrics",
	"/magus.metrics.v1alpha1.MetricsService/StreamMetrics": "the workspace collects metrics",
}

// unguardedRoutes are the mounts that take no bearer, each for its stated reason.
var unguardedRoutes = map[string]string{
	"/readyz":                "a health probe",
	"/console/":              "the console's app shell",
	"/api/v1/token/exchange": "the one-time code in its body is the credential",
}

// Every magus service registered in this binary has a Need for every procedure, and the table
// names nothing that does not exist: the server cannot start otherwise, and this says which.
func TestEveryProcedureHasANeed(t *testing.T) {
	t.Parallel()
	seen := map[protoreflect.FullName]bool{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "magus.") {
			return true
		}
		for i := range fd.Services().Len() {
			sd := fd.Services().Get(i)
			seen[sd.FullName()] = true
			needs, err := serviceNeeds("/" + string(sd.FullName()) + "/")
			if assert.NoError(t, err, sd.FullName()) {
				for path, need := range needs {
					assert.NoError(t, need.Validate(), path)
					assert.Equal(t, pinnedNeeds[path], need, "%s changed tier", path)
				}
			}
		}
		return true
	})
	for name := range procedureNeeds {
		assert.True(t, seen[name], "procedureNeeds names %s, which no proto declares", name)
	}
	for path, need := range apiNeeds {
		assert.Equal(t, pinnedNeeds[path], need, "%s changed tier", path)
	}
	_, err := serviceNeeds("/magus.nothing.v1alpha1.NoService/")
	assert.Error(t, err)
}

// The share listener serves only read routes, held to the loopback server's own Needs, so a
// share link (console=read) reaches each of them and nothing more.
func TestShareRoutesHoldTheLoopbackNeeds(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"/magus.activity.v1alpha1.ActivityService/",
		"/magus.status.v1alpha1.StatusService/",
		"/magus.insight.v1alpha1.InsightService/",
		"/magus.viewer.v1alpha1.ViewerService/",
		"/magus.metrics.v1alpha1.MetricsService/",
	} {
		route := serviceRoute(path, http.NotFoundHandler())
		require.NotEmpty(t, route.Needs, path)
		for proc, need := range route.Needs {
			assert.Equal(t, pinnedNeeds[proc], need, proc)
			assert.True(t, types.GrantViewer.Allows(need), "%s on a share link needs %s", proc, need)
		}
	}
	for _, path := range []string{"/api/v1/events", "/api/v1/insight"} {
		assert.Equal(t, map[string]types.Need{path: pinnedNeeds[path]}, apiNeedsFor(path))
	}
	assert.Empty(t, serviceRoute("/magus.nothing.v1alpha1.NoService/", http.NotFoundHandler()).Needs,
		"an unknown service yields no Needs, which the share listener refuses to guard")
}

// bearer is one presented token and the grant it should verify as; ok false means it must
// not verify at all.
type bearer struct {
	token string
	grant types.Grant
	ok    bool
}

// plant writes a token record straight into tokens.d, standing in for anything that could
// put a record there: it lets the matrix carry an expired token and records forged to hold
// another class's hash or more than any mint grants.
func plant(t *testing.T, name, secret string, grant types.Grant, expires time.Time) {
	t.Helper()
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	id := auth.TokenID(secret)
	full := sha256Hex(secret)
	require.Equal(t, id, full[:8])
	rec := map[string]any{
		"version": 2, "id": id, "name": name, "class": "stored", "sha256": full,
		"grant": grant, "created": time.Now().Add(-2 * time.Minute).UTC(), "expires": expires.UTC(),
	}
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	path := filepath.Join(dir, name+".json")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// matrixBearers mints and plants every kind of bearer the matrix presents. The server must
// already have minted the operator token.
func matrixBearers(t *testing.T) map[string]bearer {
	t.Helper()
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	store, err := auth.LoadStore(dir)
	require.NoError(t, err)
	mint := func(name string, g types.Grant) string {
		secret, _, err := store.Mint(types.GrantOperator, auth.MintRequest{Name: name, Grant: g, TTL: time.Hour})
		require.NoError(t, err)
		return secret
	}
	op, err := auth.LoadOperator()
	require.NoError(t, err)
	connector := mint("connector", types.GrantConnector)
	console := mint("console", types.GrantConsole)
	viewer := mint("viewer", types.GrantViewer)
	revoked := mint("revoked", types.GrantConsole)
	_, err = store.Revoke(types.GrantOperator, "revoked")
	require.NoError(t, err)
	code, _, err := store.MintCode(types.GrantOperator, auth.MintRequest{Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)

	// Expired: a real mgs_ token whose record's expiry has passed.
	expired := mint("expired-src", types.GrantConsole)
	_, err = store.Revoke(types.GrantOperator, "expired-src")
	require.NoError(t, err)
	plant(t, "expired", expired, types.GrantConsole, time.Now().Add(-time.Minute))
	// A record planted to hold tokens=write, and one that never expires: both skipped.
	escalated := mint("escalated-src", types.GrantViewer)
	_, err = store.Revoke(types.GrantOperator, "escalated-src")
	require.NoError(t, err)
	plant(t, "escalated", escalated, types.GrantOperator, time.Now().Add(time.Hour))
	forever := mint("forever-src", types.GrantViewer)
	_, err = store.Revoke(types.GrantOperator, "forever-src")
	require.NoError(t, err)
	plant(t, "forever", forever, types.GrantConsole, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))

	// Wrong class on loopback: a share secret, with a forged record holding its hash.
	share, _, err := auth.MintShare(types.GrantOperator, time.Hour)
	require.NoError(t, err)
	plant(t, "forged-share", share, types.GrantConsole, time.Now().Add(time.Hour))
	// A forged viewer record holding the operator secret's hash: the operator still verifies
	// as the operator, since an mgo_ string never reaches the store.
	plant(t, "forged-operator", op, types.GrantViewer, time.Now().Add(time.Hour))
	// The operator body restamped as a stored token: well-formed, and in no store.
	restamped := "mgs_" + strings.TrimPrefix(op, "mgo_")

	broken := []byte(connector)
	broken[len(broken)-1] ^= 1

	bearers := map[string]bearer{
		"operator":              {op, types.GrantOperator, true},
		"connector":             {connector, types.GrantConnector, true},
		"console":               {console, types.GrantConsole, true},
		"viewer":                {viewer, types.GrantViewer, true},
		"share on loopback":     {share, types.Grant{}, false},
		"link code as a bearer": {code, types.Grant{}, false},
		"expired":               {expired, types.Grant{}, false},
		"revoked":               {revoked, types.Grant{}, false},
		"planted tokens=write":  {escalated, types.Grant{}, false},
		"planted year-9999":     {forever, types.Grant{}, false},
		"checksum broken":       {string(broken), types.Grant{}, false},
		"operator as mgs_":      {restamped, types.Grant{}, false},
		"not a magus token":     {"dGhpcyBpcyBub3QgYSB0b2tlbg", types.Grant{}, false},
		"anonymous (no token)":  {"", types.Grant{}, false},
	}
	// The verifier agrees with the expectations the verdicts are derived from.
	for name, b := range bearers {
		cred, ok := auth.Verify(b.token)
		require.Equal(t, b.ok, ok, name)
		if ok {
			require.Equal(t, b.grant, cred.Grant, name)
		}
	}
	return bearers
}

// bootConsoleServer starts a loaded server with a built console, returning its base URL, its
// mounted patterns and the needs it recorded, and the buffer its log writes to.
func bootConsoleServer(t *testing.T) (base string, patterns []string, needs map[string]types.Need, logs *syncBuffer, cacheDir string) {
	t.Helper()
	testkit.Isolate(t)
	root := fixtureWorkspace(t)
	builtConsole(t)

	m, err := magus.Open(t.Context(), root)
	require.NoError(t, err)
	logs = &syncBuffer{}
	d, addr := testServer(t, func(opts mcp.Options) *Server {
		opts.Magus = m
		opts.Logger = slog.New(slog.NewTextHandler(logs, nil))
		return New(opts)
	})
	base, patterns, needs = serveMounted(t, d, addr)
	return base, patterns, needs, logs, m.CacheDir()
}

// bootUnloadedServer starts a server whose workspace failed to load.
func bootUnloadedServer(t *testing.T) (base string, patterns []string, needs map[string]types.Need) {
	t.Helper()
	testkit.Isolate(t)
	root := t.TempDir()
	builtConsole(t)
	failure := &types.WorkspaceFailure{Message: "magusfile: exec magusfile.buzz: [BZZ1005] ..."}
	d, addr := testServer(t, func(opts mcp.Options) *Server {
		return NewUnloaded(opts, Unloaded{Root: root, Err: func() rpcerr.Error { return rpcerr.WorkspaceFailed(root, failure) }})
	})
	return serveMounted(t, d, addr)
}

func builtConsole(t *testing.T) {
	t.Helper()
	consoleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(consoleDir, "index.html"), []byte("<html><head>\n</head><body>shell</body></html>"), 0o600))
	t.Setenv("MAGUS_CONSOLE_DIR", consoleDir)
}

func testServer(t *testing.T, build func(mcp.Options) *Server) (*Server, netip.AddrPort) {
	t.Helper()
	addr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), freePort(t))
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return build(mcp.Options{Version: "test", HTTPAddr: addr, HealthRoutes: map[string]http.Handler{"/readyz": ok}}), addr
}

func serveMounted(t *testing.T, d *Server, addr netip.AddrPort) (string, []string, map[string]types.Need) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
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
			t.Error("server did not shut down")
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", addr.Port())
	waitReady(t, base+"/readyz")
	mnt := <-got
	return base, mnt.patterns, mnt.needs
}

// checkNeeds asserts the needs a server recorded are the pinned ones, path for path, that every
// mounted pattern is either guarded by one of them or named in unguardedRoutes, and returns the
// guarded paths.
func checkNeeds(t *testing.T, patterns []string, needs map[string]types.Need, optional map[string]string) []string {
	t.Helper()
	for path, need := range needs {
		want, pinned := pinnedNeeds[path]
		if assert.True(t, pinned, "%s is guarded but not pinned in pinnedNeeds", path) {
			assert.Equal(t, want, need, "%s changed tier", path)
		}
	}
	for path := range pinnedNeeds {
		if _, ok := optional[path]; !ok {
			assert.Contains(t, needs, path, "pinned path %s is not guarded", path)
		}
	}
	for _, p := range patterns {
		if _, ok := unguardedRoutes[p]; ok {
			continue
		}
		covered := false
		for path := range needs {
			if path == p || strings.HasSuffix(p, "/") && strings.HasPrefix(path, p) {
				covered = true
			}
		}
		assert.True(t, covered, "mount %s holds no Need", p)
	}
	var paths []string
	for p := range needs {
		paths = append(paths, p)
	}
	return paths
}

// walkMatrix probes every guarded path crossed with every bearer, and checks each cell against
// the verdict DERIVED from the bearer's grant and the path's Need: 401 MGS9011 with no token,
// 401 MGS9001 for a token that is not a credential here, 403 MGS9015 for a credential whose
// grant is below the need, and admitted otherwise.
func walkMatrix(t *testing.T, base string, paths []string, bearers map[string]bearer) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	cells := 0
	for _, p := range paths {
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
	t.Logf("probed %d cells over %d paths", cells, len(paths))
}

// TestGrantMatrix walks every procedure and /api route of a loaded server crossed with every
// kind of bearer.
func TestGrantMatrix(t *testing.T) {
	base, patterns, needs, _, _ := bootConsoleServer(t)
	paths := checkNeeds(t, patterns, needs, mountedOnlyWhen)
	walkMatrix(t, base, paths, matrixBearers(t))
}

// A server whose workspace failed to load holds every path to the same Need as a loaded one:
// the failure (which names files) reaches only a caller who could have read the workspace.
func TestUnloadedGrantMatrix(t *testing.T) {
	base, patterns, needs := bootUnloadedServer(t)
	paths := checkNeeds(t, patterns, needs, nil)
	walkMatrix(t, base, paths, matrixBearers(t))
}

// probe sends one request to path as bearer token and returns the status and the MGS reason of
// a refusal, "" for anything else. It asks each path something no handler acts on, so an
// admitted cell has no side effect: a GET to a Connect procedure (Connect serves a GET only
// with a message, and a mutating procedure never), a PATCH to an /api route, and a JSON-RPC
// body /mcp rejects.
func probe(t *testing.T, client *http.Client, base, path, token string) (int, string) {
	t.Helper()
	method, body := http.MethodPatch, ""
	switch {
	case path == "/mcp":
		method, body = http.MethodPost, "{}"
	case !strings.HasPrefix(path, "/api/"):
		method = http.MethodGet
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	got, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	for _, code := range []string{"MGS9001", "MGS9011", "MGS9015"} {
		if bytes.Contains(got, []byte(code)) {
			return resp.StatusCode, code
		}
	}
	return resp.StatusCode, ""
}

// Secrets rest nowhere but with the caller: mint and revoke through the CLI's store and the
// TokenService, then search every file under the state and trail dirs, the server log, and
// every List response for each secret's bytes. The one file allowed to hold a secret is the
// operator token's own.
func TestSecretsNeverRestInTheTrailLogsOrLists(t *testing.T) {
	base, _, _, logs, cacheDir := bootConsoleServer(t)
	op, err := auth.LoadOperator()
	require.NoError(t, err)

	dir, err := auth.StoreDir()
	require.NoError(t, err)
	store, err := auth.LoadStore(dir)
	require.NoError(t, err)
	cliSecret, _, err := store.Mint(types.GrantOperator, auth.MintRequest{Name: "from-cli", Grant: types.GrantConnector, TTL: time.Hour})
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
	require.NoError(t, json.Unmarshal(call("CreateToken", `{"name":"from-console","grant":{"console":"LEVEL_WRITE"},"expireTime":"`+exp+`"}`), &minted))
	require.NotEmpty(t, minted.Secret)
	var doomed struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(call("CreateToken", `{"name":"doomed","grant":{"console":"LEVEL_READ"},"expireTime":"`+exp+`"}`), &doomed))
	call("RevokeToken", `{"name":"doomed"}`)
	list := call("ListTokens", `{}`)

	secrets := map[string]string{"operator": op, "cli mint": cliSecret, "console mint": minted.Secret, "revoked mint": doomed.Secret}
	for name, secret := range secrets {
		assert.NotContains(t, string(list), secret, "ListTokens carries the %s secret", name)
		assert.NotContains(t, logs.String(), secret, "the server log carries the %s secret", name)
	}
	opFile, err := auth.OperatorPath()
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
// server refuses to start, before it mints or serves anything.
func TestNonLoopbackBindWithoutOptInIsAnError(t *testing.T) {
	testkit.Isolate(t)
	d := New(mcp.Options{Version: "test", HTTPAddr: netip.MustParseAddrPort("0.0.0.0:0")})
	_, _, err := d.prepare(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp.insecure_bind")
	_, err = auth.LoadOperator()
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

// syncBuffer is a bytes.Buffer safe for the server's concurrent log writes.
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
