package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/cache/remotetest"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// cacheShard is a Go workspace wired the way a ci.yaml shard is: spells/github/actions
// as the remote tier, served by an emulator, reading ACTIONS_RUNTIME_TOKEN through the
// secret provider. Each call to run is a fresh process's worth of CLI state.
type cacheShard struct {
	root    string
	emu     *remotetest.GHA
	trusted string
	output  string
}

// ConfigCacheImportRecordSuite reads the -o json record of a remote import.
type ConfigCacheImportRecordSuite struct {
	suite.Suite
	shard *cacheShard
}

func TestConfigCacheImportRecordSuite(t *testing.T) {
	suite.Run(t, new(ConfigCacheImportRecordSuite))
}

func (s *ConfigCacheImportRecordSuite) SetupTest() {
	s.shard = newCacheShard(s.T())
	s.shard.sign(s.T())
}

func (s *ConfigCacheImportRecordSuite) importRecord() cache.SpellImportRecord {
	s.shard.output = "json"
	var err error
	stdout := captureStdout(s.T(), func() {
		_, err = s.shard.run(s.T(), "import", "--remote")
	})
	s.Require().NoError(err)
	var rec cache.SpellImportRecord
	s.Require().NoError(codec.Unmarshal([]byte(stdout), &rec), stdout)
	return rec
}

func (s *ConfigCacheImportRecordSuite) TestAMissRestoresNothing() {
	goCaches(s.T(), nil)
	rec := s.importRecord()
	s.False(rec.Restored)
	s.Require().Len(rec.Spells, 1)
	s.Equal(cache.SpellOutcome("cold"), rec.Spells[0].Outcome)
}

func (s *ConfigCacheImportRecordSuite) TestAnInactiveBackendRestoresNothing() {
	row := newSpellCacheRow(magus.SpellCache{Spell: "go", Inactive: true})
	s.Equal(cache.SpellOutcome("inactive"), row.Outcome)
}

func (s *ConfigCacheImportRecordSuite) TestAFailedImportStillPrintsItsRows() {
	goCaches(s.T(), nil)
	s.Require().NoError(os.WriteFile(filepath.Join(s.shard.root, "magusfile.buzz"),
		[]byte("import \"magus\";\nimport \"magus/spell/go\";\nmagus\\project({\"spells\": [go]});\n"), 0o644))
	s.shard.output = "json"
	var err error
	stdout := captureStdout(s.T(), func() {
		_, err = s.shard.run(s.T(), "import", "--remote")
	})
	s.Require().ErrorContains(err, "no remote backend configured")
	var rec cache.SpellImportRecord
	s.Require().NoError(codec.Unmarshal([]byte(stdout), &rec), stdout)
	s.False(rec.Restored)
	s.Len(rec.Spells, 1)
}

func (s *ConfigCacheImportRecordSuite) TestAnUntrustedBundleIsNamed() {
	goCaches(s.T(), map[string]string{"GOCACHE:0a/0abc-a": "compiled"})
	out, err := s.shard.run(s.T(), "export", "--remote")
	s.Require().NoError(err, out)
	s.shard.sign(s.T())
	goCaches(s.T(), nil)
	rec := s.importRecord()
	s.False(rec.Restored)
	s.Require().Len(rec.Spells, 1)
	s.Equal(cache.SpellOutcome("cold"), rec.Spells[0].Outcome)
	s.NotEmpty(rec.Spells[0].Refused)
}

func (s *ConfigCacheImportRecordSuite) TestARestoreSaysWhatItRestored() {
	goCaches(s.T(), map[string]string{"GOCACHE:0a/0abc-a": "compiled"})
	out, err := s.shard.run(s.T(), "export", "--remote")
	s.Require().NoError(err, out)
	goCaches(s.T(), nil)
	rec := s.importRecord()
	s.True(rec.Restored)
	s.Require().Len(rec.Spells, 1)
	row := rec.Spells[0]
	s.Equal(cache.SpellOutcome("restored"), row.Outcome)
	s.True(row.Exact)
	s.NotEmpty(row.Dirs)
	s.Positive(row.Transferred)
}

func newCacheShard(t *testing.T) *cacheShard {
	t.Helper()
	spell, err := os.ReadFile(filepath.Join("..", "..", "spells", "github", "actions", "spell.buzz"))
	require.NoError(t, err)
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                           "module cacheshard\n\ngo 1.25\n",
		"go.sum":                           "example.com/m v1.0.0 h1:AAAA=\n",
		"magusfile.buzz":                   "import \"magus\";\nimport \"magus/spell/go\";\nimport \"spells/github/actions\" as github;\nmagus\\project({\"spells\": [go]});\nmagus\\cache.remote(github);\n",
		"spells/github/actions/spell.buzz": string(spell),
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	// go writes telemetry here as cleanup runs; its mode file stops that.
	telemetry := filepath.Join(config, "go", "telemetry")
	require.NoError(t, os.MkdirAll(telemetry, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(telemetry, "mode"), []byte("off"), 0o644))
	t.Setenv("MAGUS_CACHE_DIR", t.TempDir())
	emu := remotetest.NewGHA()
	emu.Serve(t, "shard-token")
	return &cacheShard{root: root, emu: emu}
}

