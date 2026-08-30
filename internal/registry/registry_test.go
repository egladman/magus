package registry

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolate points config and cache at temp dirs so no test touches the real ones.
func isolate(t *testing.T) (cfg, cache string) {
	t.Helper()
	cfg, cache = t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("MAGUS_OFFLINE", "")
	return cfg, cache
}

// writeSource drops a registry.d file and its pinned key, returning the private half.
func writeSource(t *testing.T, cfg, name, url string, extra string) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	dir := filepath.Join(cfg, "magus", "registry.d")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	keyPath := filepath.Join(dir, name+".pub")
	require.NoError(t, os.WriteFile(keyPath, []byte(hex.EncodeToString(pub)), 0o600))
	body := fmt.Sprintf("url: %s\npubkey: %s\n%s", url, keyPath, extra)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600))
	return priv
}

// signedRegistry serves a registry and its detached signature.
func signedRegistry(t *testing.T, priv ed25519.PrivateKey, reg Registry) *httptest.Server {
	t.Helper()
	data, err := json.Marshal(reg)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, data)
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
	mux.HandleFunc("/index.json.sig", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(sig) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func fresh() Registry {
	return Registry{
		SchemaVersion: schemaVersion,
		GeneratedAt:   time.Now().Add(-time.Hour),
		EOL: map[string]Product{
			"nodejs": {Label: "Node.js", Cycles: []Cycle{{Cycle: "24", EOL: "2028-04-30", LTS: true}}},
		},
	}
}

func TestRefreshVerifiesAndCaches(t *testing.T) {
	cfg, _ := isolate(t)
	priv := writeSource(t, cfg, "magus", "https://example.invalid/index.json", "")
	srv := signedRegistry(t, priv, fresh())

	sources, err := LoadSources()
	require.NoError(t, err)
	require.Len(t, sources, 1)
	sources[0].URL = srv.URL + "/index.json" // loopback, so https is not required

	got, err := Refresh(context.Background(), sources[0], srv.Client())
	require.NoError(t, err)
	assert.Equal(t, StateFresh, got.State)
	require.NotNil(t, got.Registry)
	assert.Equal(t, "Node.js", got.Registry.EOL["nodejs"].Label)
	assert.FileExists(t, got.Path)

	// Rule 2: a plain Load reads that cache and sends nothing.
	cached, err := Load()
	require.NoError(t, err)
	require.Len(t, cached, 1)
	assert.Equal(t, StateFresh, cached[0].State)
	assert.Equal(t, "Node.js", cached[0].Registry.EOL["nodejs"].Label)
}

// TestRefreshRefusesAForeignSignature: the pinned key is the whole point, so a
// document signed by anything else must not reach the cache.
func TestRefreshRefusesAForeignSignature(t *testing.T) {
	cfg, _ := isolate(t)
	writeSource(t, cfg, "magus", "https://example.invalid/index.json", "")
	_, attacker, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	srv := signedRegistry(t, attacker, fresh())

	sources, err := LoadSources()
	require.NoError(t, err)
	sources[0].URL = srv.URL + "/index.json"

	_, err = Refresh(context.Background(), sources[0], srv.Client())
	require.ErrorContains(t, err, "signature does not match")

	cached, err := Load()
	require.NoError(t, err)
	assert.Equal(t, StateNeverSynced, cached[0].State, "a rejected document must not be persisted")
}

// TestStalenessIsMeasuredFromGeneratedAt is the rollback hole the design names: a
// two-year-old file fetched today is still two years old, and measuring from the
// fetch would call it current.
func TestStalenessIsMeasuredFromGeneratedAt(t *testing.T) {
	cfg, _ := isolate(t)
	priv := writeSource(t, cfg, "magus", "https://example.invalid/index.json", "stale_after: 24h\n")
	old := fresh()
	old.GeneratedAt = time.Now().Add(-400 * 24 * time.Hour)
	srv := signedRegistry(t, priv, old)

	sources, err := LoadSources()
	require.NoError(t, err)
	sources[0].URL = srv.URL + "/index.json"

	got, err := Refresh(context.Background(), sources[0], srv.Client())
	require.NoError(t, err, "an old file is valid, just old")
	assert.Equal(t, StateStale, got.State, "freshly fetched and still stale")
	assert.Greater(t, got.Age, 300*24*time.Hour)
}

func TestRefreshRefusesAnExpiredRegistry(t *testing.T) {
	cfg, _ := isolate(t)
	reg := fresh()
	reg.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	priv := writeSource(t, cfg, "magus", "https://example.invalid/index.json", "")
	srv := signedRegistry(t, priv, reg)

	sources, err := LoadSources()
	require.NoError(t, err)
	sources[0].URL = srv.URL + "/index.json"

	_, err = Refresh(context.Background(), sources[0], srv.Client())
	require.ErrorContains(t, err, "expired at")
}

// TestOfflineIsANamedRefusal: the variable is only worth having if you can tell it
// worked, so it must not degrade into a silent skip.
func TestOfflineIsANamedRefusal(t *testing.T) {
	cfg, _ := isolate(t)
	writeSource(t, cfg, "magus", "https://example.invalid/index.json", "")
	t.Setenv("MAGUS_OFFLINE", "1")

	sources, err := LoadSources()
	require.NoError(t, err)
	_, err = Refresh(context.Background(), sources[0], nil)
	require.ErrorIs(t, err, ErrOffline)
	require.ErrorContains(t, err, "example.invalid", "the refusal names what it would have fetched")
}

func TestSourceValidation(t *testing.T) {
	cfg, _ := isolate(t)
	dir := filepath.Join(cfg, "magus", "registry.d")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	cases := []struct{ name, body, wantErr string }{
		{"nourl", "pubkey: /tmp/k\n", "declares no url"},
		{"nokey", "url: https://example.invalid/i.json\n", "declares no pubkey"},
		{"plaintext", "url: http://example.invalid/i.json\npubkey: /tmp/k\n", "must be https"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, c.name+".yaml")
			require.NoError(t, os.WriteFile(path, []byte(c.body), 0o600))
			t.Cleanup(func() { _ = os.Remove(path) })
			_, err := LoadSources()
			require.ErrorContains(t, err, c.wantErr)
		})
	}
}

