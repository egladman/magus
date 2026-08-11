package bindings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDriver is a spells.Driver that records what it was invoked with and replies with a
// canned response, so the adapter can be exercised without a Buzz VM.
type stubDriver struct {
	resp spells.InvokeResponse
	err  error
	got  spells.InvokeRequest
}

func (d *stubDriver) Name() string { return "stub" }

func (d *stubDriver) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	d.got = req
	return d.resp, d.err
}

func TestSpellSecretProviderInvokesResolveSecretWithTheReference(t *testing.T) {
	d := &stubDriver{resp: spells.InvokeResponse{Text: "value-from-spell"}}
	v, err := spellSecretProvider{drv: d}.Fetch(context.Background(), "Private/DockerHub/token")
	require.NoError(t, err)
	assert.Equal(t, "value-from-spell", v.Reveal())

	assert.Equal(t, "resolve_secret", d.got.Target)
	assert.Equal(t, map[string]any{"ref": "Private/DockerHub/token"}, d.got.Params,
		"the reference is passed through verbatim; magus never parses it")
}

func TestSpellSecretProviderAcceptsAStringInData(t *testing.T) {
	// A spell whose op returns a value rather than printing it lands in Data. Documented
	// alongside the Text form in the adapter's contract.
	d := &stubDriver{resp: spells.InvokeResponse{Data: "value-in-data"}}
	v, err := spellSecretProvider{drv: d}.Fetch(context.Background(), "ref")
	require.NoError(t, err)
	assert.Equal(t, "value-in-data", v.Reveal())
}

func TestSpellSecretProviderRejectsAnEmptyOrWrongTypedResult(t *testing.T) {
	for name, resp := range map[string]spells.InvokeResponse{
		"nothing at all":  {},
		"non-string data": {Data: 42},
		"empty text":      {Text: ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := spellSecretProvider{drv: &stubDriver{resp: resp}}.Fetch(context.Background(), "ref")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "resolve_secret",
				"the error must name the op the spell got wrong")
		})
	}
}

func TestSpellSecretProviderPropagatesTheSpellError(t *testing.T) {
	want := errors.New("vault is sealed")
	_, err := spellSecretProvider{drv: &stubDriver{err: want}}.Fetch(context.Background(), "ref")
	require.ErrorIs(t, err, want)
}

func TestOpenSpellSecretProviderRejectsAnUnregisteredSpell(t *testing.T) {
	_, err := openSpellSecretProvider(context.Background(), "no-such-spell-here")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-spell-here")
	assert.Contains(t, err.Error(), "import it in the magusfile",
		"the error carries the remediation, since a missing import is the usual cause")
}

// slowDriver blocks until its context is cancelled, standing in for a provider waiting on
// an unlock nobody is there to complete.
type slowDriver struct{ stubDriver }

func (d *slowDriver) Invoke(ctx context.Context, _ spells.InvokeRequest) (spells.InvokeResponse, error) {
	<-ctx.Done()
	return spells.InvokeResponse{}, ctx.Err()
}

// TestSpellSecretProviderFailsFastWithoutATerminal pins the policy that matters most in
// CI: a provider that would prompt cannot prompt, so waiting is pointless. Without a TTY
// magus gives it the SHORT budget and fails with a message naming the real problem -
// "there is nobody to ask" - rather than "timed out", which invites a retry that will
// fail the same way. Go tests run without a terminal, so this is the non-interactive path
// by construction.
func TestSpellSecretProviderFailsFastWithoutATerminal(t *testing.T) {
	r := secret.New(secret.WithTimeouts(secret.Timeouts{Interactive: 2 * time.Second, Unattended: 50 * time.Millisecond}))
	ctx := secret.ContextWithResolver(context.Background(), r)

	start := time.Now()
	_, err := spellSecretProvider{drv: &slowDriver{}, name: "onepassword"}.
		Fetch(ctx, "Private/Thing/token")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no terminal to prompt on")
	assert.Contains(t, err.Error(), "onepassword", "the error names which provider stalled")
	assert.Less(t, elapsed, time.Second,
		"it must use the short non-interactive budget, not the interactive one")
}
