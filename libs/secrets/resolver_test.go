package secrets

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ResolverSuite struct {
	suite.Suite
	r *Resolver
}

func TestResolver(t *testing.T) { suite.Run(t, new(ResolverSuite)) }

// SetupTest gives every test a fresh resolver on its context: nothing leaks between
// tests, as nothing leaks between scopes of a long-lived caller.
func (s *ResolverSuite) SetupTest() {
	s.r = NewResolver()
}

// ctx is the test's context carrying the suite's resolver.
func (s *ResolverSuite) resolverContext() context.Context { return WithResolver(s.T().Context(), s.r) }

// read reads ref through the context's resolver, failing the test on an error.
func (s *ResolverSuite) read(ref string) Secret {
	s.T().Helper()
	ctx := s.resolverContext()
	v, err := ResolverFromContext(ctx).Read(ctx, ref)
	s.Require().NoError(err)
	return v
}

// providerFunc adapts a function to [Provider].
type providerFunc func(context.Context, string) (Secret, error)

func (f providerFunc) Fetch(ctx context.Context, ref string) (Secret, error) { return f(ctx, ref) }

// withProvider replaces the suite's resolver with one whose opener returns p for every
// name, reading through name.
func (s *ResolverSuite) withProvider(name string, p Provider, opts ...Option) {
	opener := func(context.Context, string) (Provider, error) { return p, nil }
	s.r = NewResolver(append([]Option{WithOpener(opener)}, opts...)...).WithProvider(name)
}

func (s *ResolverSuite) TestEnvProviderReadsTheNamedVariable() {
	s.T().Setenv("SECRETS_TEST_TOKEN", "s3cret-value")
	v, err := envProvider{}.Fetch(s.T().Context(), "SECRETS_TEST_TOKEN")
	s.Require().NoError(err)
	s.Equal("s3cret-value", v.Reveal())
}

func (s *ResolverSuite) TestEnvProviderTellsUnsetFromEmpty() {
	_, err := envProvider{}.Fetch(s.T().Context(), "SECRETS_TEST_ABSENT")
	s.Require().ErrorContains(err, "$SECRETS_TEST_ABSENT is not set")

	s.T().Setenv("SECRETS_TEST_BLANK", "")
	_, err = envProvider{}.Fetch(s.T().Context(), "SECRETS_TEST_BLANK")
	s.Require().ErrorContains(err, "is set but empty")
}

func (s *ResolverSuite) TestReadRegistersForRedaction() {
	s.T().Setenv("SECRETS_TEST_TOKEN", "hunter2-token")
	s.Equal("hunter2-token", s.read("SECRETS_TEST_TOKEN").Reveal())
	s.Equal("pushing with *** to ghcr.io\n", string(s.r.redact([]byte("pushing with hunter2-token to ghcr.io\n"))))
}

func (s *ResolverSuite) TestRedactIsInertWithoutReadSecrets() {
	in := []byte("nothing sensitive here")
	got := s.r.redact(in)
	s.Equal("nothing sensitive here", string(got))
	// Returned without copying when there is nothing to do: the hot path for every
	// caller that reads no secret at all.
	s.Same(&in[0], &got[0])
}

func (s *ResolverSuite) TestANilResolverRefusesToRedact() {
	var r *Resolver
	var nr *NoResolverError

	out, err := r.Redact([]byte("plaintext"))
	s.Require().ErrorAs(err, &nr)
	s.Nil(out, "nothing a nil resolver returns is known to be masked")
	str, err := r.RedactString("plaintext")
	s.Require().ErrorAs(err, &nr)
	s.Empty(str)
}

func (s *ResolverSuite) TestNilResolverDegradesSafely() {
	var r *Resolver
	s.Empty(r.ProviderName())
	s.Nil(r.WithProvider("x"))
	var nr *NoResolverError
	s.ErrorAs(r.Register(New("refused-by-nil")), &nr)
	s.Equal(DefaultTimeouts(), r.Timeouts())

	_, err := ResolverFromContext(context.Background()).Read(context.Background(), "ANY")
	s.Require().ErrorAs(err, &nr)
	s.Equal("secrets: no resolver on this context", err.Error())
}

func (s *ResolverSuite) TestRedactMasksLongestValueFirst() {
	// A short token that is a substring of a longer one. Masking the short value first
	// would leave the rest of the long one in the output.
	s.T().Setenv("SECRETS_TEST_SHORT", "abcd")
	s.T().Setenv("SECRETS_TEST_LONG", "abcdefgh")
	s.read("SECRETS_TEST_SHORT")
	s.read("SECRETS_TEST_LONG")
	s.Equal("value=***", string(s.r.redact([]byte("value=abcdefgh"))))
}

