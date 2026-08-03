package secret

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withResolver returns a context carrying a fresh resolver, plus the resolver. Per-test
// state, which is the whole point of the type: nothing leaks between tests, and nothing
// leaks between runs in a long-lived daemon either.
func withResolver(t *testing.T) (context.Context, *Resolver) {
	t.Helper()
	r := New()
	return ContextWithResolver(t.Context(), r), r
}

func TestEnvProviderRead(t *testing.T) {
	t.Run("reads the named variable", func(t *testing.T) {
		t.Setenv("MAGUS_TEST_TOKEN", "s3cret-value")
		v, err := envProvider{}.Fetch(t.Context(), "MAGUS_TEST_TOKEN")
		require.NoError(t, err)
		assert.Equal(t, "s3cret-value", v)
	})

	t.Run("unset and empty are distinct errors, both naming the variable", func(t *testing.T) {
		_, err := envProvider{}.Fetch(t.Context(), "MAGUS_TEST_ABSENT")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "$MAGUS_TEST_ABSENT is not set")

		t.Setenv("MAGUS_TEST_BLANK", "")
		_, err = envProvider{}.Fetch(t.Context(), "MAGUS_TEST_BLANK")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is set but empty")
	})
}

func TestReadRegistersForRedaction(t *testing.T) {
	ctx, r := withResolver(t)
	t.Setenv("MAGUS_TEST_TOKEN", "hunter2-token")

	v, err := ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_TOKEN")
	require.NoError(t, err)
	assert.Equal(t, "hunter2-token", v)

	assert.Equal(t, "pushing with *** to ghcr.io\n",
		string(r.Redact([]byte("pushing with hunter2-token to ghcr.io\n"))))
}

func TestRedactIsInertWithoutReadSecrets(t *testing.T) {
	_, r := withResolver(t)

	in := []byte("nothing sensitive here")
	got := r.Redact(in)
	assert.Equal(t, "nothing sensitive here", string(got))
	// Returned without copying when there is nothing to do: this is the hot path for
	// every build that reads no secret at all.
	assert.Same(t, &in[0], &got[0])
}

func TestNilResolverDegradesSafely(t *testing.T) {
	// describe/parse paths never install a resolver. Every method must tolerate that
	// rather than force each caller to nil-check.
	var r *Resolver
	assert.Equal(t, "untouched", string(r.Redact([]byte("untouched"))))
	assert.Equal(t, "untouched", r.RedactString("untouched"))
	assert.Empty(t, r.ProviderName())
	assert.NotPanics(t, func() { r.SetProviderName("x") })

	_, err := ResolverFromContext(context.Background()).Read(context.Background(), "ANY")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no resolver")
}

func TestRedactMasksLongestValueFirst(t *testing.T) {
	ctx, r := withResolver(t)
	// A short token that is a substring of a longer one. Masking the short value first
	// would leave the rest of the long one in the output.
	t.Setenv("MAGUS_TEST_SHORT", "abcd")
	t.Setenv("MAGUS_TEST_LONG", "abcdefgh")
	_, err := ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_SHORT")
	require.NoError(t, err)
	_, err = ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_LONG")
	require.NoError(t, err)

	assert.Equal(t, "value=***", string(r.Redact([]byte("value=abcdefgh"))))
}

func TestReadDoesNotRegisterValuesTooShortToMask(t *testing.T) {
	ctx, r := withResolver(t)
	t.Setenv("MAGUS_TEST_TINY", "ab")

	v, err := ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_TINY")
	require.NoError(t, err)
	assert.Equal(t, "ab", v, "the value is returned; only its protection is declined")

	// Unregistered, so ordinary output containing those letters survives intact. This is
	// a documented hole, not an accident - see minRedactLen.
	assert.Equal(t, "grab a table", string(r.Redact([]byte("grab a table"))))
}

func TestReadMemoizesPerProviderAndReference(t *testing.T) {
	ctx, r := withResolver(t)

	var calls int
	var mu sync.Mutex
	withOpener(t, func(context.Context, string) (Provider, error) {
		return providerFunc(func(context.Context, string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return "resolved-value", nil
		}), nil
	})
	r.SetProviderName("fake")

	for range 3 {
		v, err := ResolverFromContext(ctx).Read(ctx, "SOME_REF")
		require.NoError(t, err)
		assert.Equal(t, "resolved-value", v)
	}
	assert.Equal(t, 1, calls, "a provider that shells out is invoked once per reference")
}

