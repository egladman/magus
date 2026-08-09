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
		SchemaVersion: SchemaVersion,
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

// TestNoSourcesIsAWorkingState: a fresh install contacts nobody, and says so
// rather than failing.
func TestNoSourcesIsAWorkingState(t *testing.T) {
	isolate(t)
	cached, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cached)
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