// sign sets the key a main shard signs with and trusts its public half.
func (s *cacheShard) sign(t *testing.T) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	t.Setenv("MAGUS_CACHE_SIGNING_KEY", base64.StdEncoding.EncodeToString(priv.Seed()))
	s.trusted = base64.StdEncoding.EncodeToString(pub)
}

// goCaches points GOCACHE and GOMODCACHE, the caches the go spell declares, at fresh
// directories holding files.
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
func (s *cacheShard) run(t *testing.T, args ...string) (string, error) {
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
	global.output = s.output
	var err error
	stderr := notices(t, func() {
		err = configCacheCommand(testkit.WithEnvResolver(context.Background()), s.root, args)
	})
	if magusValue != nil {
		_ = magusValue.Close()
		magusValue = nil
	}
	return stderr, err
}

// The remote tier is a spell handler that reads its token through magus\secret, so
// these drive the real one: an export and import that never reached a resolver failed
// on every CI shard while an in-memory backend passed. The caches are the ones the go
// spell declares, found where its sandbox grants locate them.
func TestConfigCacheRemoteRoundTripsTheSpellsCaches(t *testing.T) {
	s := newCacheShard(t)
	s.sign(t)
	goCaches(t, map[string]string{
		"GOCACHE:0a/0abc-a":                    "compiled",
		"GOMODCACHE:example.com/m@v1.0.0/m.go": "package m\n",
	})

	out, err := s.run(t, "export", "--remote")
	require.NoError(t, err, out)
	assert.Contains(t, out, "magus config cache export: stored go-")
	require.NotEmpty(t, s.emu.Committed(), "the bundle reached the service")
	for key := range s.emu.Committed() {
		assert.True(t, strings.HasPrefix(key, "magus-go-"), key)
	}
	for i, auth := range s.emu.TwirpAuths() {
		assert.Equal(t, "Bearer shard-token", auth, "Twirp call %d carried the resolved token", i)
	}

	gocache, gomodcache := goCaches(t, nil)
	out, err = s.run(t, "import", "--remote")
	require.NoError(t, err, out)
	assert.Contains(t, out, "(these lockfiles)")
	got, err := os.ReadFile(filepath.Join(gocache, "0a", "0abc-a"))
	require.NoError(t, err)
	assert.Equal(t, "compiled", string(got))
	got, err = os.ReadFile(filepath.Join(gomodcache, "example.com", "m@v1.0.0", "m.go"))
	require.NoError(t, err)
	assert.Equal(t, "package m\n", string(got))
}

// An empty tier leaves the build cold and exits 0 with the documented notice.
func TestConfigCacheRemoteMiss(t *testing.T) {
	s := newCacheShard(t)
	s.sign(t)
	goCaches(t, nil)

	out, err := s.run(t, "import", "--remote")
	require.NoError(t, err, out)
	assert.Contains(t, out, "magus config cache import: no verified go cache bundle in the remote tier for the last week (0 refused); builds start cold")
	assert.NotEmpty(t, s.emu.TwirpAuths(), "the miss was asked of the service, not assumed")
}

// A bundle signed by a key outside the trust set is named and never restored.
func TestConfigCacheRemoteRefusesAnUntrustedBundle(t *testing.T) {
	s := newCacheShard(t)
	s.sign(t)
	goCaches(t, map[string]string{"GOCACHE:0a/0abc-a": "compiled"})
	out, err := s.run(t, "export", "--remote")
	require.NoError(t, err, out)

	s.sign(t)
	gocache, _ := goCaches(t, nil)
	out, err = s.run(t, "import", "--remote")
	require.NoError(t, err, out)
	assert.Regexp(t, `no verified go cache bundle in the remote tier for the last week \([1-9]\d* refused\); builds start cold`, out)
	assert.NoFileExists(t, filepath.Join(gocache, "0a", "0abc-a"))
}

// Only a holder of the signing key writes the remote tier: without one there is no
// bundle to refuse later, since none is stored.
func TestConfigCacheRemoteExportNeedsTheSigningKey(t *testing.T) {
	s := newCacheShard(t)
	t.Setenv("MAGUS_CACHE_SIGNING_KEY", "")
	goCaches(t, map[string]string{"GOCACHE:0a/0abc-a": "compiled"})

	out, err := s.run(t, "export", "--remote")
	require.Error(t, err, out)
	assert.Empty(t, s.emu.Committed(), "nothing unsigned reaches the service")
}

