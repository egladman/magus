package bindings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/spells/gen/spellstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSpellSecretProviderInvokesResolveSecretWithTheReference(t *testing.T) {
	d := spellstest.NewMockDriver(t)
	// The reference is passed through verbatim; magus never parses it.
	d.EXPECT().
		Invoke(mock.Anything, spells.InvokeRequest{Target: "resolve_secret", Params: map[string]any{"ref": "Private/DockerHub/token"}}).
		Return(spells.InvokeResponse{Text: "value-from-spell"}, nil).
		Once()
	v, err := spellSecretProvider{drv: d}.Fetch(context.Background(), "Private/DockerHub/token")
	require.NoError(t, err)
	assert.Equal(t, "value-from-spell", v.Reveal())
}

func TestSpellSecretProviderAcceptsAStringInData(t *testing.T) {
	// A spell whose op returns a value rather than printing it lands in Data. Documented
	// alongside the Text form in the adapter's contract.
	d := spellstest.NewMockDriver(t)
	d.EXPECT().Invoke(mock.Anything, mock.Anything).Return(spells.InvokeResponse{Data: "value-in-data"}, nil).Once()
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
			d := spellstest.NewMockDriver(t)
			d.EXPECT().Invoke(mock.Anything, mock.Anything).Return(resp, nil).Once()
			_, err := spellSecretProvider{drv: d}.Fetch(context.Background(), "ref")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "resolve_secret",
				"the error must name the op the spell got wrong")
		})
	}
}

func TestSpellSecretProviderPropagatesTheSpellError(t *testing.T) {
	want := errors.New("vault is sealed")
	d := spellstest.NewMockDriver(t)
	d.EXPECT().Invoke(mock.Anything, mock.Anything).Return(spells.InvokeResponse{}, want).Once()
	_, err := spellSecretProvider{drv: d}.Fetch(context.Background(), "ref")
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
type slowDriver struct{}

func (*slowDriver) Name() string { return "slow" }

func (d *slowDriver) Invoke(ctx context.Context, _ spells.InvokeRequest) (spells.InvokeResponse, error) {
	<-ctx.Done()
	return spells.InvokeResponse{}, ctx.Err()
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
