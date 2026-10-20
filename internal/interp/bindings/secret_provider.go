package bindings

import (
	"context"
	"fmt"
	"io"

	interactivepkg "github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// This file lives in the bindings layer (not internal/secret) because a spell-backed
// secret provider needs the Buzz VM to run the spell's handler op. It registers itself
// with the secret package at init, so core can resolve through a spell provider without
// linking the VM: the same indirection ci_provider.go and remote_cache.go use.
func init() { //nolint:gochecknoinits // internal/secret cannot link the Buzz VM; the opener registers from the layer that can
	secret.RegisterProviderOpener(openSpellSecretProvider)
}

// openSpellSecretProvider resolves the selected spell by name and adapts it to
// [secret.Provider].
func openSpellSecretProvider(_ context.Context, name string) (secret.Provider, error) {
	drv, ok := project.DefaultSpellRegistry().Lookup(name)
	if !ok {
		return nil, fmt.Errorf("spell %q is not registered, import it in the magusfile before selecting it", name)
	}
	return spellSecretProvider{drv: drv, name: name}, nil
}

// spellSecretProvider adapts a spell to [secret.Provider]. The spell is an ordinary
// magus spell authored in Buzz, exposing one provider op:
//
//	resolve_secret(target: Target, req: SecretRef) > Secret   throws if unresolvable
//
// The adapter has no provider knowledge, so the binary stays backend-agnostic and the
// reference is passed through verbatim. See docs/concepts/secrets.md for why there is no
// URI scheme.
type spellSecretProvider struct {
	drv  spells.Driver
	name string // for the waiting announcement and the timeout errors
}

// Fetch calls the spell's resolve_secret op and returns the value it produced. The value
// decodes straight into a Secret and is wrapped at once, so it never passes through
// InvokeResponse.Data or the JSON encoder that serializes Data for MCP.
func (p spellSecretProvider) Fetch(ctx context.Context, ref string) (secret.Value, error) {
	// ANNOUNCED BEFORE THE PROVIDER RUNS, not after. A 1Password prompt appearing during
	// what looked like an ordinary build, with nothing on screen explaining it, is a
	// trust failure: the user cannot tell whether magus asked for it or something else
	// on their machine did. This line is the accountability: what is waiting, what it
	// wants, and how long before it gives up.
	//
	// The audit event (journal KindSecret) records the read AFTER it succeeds; this is
	// the before half, and only this one can be seen while the prompt is on screen.
	// Budgets from the run's resolver, which carries what magus.yaml configured.
	budgets := secret.ResolverFromContext(ctx).Timeouts()
	interactive := tty.StdinIsTerminal()
	timeout := budgets.Interactive
	if !interactive {
		timeout = budgets.Unattended
	}
	interactivepkg.Hint(ctx, fmt.Sprintf("secret: waiting on %s for %q (timeout %s)",
		p.name, ref, timeout))

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Silenced for the duration of the provider's own subprocess. Its stdout IS the
	// credential, and it streams BEFORE the resolver has any value to redact: the one
	// exec guaranteed to contain a secret is the one redaction structurally cannot
	// cover, because the value is unknown until the command that reveals it has printed.
	// Discarding both streams is the only correct answer; a provider that needs to
	// report something must do it by failing.
	ctx = spawn.WithOutputWriters(ctx, io.Discard, io.Discard)

	op := providerops.ResolveSecret
	got, exported, err := CallOp(ctx, p.drv, op, providerTarget(ctx, op.Name, ""), types.SecretRef{Ref: ref})
	if err != nil {
		if ctx.Err() != nil && !interactive {
			// The distinction worth drawing for the reader: it did not "time out", it had
			// nobody to ask. Naming that is the difference between re-running it and
			// wiring a service token.
			return secret.Value{}, fmt.Errorf("provider %q needed %s and there is no terminal to prompt on, "+
				"configure an unattended credential for it (a service-account token) or run this interactively",
				p.name, timeout)
		}
		if ctx.Err() != nil {
			return secret.Value{}, fmt.Errorf("provider %q did not answer within %s, it may be waiting on an "+
				"unlock nobody completed", p.name, timeout)
		}
		return secret.Value{}, err
	}
	if !exported {
		return secret.Value{}, fmt.Errorf("provider %q does not export %s(target: Target, req: SecretRef) > Secret", p.name, op.Name)
	}
	if got.Value == "" {
		return secret.Value{}, fmt.Errorf("provider %q returned a Secret with no value (%s must set Secret.value)", p.name, op.Name)
	}
	return secret.NewValue(got.Value), nil
}