// TestDisabledSourceIsSilent: someone who declined on purpose must not be told to
// sync forever, which is the nag this whole area exists to avoid.
func TestDisabledSourceIsSilent(t *testing.T) {
	cfg, _ := isolate(t)
	writeSource(t, cfg, "magus", "https://example.invalid/index.json", "enabled: false\n")

	sources, err := LoadSources()
	require.NoError(t, err)
	assert.Empty(t, sources)

	cached, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cached)
}

// TestBuiltinSourceShipsByDefault: a fresh install knows where the registry is,
// the same way it knows the console and update URLs, and still contacts nobody
// until asked. A default SOURCE is not a default FETCH.
func TestBuiltinSourceShipsByDefault(t *testing.T) {
	isolate(t)
	sources, err := LoadSources()
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, builtinSourceName, sources[0].Name)
	assert.Equal(t, defaultRegistryURL, sources[0].URL)
	assert.True(t, sources[0].Builtin)
	assert.Empty(t, sources[0].PubKey, "the built-in source verifies against the pinned ring")

	cached, err := Load()
	require.NoError(t, err)
	require.Len(t, cached, 1)
	assert.Equal(t, StateNeverSynced, cached[0].State, "shipped, not synced")
}

// TestBuiltinSourceIsReplacedNotDuplicated: pointing the default at a mirror is one
// file. Adding a second entry for the same registry would double every fetch and
// give two caches of one fact.
func TestBuiltinSourceIsReplacedNotDuplicated(t *testing.T) {
	cfg, _ := isolate(t)
	writeSource(t, cfg, builtinSourceName, "https://mirror.invalid/index.json", "")

	sources, err := LoadSources()
	require.NoError(t, err)
	require.Len(t, sources, 1, "a file named for the built-in source replaces it")
	assert.Equal(t, "https://mirror.invalid/index.json", sources[0].URL)
}

// TestBuiltinSourceMirrorNeedsNoKeyOfItsOwn: a mirror serves the bytes we signed, so
// requiring it to declare a key would be asking for a key it does not have.
func TestBuiltinSourceMirrorNeedsNoKeyOfItsOwn(t *testing.T) {
	cfg, _ := isolate(t)
	dir := filepath.Join(cfg, "magus", "registry.d")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, builtinSourceName+".yaml"),
		[]byte("url: https://mirror.invalid/index.json\n"), 0o600))

	sources, err := LoadSources()
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.True(t, sources[0].Builtin, "it inherits the pinned ring")
}

// TestBuiltinSourceCanBeDeclined: someone who does not want it says so once, and is
// never told to sync again.
func TestBuiltinSourceCanBeDeclined(t *testing.T) {
	cfg, _ := isolate(t)
	dir := filepath.Join(cfg, "magus", "registry.d")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, builtinSourceName+".yaml"),
		[]byte("url: https://unused.invalid/i.json\nenabled: false\n"), 0o600))

	sources, err := LoadSources()
	require.NoError(t, err)
	assert.Empty(t, sources, "declining the built-in source leaves nothing configured")
}

// TestBuiltinSourceRefusesWithoutAPinnedKey is the state this build ships in: the
// registry keypair does not exist yet, so the ring is empty and a refresh must say
// exactly that rather than fetch something it cannot check.
func TestBuiltinSourceRefusesWithoutAPinnedKey(t *testing.T) {
	if len(pinnedKeys) > 0 {
		t.Skip("a registry key is pinned now; this covers the window before that")
	}
	isolate(t)
	sources, err := LoadSources()
	require.NoError(t, err)
	require.Len(t, sources, 1)

	_, err = sources[0].Keys()
	require.ErrorContains(t, err, "pins no registry key")
	require.ErrorContains(t, err, "could be checked", "the error says why it refused, not just that it did")
}

// TestRegistryURLEnvOverridesTheBuiltin mirrors MAGUS_UPDATE_URL: pointing at a
// mirror is a machine fact, so it is reachable without editing a file.
func TestRegistryURLEnvOverridesTheBuiltin(t *testing.T) {
	isolate(t)
	t.Setenv(registryURLEnv, "https://env-mirror.invalid/index.json")
	sources, err := LoadSources()
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, "https://env-mirror.invalid/index.json", sources[0].URL)
}

// TestLoadDegradesRatherThanFailing: a corrupt cache is recoverable by refreshing,
// and must not take down a command that only wanted to print a column.
func TestLoadDegradesRatherThanFailing(t *testing.T) {
	cfg, cache := isolate(t)
	writeSource(t, cfg, "magus", "https://example.invalid/index.json", "")
	dir := filepath.Join(cache, "magus", "registry")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.json"), []byte("{not json"), 0o644))

	cached, err := Load()
	require.NoError(t, err)
	require.Len(t, cached, 1)
	assert.Equal(t, StateNeverSynced, cached[0].State)
}