// The flags a bundle once took are gone, and --remote and --to name one destination.
func TestConfigCacheExportAndImportTakeNoCacheSelector(t *testing.T) {
	s := newCacheShard(t)
	for _, args := range [][]string{
		{"export", "--toolchain", "go", "--remote"},
		{"export", "--remote", "--used-within", "12h"},
		{"import", "--toolchain", "go", "--remote"},
	} {
		_, err := s.run(t, args...)
		require.ErrorContains(t, err, "flag provided but not defined", "%v", args)
	}
	_, err := s.run(t, "export", "--remote", "--to", "x")
	require.ErrorContains(t, err, "--remote and --to are exclusive")
	_, err = s.run(t, "import", "--remote", "x")
	require.ErrorContains(t, err, "--remote reads no file")
}

// withOutputFlags sets the package-level display flags for one test and restores
// them, since every command reads them off `global` rather than taking them as
// arguments.
func withOutputFlags(t *testing.T, format, tee string) {
	t.Helper()
	prevOut, prevTee := global.output, global.tee.Path
	global.output, global.tee.Path = format, tee
	t.Cleanup(func() { global.output, global.tee.Path = prevOut, prevTee })
}

// The seed goes to a secret store over a pipe, so stdout must carry the seed and
// NOTHING else: no prose, and no trailing newline. `gh secret set` stores stdin
// verbatim, so one stray byte becomes part of the secret and the base64 decode on
// the other end fails at signing time, a long way from the cause.
func TestCacheKeyGenerateTemplateEmitsOnlyTheSeed(t *testing.T) {
	withOutputFlags(t, "template={{.seed}}", "")

	var err error
	out := captureStdout(t, func() {
		_ = notices(t, func() { err = configCacheKeyGenerate(t.Context(), nil) })
	})
	require.NoError(t, err)

	assert.NotContains(t, out, "\n", "stdout must be the bare seed; a newline would be stored as part of the secret")
	assert.NotContains(t, out, "SECRET", "the warning banner belongs on stderr, not in the piped value")
	raw, decErr := base64.StdEncoding.DecodeString(out)
	require.NoError(t, decErr, "stdout must decode as the base64 seed exactly as emitted")
	assert.Len(t, raw, 32, "an Ed25519 seed is 32 bytes")
}

// The keyid and public key still have to reach a human who is piping the seed
// away unseen (they are what you paste into magus.yaml), so they move to stderr
// rather than being suppressed.
func TestCacheKeyGenerateTemplateKeepsPublicHalfOnStderr(t *testing.T) {
	withOutputFlags(t, "template={{.keyid}}", "")

	var err error
	errOut := notices(t, func() {
		_ = captureStdout(t, func() { err = configCacheKeyGenerate(t.Context(), nil) })
	})
	require.NoError(t, err)

	assert.Contains(t, errOut, "keyid:")
	assert.Contains(t, errOut, "trusted_keys")
}

// --tee mirrors structured output into a FILE. Every other command wants that;
// here it would write a signing key to disk, which is the one thing this command
// is built never to do. Refused rather than ignored, so nobody believes they have
// a backup copy they do not have.
func TestCacheKeyGenerateRefusesTee(t *testing.T) {
	withOutputFlags(t, "json", "keys.json")

	err := configCacheKeyGenerate(t.Context(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not come to rest on disk")
}

// seedOf pulls the base64 seed back out of the rendered banner: it is the one line
// that decodes to 32 bytes.
func findSeed(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if raw, err := base64.StdEncoding.DecodeString(line); err == nil && len(raw) == 32 {
			return line
		}
	}
	t.Fatal("no 32-byte base64 line in the output; the seed is not on a line of its own")
	return ""
}

// The default is a human who will read the seed once and paste it into a password
// manager, so the prose banner stays. Every assertion here is a property someone
// copying from a terminal depends on, not a spelling.
func TestCacheKeyGenerateDefaultStaysHumanReadable(t *testing.T) {
	withOutputFlags(t, "", "")

	var err error
	out := captureStdout(t, func() { err = configCacheKeyGenerate(t.Context(), nil) })
	require.NoError(t, err)

	assert.Contains(t, out, "keyid  ")
	// The seed sits BARE on its own line, not after "VAR=", which is awkward to
	// select and invites pasting it into an export, straight into shell history.
	assert.Contains(t, out, fmt.Sprintf(`

%s

`, findSeed(t, out)), "the seed must be alone on its line")
	assert.NotContains(t, out, "MAGUS_CACHE_SIGNING_KEY=", "the seed must not be rendered as an assignment")
	// Column zero, or the snippet does not paste into magus.yaml.
	assert.Contains(t, out, "\ncache:\n  remote:\n    trusted_keys:\n")
	assert.Contains(t, out, "gh secret set MAGUS_CACHE_SIGNING_KEY", "the next command is spelled out")
	// Plain ASCII: the old layout drew a box, which this repo's own rule forbids in
	// user-facing strings and which renders badly in a plain terminal.
	for _, r := range out {
		assert.Less(t, r, rune(128), "user-facing output must be ASCII, found %q", r)
	}
}