func (s *ResolverSuite) TestReadDoesNotRegisterValuesTooShortToMask() {
	s.T().Setenv("SECRETS_TEST_TINY", "ab")
	s.r = NewResolver(WithShortSecret(func(context.Context, string) {}))
	s.Equal("ab", s.read("SECRETS_TEST_TINY").Reveal(), "the value is returned; only its protection is declined")
	s.Equal("grab a table", string(s.r.redact([]byte("grab a table"))))

	// No encoded derivative either: encoding inflates, so base64("ab") is "YWI=" and
	// hex("ab") is "6162", both long enough to mask on their own.
	for _, form := range []string{"YWI=", "YWJh", "6162"} {
		s.Equal(fmt.Sprintf("x%sy", form), string(s.r.redact(fmt.Appendf(nil, "x%sy", form))),
			"no encoded form of a too-short secret may be registered")
	}
}

func (s *ResolverSuite) TestReadKeepsAnAnswerPerProviderAndReference() {
	var mu sync.Mutex
	calls := 0
	s.withProvider("fake", providerFunc(func(context.Context, string) (Secret, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return New("resolved-value"), nil
	}))

	for range 3 {
		s.Equal("resolved-value", s.read("SOME_REF").Reveal())
	}
	s.Equal(1, calls, "a provider that shells out is invoked once per reference")
}

// A read before a provider is selected must not answer for that provider afterwards:
// keeping answers by reference alone would keep returning the env value under it.
func (s *ResolverSuite) TestReadReReadsWhenTheProviderChanges() {
	opener := func(context.Context, string) (Provider, error) {
		return providerFunc(func(context.Context, string) (Secret, error) { return New("from-vault"), nil }), nil
	}
	s.r = NewResolver(WithOpener(opener))
	s.T().Setenv("SOME_REF", "from-env")
	s.Equal("from-env", s.read("SOME_REF").Reveal())

	s.r = s.r.WithProvider("vault")
	s.Equal("from-vault", s.read("SOME_REF").Reveal(), "the declared provider must win over a kept fallback")
}

// N readers of one reference at once share one provider invocation; for an interactive
// backend, that is the difference between one unlock prompt and N.
func (s *ResolverSuite) TestReadCollapsesConcurrentReadsOfOneReference() {
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	s.withProvider("slow", providerFunc(func(context.Context, string) (Secret, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release // hold every caller inside the provider so they genuinely overlap
		return New("one-value"), nil
	}))

	var wg sync.WaitGroup
	got := make([]string, 8)
	errs := make([]error, 8)
	for i := range 8 {
		wg.Go(func() {
			v, err := s.r.Read(s.resolverContext(), "SHARED_REF")
			got[i], errs[i] = v.Reveal(), err
		})
	}
	close(release)
	wg.Wait()

	for i := range 8 {
		s.Require().NoError(errs[i])
		s.Equal("one-value", got[i])
	}
	s.Equal(1, calls)
}

// The fetch is shared, so the caller that happened to start it giving up must not fail
// the callers still waiting on it.
func (s *ResolverSuite) TestReadOutlivesTheFirstCallersCancellation() {
	started := make(chan struct{})
	release := make(chan struct{})
	s.withProvider("slow", providerFunc(func(ctx context.Context, _ string) (Secret, error) {
		close(started)
		select {
		case <-release:
			return New("shared-value"), nil
		case <-ctx.Done():
			return Secret{}, ctx.Err()
		}
	}))

	first, cancelFirst := context.WithCancel(s.resolverContext())
	firstErr := make(chan error, 1)
	go func() {
		_, err := s.r.Read(first, "SHARED_REF")
		firstErr <- err
	}()
	<-started

	second := make(chan Secret, 1)
	go func() {
		v, err := s.r.Read(s.resolverContext(), "SHARED_REF")
		s.NoError(err)
		second <- v
	}()

	cancelFirst()
	err := <-firstErr
	s.Require().ErrorIs(err, context.Canceled, "the canceled caller stops waiting")
	var re *ReadError
	s.Require().ErrorAs(err, &re)

	close(release)
	s.Equal("shared-value", (<-second).Reveal(), "a waiter must not inherit another caller's cancellation")
}

func (s *ResolverSuite) TestReadRejectsEmptyReference() {
	_, err := s.r.Read(s.resolverContext(), "")
	var re *ReadError
	s.Require().ErrorAs(err, &re)
	s.Equal("secrets: read: empty reference", err.Error())
}

func (s *ResolverSuite) TestWithProviderDerivesWithoutChangingTheOriginal() {
	vault := s.r.WithProvider("onepassword")
	s.Empty(s.r.ProviderName(), "no selection means the built-in env provider applies")
	s.Equal("onepassword", vault.ProviderName())

	// The derived resolver shares the redaction set: a value one registers, both mask.
	s.Require().NoError(vault.Register(New("shared-redaction-token")))
	s.Equal(mask, s.r.redactString("shared-redaction-token"))
}

func (s *ResolverSuite) TestReadUnderANamedProviderNeedsAnOpener() {
	_, err := s.r.WithProvider("vault").Read(s.resolverContext(), "REF")
	s.Require().ErrorContains(err, "has no opener")
	s.Require().ErrorContains(err, `secrets: read "REF" via provider "vault"`)
}