// TestReadReReadsWhenTheProviderChanges pins the fix for a silently-wrong-credential bug:
// memoizing on the reference alone meant a magusfile that read before selecting a
// provider kept returning the built-in env value under a declared provider.
func TestReadReReadsWhenTheProviderChanges(t *testing.T) {
	ctx, r := withResolver(t)
	t.Setenv("SOME_REF", "from-env")

	v, err := ResolverFromContext(ctx).Read(ctx, "SOME_REF")
	require.NoError(t, err)
	assert.Equal(t, "from-env", v)

	withOpener(t, func(context.Context, string) (Provider, error) {
		return providerFunc(func(context.Context, string) (string, error) {
			return "from-vault", nil
		}), nil
	})
	r.SetProviderName("vault")

	v, err = ResolverFromContext(ctx).Read(ctx, "SOME_REF")
	require.NoError(t, err)
	assert.Equal(t, "from-vault", v, "the declared provider must win over a memoized fallback")
}

// TestReadCollapsesConcurrentReadsOfOneReference pins the singleflight. Without it, N
// targets reading one reference at once means N provider invocations - for an
// interactive backend, N unlock prompts.
func TestReadCollapsesConcurrentReadsOfOneReference(t *testing.T) {
	ctx, r := withResolver(t)

	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	withOpener(t, func(context.Context, string) (Provider, error) {
		return providerFunc(func(context.Context, string) (string, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release // hold every caller inside the provider so they genuinely overlap
			return "one-value", nil
		}), nil
	})
	r.SetProviderName("slow")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := ResolverFromContext(ctx).Read(ctx, "SHARED_REF")
			assert.NoError(t, err)
			assert.Equal(t, "one-value", v)
		}()
	}
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, calls)
}

