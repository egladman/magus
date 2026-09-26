package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache/remotetest"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// toolchainShard is a workspace wired the way a ci.yaml shard is: spells/github/actions
// as the remote tier, served by an emulator, reading ACTIONS_RUNTIME_TOKEN through the
// secret provider. Each call to run is a fresh process's worth of CLI state.
type toolchainShard struct {
	root    string
	emu     *remotetest.GHA
	trusted string
}

func newToolchainShard(t *testing.T) *toolchainShard {
	t.Helper()
	spell, err := os.ReadFile(filepath.Join("..", "..", "spells", "github", "actions", "spell.buzz"))
	require.NoError(t, err)
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                           "module toolchainshard\n\ngo 1.25\n",
		"go.sum":                           "example.com/m v1.0.0 h1:AAAA=\n",
		"magusfile.buzz":                   "import \"magus\";\nimport \"spells/github/actions\" as github;\nmagus\\cache.remote(github);\n",
		"spells/github/actions/spell.buzz": string(spell),
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAGUS_CACHE_DIR", t.TempDir())
	emu := remotetest.NewGHA()
	emu.Serve(t, "shard-token")
	return &toolchainShard{root: root, emu: emu}
}

// sign sets the key a main shard signs with and trusts its public half.
func (s *toolchainShard) sign(t *testing.T) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	t.Setenv(signingKeyEnv, base64.StdEncoding.EncodeToString(priv.Seed()))
	s.trusted = base64.StdEncoding.EncodeToString(pub)
}

// goCaches points GOCACHE and GOMODCACHE at fresh directories holding files.
func goCaches(t *testing.T, files map[string]string) (gocache, gomodcache string) {
	t.Helper()
	gocache, gomodcache = t.TempDir(), t.TempDir()
	for rel, body := range files {
		dir, rest, _ := strings.Cut(rel, ":")
		base := map[string]string{"GOCACHE": gocache, "GOMODCACHE": gomodcache}[dir]
		path := filepath.Join(base, filepath.FromSlash(rest))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	t.Setenv("GOCACHE", gocache)
	t.Setenv("GOMODCACHE", gomodcache)
	return gocache, gomodcache
}

// run drives `magus config cache <args>` and returns what it wrote to stderr.
func (s *toolchainShard) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	restore := snapshotGlobals()
	resetStartupSingletons()
	t.Cleanup(func() {
		if magusValue != nil {
			_ = magusValue.Close()
		}
		resetStartupSingletons()
		restore()
	})
	globalCfg = config.Defaults()
	globalCfg.Broker = types.BrokerOff
	globalCfg.Server.Enabled = false
	if s.trusted != "" {
		globalCfg.Cache.Remote.TrustedKeys = []string{s.trusted}
	}
	var err error
	stderr := captureStderr(t, func() {
		err = configCacheCmd(context.Background(), s.root, args)
	})
	if magusValue != nil {
		_ = magusValue.Close()
		magusValue = nil
	}
	return stderr, err
}

// The remote tier is a spell handler that reads its token through magus\secret, so
// these drive the real one: an export and import that never reached a resolver failed
// on every CI shard while an in-memory backend passed.
func TestConfigCacheToolchainRemoteRoundTrip(t *testing.T) {
	s := newToolchainShard(t)
	s.sign(t)
	goCaches(t, map[string]string{
		"GOCACHE:0a/0abc-a":                    "compiled",
		"GOMODCACHE:example.com/m@v1.0.0/m.go": "package m\n",
	})

	out, err := s.run(t, "export", "--toolchain", "go", "--remote")
	require.NoError(t, err, out)
	assert.Contains(t, out, "magus config cache export: stored ")
	require.NotEmpty(t, s.emu.Committed(), "the bundle reached the service")
	for key := range s.emu.Committed() {
		assert.True(t, strings.HasPrefix(key, "magus-go-"), key)
	}
	for i, auth := range s.emu.TwirpAuths() {
		assert.Equal(t, "Bearer shard-token", auth, "Twirp call %d carried the resolved token", i)
	}

	gocache, gomodcache := goCaches(t, nil)
	out, err = s.run(t, "import", "--toolchain", "go", "--remote")
	require.NoError(t, err, out)
	assert.Contains(t, out, "(this module set)")
	got, err := os.ReadFile(filepath.Join(gocache, "0a", "0abc-a"))
	require.NoError(t, err)
	assert.Equal(t, "compiled", string(got))
	got, err = os.ReadFile(filepath.Join(gomodcache, "example.com", "m@v1.0.0", "m.go"))
	require.NoError(t, err)
	assert.Equal(t, "package m\n", string(got))
}

// An empty tier leaves the build cold and exits 0 with the documented notice.
func TestConfigCacheToolchainRemoteMiss(t *testing.T) {
	s := newToolchainShard(t)
	s.sign(t)
	goCaches(t, nil)

	out, err := s.run(t, "import", "--toolchain", "go", "--remote")
	require.NoError(t, err, out)
	assert.Contains(t, out, "magus config cache import: no verified go toolchain bundle in the remote tier for the last week (0 refused); builds start cold")
	assert.NotEmpty(t, s.emu.TwirpAuths(), "the miss was asked of the service, not assumed")
}

// A bundle signed by a key outside the trust set is named and never restored.
func TestConfigCacheToolchainRemoteRefusesAnUntrustedBundle(t *testing.T) {
	s := newToolchainShard(t)
	s.sign(t)
	goCaches(t, map[string]string{"GOCACHE:0a/0abc-a": "compiled"})
	out, err := s.run(t, "export", "--toolchain", "go", "--remote")
	require.NoError(t, err, out)

	s.sign(t)
	gocache, _ := goCaches(t, nil)
	out, err = s.run(t, "import", "--toolchain", "go", "--remote")
	require.NoError(t, err, out)
	assert.Regexp(t, `no verified go toolchain bundle in the remote tier for the last week \([1-9]\d* refused\); builds start cold`, out)
	assert.NoFileExists(t, filepath.Join(gocache, "0a", "0abc-a"))
}
