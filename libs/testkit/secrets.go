package testkit

import (
	"context"
	"fmt"
	"sync"

	"github.com/egladman/magus/libs/secrets"
)

// SecretProvider is a secrets.Provider that answers from a fixed set of values and counts
// how often each reference was fetched, so a test can assert that a read was memoized or
// that nothing was resolved at all. It is safe for concurrent use.
type SecretProvider struct {
	mu      sync.Mutex
	values  map[string]string
	fetches map[string]int
}

// NewSecretProvider returns a provider that resolves each key of values to its value. A
// reference it does not hold is an error naming it, as a real provider's would be.
func NewSecretProvider(values map[string]string) *SecretProvider {
	return &SecretProvider{values: values, fetches: map[string]int{}}
}

// Fetch returns ref's value and counts the fetch, including a failed one.
func (p *SecretProvider) Fetch(_ context.Context, ref string) (secrets.Secret, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetches[ref]++
	v, ok := p.values[ref]
	if !ok {
		return secrets.Secret{}, fmt.Errorf("testkit secret provider holds no %q", ref)
	}
	return secrets.New(v), nil
}

// Fetches is how many times ref was fetched.
func (p *SecretProvider) Fetches(ref string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetches[ref]
}

// EnvResolver returns a Resolver that reads a bare reference from the environment, the
// way a production scope does, with opts applied. It fails the test when the resolver
// cannot be built, which only an invalid option causes.
func EnvResolver(t TB, opts ...secrets.Option) *secrets.Resolver {
	t.Helper()
	r, err := secrets.NewResolver(secrets.Config{Fallback: secrets.EnvProvider{}}, opts...)
	if err != nil {
		t.Fatalf("testkit: build the secret resolver: %v", err)
	}
	return r
}

// WithEnvResolver installs an [EnvResolver] on ctx, for a helper that has a context but no
// TB to fail. It has no options, so the build cannot fail; were it to, ctx carries a nil
// resolver, and every read and redaction under it is a *secrets.NoResolverError.
func WithEnvResolver(ctx context.Context) context.Context {
	r, _ := secrets.NewResolver(secrets.Config{Fallback: secrets.EnvProvider{}})
	return secrets.WithResolver(ctx, r)
}

// Opener returns a secrets.Opener that opens p under every name, for secrets.WithOpener.
func (p *SecretProvider) Opener() secrets.Opener {
	return func(context.Context, string) (secrets.Provider, error) { return p, nil }
}