func TestReadRejectsEmptyReference(t *testing.T) {
	ctx, _ := withResolver(t)
	_, err := ResolverFromContext(ctx).Read(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty reference")
}

func TestProviderNameReportsSelection(t *testing.T) {
	_, r := withResolver(t)
	assert.Empty(t, r.ProviderName(), "no selection means the built-in env provider applies")
	r.SetProviderName("onepassword")
	assert.Equal(t, "onepassword", r.ProviderName())
}

func TestRedactStringMatchesRedact(t *testing.T) {
	ctx, r := withResolver(t)
	t.Setenv("MAGUS_TEST_BOTH", "both-forms-token")
	_, err := ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_BOTH")
	require.NoError(t, err)

	const in = "x=both-forms-token y"
	assert.Equal(t, r.RedactString(in), string(r.Redact([]byte(in))))
	assert.True(t, strings.Contains(r.RedactString(in), mask))
}

// providerFunc adapts a function to [Provider].
type providerFunc func(context.Context, string) (string, error)

func (f providerFunc) Fetch(ctx context.Context, ref string) (string, error) { return f(ctx, ref) }

// withOpener installs an opener for one test and restores the previous one after.
// RegisterProviderOpener panics on a second call - correct for its real once-at-init
// contract, unusable across several tests - so this reaches the variable directly rather
// than relaxing that contract for production callers.
func withOpener(t *testing.T, fn func(context.Context, string) (Provider, error)) {
	t.Helper()
	openerMu.Lock()
	prev := providerOpener
	providerOpener = fn
	openerMu.Unlock()
	t.Cleanup(func() {
		openerMu.Lock()
		providerOpener = prev
		openerMu.Unlock()
	})
}

// TestRedactingHandlerCoversNonPrettyFormats is the regression for a FOURTH leak path,
// found only by review: redaction lived in one handler's print funnel, so
// `--log-format=json` sent run.exec's argv - documented as able to carry a credential -
// to stderr untouched. Wrapping the handler covers every format, including one added later.
func TestRedactingHandlerCoversNonPrettyFormats(t *testing.T) {
	ctx, _ := withResolver(t)
	t.Setenv("MAGUS_TEST_LOG_TOKEN", "ghp_never_json_me")
	_, err := ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_LOG_TOKEN")
	require.NoError(t, err)

	var sink bytes.Buffer
	h := NewRedactingHandler(slog.NewJSONHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.New(h).DebugContext(ctx, "run.exec tok=ghp_never_json_me",
		"cmd", "sh", "arg", "-p ghp_never_json_me")

	assert.NotContains(t, sink.String(), "ghp_never_json_me", "neither message nor attrs may carry it")
	assert.Contains(t, sink.String(), mask)
}

func TestRedactingHandlerIsAPassthroughWithoutAResolver(t *testing.T) {
	var sink bytes.Buffer
	h := NewRedactingHandler(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.New(h).DebugContext(context.Background(), "plain", "cmd", "go build")

	assert.Contains(t, sink.String(), "go build")
	assert.NotContains(t, sink.String(), mask)
}

func TestRegisterProviderOpenerRejectsASecondRegistration(t *testing.T) {
	// The panic-on-second-call branch had zero coverage because every other test reaches
	// providerOpener directly. It is a real contract - two openers would mean the second
	// silently shadowing the first - so it is worth exercising through the exported door.
	openerMu.Lock()
	prev := providerOpener
	providerOpener = nil
	openerMu.Unlock()
	t.Cleanup(func() {
		openerMu.Lock()
		providerOpener = prev
		openerMu.Unlock()
	})

	fn := func(context.Context, string) (Provider, error) { return envProvider{}, nil }
	require.NotPanics(t, func() { RegisterProviderOpener(fn) }, "the first registration is the contract")
	assert.Panics(t, func() { RegisterProviderOpener(fn) }, "a second must not silently shadow the first")
}

func TestWithTimeoutsKeepsDefaultsForZeroFields(t *testing.T) {
	// A partially-specified magus.yaml section must not zero the other budget - a zero
	// timeout would make every provider read fail instantly.
	assert.Equal(t, DefaultTimeouts, New().Timeouts(), "no options means the built-ins")

	only := New(WithTimeouts(Timeouts{Interactive: 5 * time.Second}))
	assert.Equal(t, 5*time.Second, only.Timeouts().Interactive)
	assert.Equal(t, DefaultTimeouts.Unattended, only.Timeouts().Unattended,
		"the unset field keeps its default rather than becoming zero")

	both := New(WithTimeouts(Timeouts{Interactive: time.Second, Unattended: 2 * time.Second}))
	assert.Equal(t, Timeouts{Interactive: time.Second, Unattended: 2 * time.Second}, both.Timeouts())

	var nilResolver *Resolver
	assert.Equal(t, DefaultTimeouts, nilResolver.Timeouts(), "a nil resolver reports the defaults")
}

func TestPackageLevelRedactorsFollowTheContext(t *testing.T) {
	ctx, _ := withResolver(t)
	t.Setenv("MAGUS_TEST_PKG", "pkg-level-token")
	_, err := ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_PKG")
	require.NoError(t, err)

	assert.Equal(t, "a=***", string(Redact(ctx, []byte("a=pkg-level-token"))))
	assert.Equal(t, "a=***", RedactString(ctx, "a=pkg-level-token"))

	// No resolver on the context, and a nil context: both degrade, neither panics. These
	// are the paths log handlers and the capture tap take on an ordinary build.
	assert.Equal(t, "a=pkg-level-token", RedactString(context.Background(), "a=pkg-level-token"))
	// A nil context reaches these from log handlers that have none to hand them, and
	// ctx.Value on a nil interface panics - so the nil guard is the contract, not caution.
	// Held in a variable because staticcheck rightly rejects a nil ctx literal at a call.
	var nilCtx context.Context
	assert.NotPanics(t, func() {
		assert.Nil(t, ResolverFromContext(nilCtx))
		assert.Equal(t, "x", RedactString(nilCtx, "x"))
		assert.Equal(t, "x", string(Redact(nilCtx, []byte("x"))))
	})
}

func TestRedactingHandlerPreservesAttrsAndGroups(t *testing.T) {
	// WithAttrs/WithGroup must return a wrapper, not the bare inner handler: a slog.Logger
	// that calls either would otherwise escape redaction for every later record.
	ctx, _ := withResolver(t)
	t.Setenv("MAGUS_TEST_GRP", "group-token-value")
	_, err := ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_GRP")
	require.NoError(t, err)

	var sink bytes.Buffer
	base := NewRedactingHandler(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug}))

	withAttrs := base.WithAttrs([]slog.Attr{slog.String("preset", "group-token-value")})
	require.IsType(t, redactingHandler{}, withAttrs, "WithAttrs must stay wrapped")
	slog.New(withAttrs).DebugContext(ctx, "m")
	assert.NotContains(t, sink.String(), "group-token-value")

	sink.Reset()
	grouped := base.WithGroup("g")
	require.IsType(t, redactingHandler{}, grouped, "WithGroup must stay wrapped")
	slog.New(grouped).DebugContext(ctx, "m", "tok", "group-token-value")
	assert.NotContains(t, sink.String(), "group-token-value")

	assert.True(t, base.Enabled(ctx, slog.LevelDebug))
}

func TestReadSurfacesProviderOpenFailures(t *testing.T) {
	ctx, r := withResolver(t)
	withOpener(t, func(context.Context, string) (Provider, error) {
		return nil, errors.New("spell not registered")
	})
	r.SetProviderName("missing-spell")

	_, err := r.Read(ctx, "ANY")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-spell", "the error names the provider that failed to open")
}

func TestReadRejectsAnEmptyValueFromAProvider(t *testing.T) {
	ctx, r := withResolver(t)
	withOpener(t, func(context.Context, string) (Provider, error) {
		return providerFunc(func(context.Context, string) (string, error) { return "", nil }), nil
	})
	r.SetProviderName("blank")

	_, err := r.Read(ctx, "REF")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty value",
		"a blank credential must fail here, not at whatever consumes it")
}
