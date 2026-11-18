package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types"
)

func writeSpellsYAML(t *testing.T, root, path, tag string) {
	t.Helper()
	body := fmt.Sprintf(`spells:
  %q:
    tag: %s
`, path, tag)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(body), 0o644))
}

// A registry credential is a secret reference resolved through the workspace's
// provider, so the value is registered for redaction like any magus\secret.read.
func TestRegistryCredentialResolvesThroughTheSecretProvider(t *testing.T) {
	spells := config.SpellsConfig{Registries: []config.SpellRegistry{
		{Host: "ghcr.io", Username: "ci", PasswordRef: "SPELL_REGISTRY_TOKEN"},
	}}
	r := secrets.NewResolver()
	t.Setenv("SPELL_REGISTRY_TOKEN", "ghp_abcdef123456")

	user, pass, err := registryCredential(t.Context(), spells, "ghcr.io", r)
	require.NoError(t, err)
	assert.Equal(t, "ci", user)
	assert.Equal(t, "ghp_abcdef123456", pass.Reveal())
	red, err := r.RedactString("token=ghp_abcdef123456")
	require.NoError(t, err)
	assert.Equal(t, "token=***", red)

	_, _, err = registryCredential(t.Context(), spells, "docker.io", r)
	require.ErrorContains(t, err, "no entry for docker.io")

	unset := config.SpellsConfig{Registries: []config.SpellRegistry{{Host: "ghcr.io", Username: "ci", PasswordRef: "SPELL_REGISTRY_UNSET"}}}
	_, _, err = registryCredential(t.Context(), unset, "ghcr.io", secrets.NewResolver())
	require.ErrorContains(t, err, "$SPELL_REGISTRY_UNSET is not set")
}

func TestNewClientPicksItsCredentialSource(t *testing.T) {
	spells := config.SpellsConfig{Registries: []config.SpellRegistry{
		{Host: "ghcr.io", Username: "ci", PasswordRef: "SPELL_CLIENT_TOKEN"},
	}}
	t.Setenv("SPELL_CLIENT_TOKEN", "from-provider")
	r := secrets.NewResolver()
	login := Login{
		ReadToken: func(context.Context) (secrets.Secret, error) { return secrets.New("from-stdin"), nil },
		Secrets:   func(ctx context.Context) (context.Context, *secrets.Resolver, error) { return ctx, r, nil },
	}

	c, err := NewClient(t.Context(), nil, spells, "ghcr.io", "me", login)
	require.NoError(t, err)
	assert.Equal(t, oci.Credentials{Username: "me", Password: secrets.New("from-stdin")}, c.Credentials, "--username reads stdin")

	c, err = NewClient(t.Context(), nil, spells, "ghcr.io", "", login)
	require.NoError(t, err)
	assert.Equal(t, "ci", c.Credentials.Username)
	assert.Equal(t, "from-provider", c.Credentials.Password.Reveal(), "a declared host resolves through the provider")

	c, err = NewClient(t.Context(), nil, spells, "docker.io", "", login)
	require.NoError(t, err)
	assert.Zero(t, c.Credentials, "an undeclared host is anonymous")
}

// The frozen check passes once Relock has pinned every declared spell, and names a pin
// magus.yaml no longer declares.
func TestVerifyLock(t *testing.T) {
	reg := newRegistry(t)
	pushTag(t, reg, spellDir(t), "v1")
	path := reg.host() + "/team/spells/x"
	cfg := declare(path, "v1")
	root := t.TempDir()
	opts := fixtureOptions(reg, t.TempDir())

	require.ErrorIs(t, VerifyLock(t.Context(), root, cfg, opts), types.RemoteSpellLockStale, "nothing is pinned yet")

	require.NoError(t, UpdateLock(t.Context(), root, func(Lock) (Lock, error) { return Relock(t.Context(), cfg, opts) }))
	require.NoError(t, VerifyLock(t.Context(), root, cfg, opts))

	err := VerifyLock(t.Context(), root, config.SpellsConfig{}, opts)
	require.ErrorIs(t, err, types.RemoteSpellLockStale, "the lock names a spell no longer declared")
	assert.ErrorContains(t, err, path)
}

func TestSummarizeSortsPinsByPath(t *testing.T) {
	lock := Lock{Spells: map[string]LockEntry{"b/x": {Tag: "v2"}, "a/x": {Tag: "v1"}}}
	assert.Equal(t, []LockSummary{{Path: "a/x", Tag: "v1"}, {Path: "b/x", Tag: "v2"}}, Summarize(lock))
	assert.Equal(t, []LockSummary{}, Summarize(Lock{}), "an empty lock reports an empty list, not null")
}