func (s *ResolverSuite) TestReadSurfacesProviderOpenFailures() {
	s.r = NewResolver(WithOpener(func(context.Context, string) (Provider, error) {
		return nil, errors.New("spell not registered")
	})).WithProvider("missing-spell")
	_, err := s.r.Read(s.resolverContext(), "ANY")
	s.Require().ErrorContains(err, "missing-spell", "the error names the provider that failed to open")
}

func (s *ResolverSuite) TestReadRejectsAnEmptyValueFromAProvider() {
	s.withProvider("blank", providerFunc(func(context.Context, string) (Secret, error) { return Secret{}, nil }))
	_, err := s.r.Read(s.resolverContext(), "REF")
	s.Require().ErrorContains(err, "empty value", "a blank credential must fail here, not at whatever consumes it")
}

func (s *ResolverSuite) TestRedactStringMatchesRedact() {
	s.T().Setenv("SECRETS_TEST_BOTH", "both-forms-token")
	s.read("SECRETS_TEST_BOTH")

	const in = "x=both-forms-token y"
	s.Equal(s.r.redactString(in), string(s.r.redact([]byte(in))))
	s.True(strings.Contains(s.r.redactString(in), mask))
}

// A token in an `Authorization: Basic` header is base64 and a token in a URL is
// percent-escaped, so a tool that composes either and logs it would otherwise print a
// trivially recoverable credential.
func (s *ResolverSuite) TestRedactCoversCommonEncodings() {
	const tok = "ghp_encode_me_please"
	s.T().Setenv("SECRETS_TEST_TOKEN", tok)
	s.read("SECRETS_TEST_TOKEN")

	s.Equal(mask, s.r.redactString(tok))
	s.Equal(mask, s.r.redactString(base64.StdEncoding.EncodeToString([]byte(tok))))
	s.Equal(mask, s.r.redactString(base64.RawStdEncoding.EncodeToString([]byte(tok))))
	s.Equal(mask, s.r.redactString(base64.URLEncoding.EncodeToString([]byte(tok))))
	s.Equal(mask, s.r.redactString(hex.EncodeToString([]byte(tok))))

	// A percent-escaped value only differs when it contains reserved characters.
	const urly = "p@ss/word+with spaces"
	s.T().Setenv("SECRETS_TEST_URL_TOKEN", urly)
	s.read("SECRETS_TEST_URL_TOKEN")
	s.Equal(mask, s.r.redactString(url.QueryEscape(urly)))
	s.Equal(mask, s.r.redactString(url.PathEscape(urly)))
}

func (s *ResolverSuite) TestPackageLevelRedactorsFollowTheContext() {
	s.T().Setenv("SECRETS_TEST_PKG", "pkg-level-token")
	s.read("SECRETS_TEST_PKG")

	b, err := Redact(s.resolverContext(), []byte("a=pkg-level-token"))
	s.Require().NoError(err)
	s.Equal("a=***", string(b))
	str, err := RedactString(s.resolverContext(), "a=pkg-level-token")
	s.Require().NoError(err)
	s.Equal("a=***", str)

	var nr *NoResolverError
	_, err = RedactString(context.Background(), "a=pkg-level-token")
	s.Require().ErrorAs(err, &nr, "a context with no resolver is refused, not handed back in plaintext")

	// A nil context reaches these from log handlers that have none to hand them, and
	// ctx.Value on a nil interface panics, so the nil guard is the contract. Held in a
	// variable because staticcheck rejects a nil ctx literal at a call.
	var nilCtx context.Context
	s.NotPanics(func() {
		s.Nil(ResolverFromContext(nilCtx))
		_, err := RedactString(nilCtx, "x")
		s.ErrorAs(err, &nr)
		_, err = Redact(nilCtx, []byte("x"))
		s.ErrorAs(err, &nr)
	})
}

// A credential held without a provider read is masked like a resolved one.
func (s *ResolverSuite) TestRegisterMasksAHeldCredential() {
	s.Require().NoError(s.r.Register(New("stdin-registry-token")))
	s.Equal("push with "+mask, s.r.redactString("push with stdin-registry-token"))
	s.Empty(s.r.resolved, "a registered value is not a resolved reference")
}

func (s *ResolverSuite) TestRegisterRefusesAValueTooShortToMask() {
	var short *ShortSecretError
	s.Require().ErrorAs(s.r.Register(New("ab")), &short)
	s.Equal(2, short.Len)
	s.Equal("grab", s.r.redactString("grab"))
}

func (s *ResolverSuite) TestReadRefusesAShortValueNoNoticeHears() {
	s.T().Setenv("SECRETS_TEST_TINY", "ab")
	_, err := NewResolver().Read(s.T().Context(), "SECRETS_TEST_TINY")

	var short *ShortSecretError
	s.Require().ErrorAs(err, &short)
}

func (s *ResolverSuite) TestUnregisterKeepsOtherValues() {
	s.Require().NoError(s.r.Register(New("keep-this-token")))
	s.Require().NoError(s.r.Register(New("drop-this-token")))
	s.r.unregisterRedactable(New("drop-this-token"))
	s.Equal("drop-this-token "+mask, s.r.redactString("drop-this-token keep-this-token"))
}
