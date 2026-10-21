package bindings

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpellSecretProviderHandsTheReferenceThroughVerbatim(t *testing.T) {
	// The reference is passed through verbatim; magus never parses it.
	drv := buzzProvider(t, `
export fun resolve_secret(target: Target, req: SecretRef) > Secret {
    return Secret{ value = "value-for:" + req.ref };
}`)
	v, err := spellSecretProvider{drv: drv, name: "vault"}.Fetch(context.Background(), "Private/DockerHub/token")
	require.NoError(t, err)
	assert.Equal(t, "value-for:Private/DockerHub/token", v.Reveal())
}

func TestSpellSecretProviderRejectsAMissingOrEmptySecret(t *testing.T) {
	for name, src := range map[string]string{
		"no resolve_secret": `export fun mgs_getName() > str { return "vault"; }`,
		"an empty value": `
export fun resolve_secret(target: Target, req: SecretRef) > Secret { return Secret{}; }`,
		"a bare string": `
export fun resolve_secret(target: Target, req: SecretRef) > any { return "value"; }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := spellSecretProvider{drv: buzzProvider(t, src), name: "vault"}.Fetch(context.Background(), "ref")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "resolve_secret", "the error must name the op the spell got wrong")
		})
	}
}

func TestSpellSecretProviderPropagatesTheSpellError(t *testing.T) {
	drv := buzzProvider(t, `
export fun resolve_secret(target: Target, req: SecretRef) > Secret !> any {
    throw "vault is sealed";
}`)
	_, err := spellSecretProvider{drv: drv, name: "vault"}.Fetch(context.Background(), "ref")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault is sealed")
}

func TestOpenSpellSecretProviderRejectsAnUnregisteredSpell(t *testing.T) {
	_, err := openSpellSecretProvider(context.Background(), "no-such-spell-here")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-spell-here")
	assert.Contains(t, err.Error(), "import it in the magusfile",
		"the error carries the remediation, since a missing import is the usual cause")
}

// TestSpellSecretProviderFailsFastWithoutATerminal pins the policy that matters most in
// CI: a provider that would prompt cannot prompt, so waiting is pointless. Without a TTY
// magus gives it the SHORT budget and fails with a message naming the real problem
// ("there is nobody to ask") rather than "timed out", which invites a retry that will
// fail the same way. Go tests run without a terminal, so this is the non-interactive path
// by construction.
func TestSpellSecretProviderFailsFastWithoutATerminal(t *testing.T) {
	r := secret.New(secret.WithTimeouts(secret.Timeouts{Interactive: 2 * time.Second, Unattended: 50 * time.Millisecond}))
	ctx := secret.ContextWithResolver(context.Background(), r)
	// Spins until the deadline cancels the VM, standing in for a provider waiting on an
	// unlock nobody is there to complete.
	drv := buzzProvider(t, `
export fun resolve_secret(target: Target, req: SecretRef) > Secret {
    var n = 0;
    while (n >= 0) {
        n = n + 1;
    }
    return Secret{};
}`)

	start := time.Now()
	_, err := spellSecretProvider{drv: drv, name: "onepassword"}.Fetch(ctx, "Private/Thing/token")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no terminal to prompt on")
	assert.Contains(t, err.Error(), "onepassword", "the error names which provider stalled")
	assert.Less(t, elapsed, time.Second,
		"it must use the short non-interactive budget, not the interactive one")
}
