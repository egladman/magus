package secrets

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

// Provider fetches the value behind an opaque reference. The reference format is the
// provider's own business (an environment variable name, a vault path) and this package
// never parses it.
type Provider interface {
	// Fetch returns ref's value, or errors if it cannot. A backend FETCHES; the
	// [Resolver] READS: it selects the provider, memoizes, and registers the value for
	// redaction, which is the security-critical part. Two verbs so the two contracts
	// cannot be confused at a call site.
	//
	// Returns a [Secret], so a provider cannot accidentally log or format the credential
	// it just fetched on its way back.
	Fetch(ctx context.Context, ref string) (Secret, error)
}

// MinMaskLen is the shortest value a Resolver masks. Below it, masking does more harm than
// good: a two-character "secret" occurs constantly in ordinary output, and redacting every
// occurrence would shred the logs a person needs while protecting something that was never
// a credential. See [WithShortSecret].
const MinMaskLen = 4

// envProvider is the built-in provider: a reference is an environment variable name. It
// applies when no provider is selected.
type envProvider struct{}

// Fetch returns the environment variable named by ref. Unset and set-to-empty are both
// errors: returning "" would hand the caller a credential-shaped blank that fails later
// at whatever consumes it, with an error far from the cause.
func (envProvider) Fetch(_ context.Context, ref string) (Secret, error) {
	v, ok := os.LookupEnv(ref)
	if !ok {
		return Secret{}, fmt.Errorf("$%s is not set", ref)
	}
	if v == "" {
		return Secret{}, fmt.Errorf("$%s is set but empty", ref)
	}
	return New(v), nil
}

// memoKey pairs a reference with the provider that resolved it. Keying on the reference
// alone would let a caller that reads before selecting a provider memoize the built-in
// env fallback and keep returning it under the declared provider: a silently wrong
// credential rather than an error.
type memoKey struct {
	provider string
	ref      string
}

// Resolver holds one scope's secret state: which provider is selected, the values read so
// far, the memo of what each reference resolved to, and the endpoints opened for grants.
// It is safe for concurrent use. A nil *Resolver is usable: every method degrades to
// "nothing resolved, nothing to redact", or errors where a value was asked for.
type Resolver struct {
	mu sync.RWMutex
	// providerName is the provider selected; empty means the built-in.
	providerName string
	// memo avoids re-invoking a provider that shells out (`op read`, `vault kv get`).
	memo map[memoKey]Secret
	// redactable holds every distinct value read, longest first so [Resolver.Redact]
	// masks a containing secret before one nested inside it.
	redactable []Secret
	// group collapses concurrent reads of one key into a single provider invocation.
	// Without it, N readers of the same reference at once means N interactive unlock
	// prompts, which the memo alone does not prevent; the lookup and the fetch cannot
	// share a lock without serializing unrelated references too.
	group singleflight.Group

	// Set once at construction and never written again, so deliberately outside mu.
	timeouts Timeouts
	open     Opener
	short    func(ctx context.Context, ref string)
	log      *slog.Logger
	// endpointTransport overrides the forwarder's round tripper. Nil outside this
	// package's tests, where http.DefaultTransport's clone is correct and the upstream
	// scheme is always https: an option that could aim a forwarder at a plaintext
	// upstream would undo the one guarantee the forwarder makes about the hop it owns.
	endpointTransport http.RoundTripper

	// endpointMu guards endpoints. A mutex of its OWN rather than mu: opening an endpoint
	// binds a socket while the lock is held, and that must not block an unrelated Redact,
	// which sits on the output capture hot path.
	//
	// LOCK ORDER: endpointMu then mu, never the reverse. OpenEndpoint holds endpointMu
	// across Register, which takes mu.
	endpointMu sync.Mutex
	// endpoints are the loopback forwarders opened through this resolver, one per
	// distinct grant and scope. Liveness-checked on lookup rather than assumed: the
	// resolver outlives a single scope. See endpoint.go.
	endpoints map[endpointKey]*forwarder
}

// NewResolver returns an empty Resolver.
func NewResolver(opts ...Option) *Resolver {
	r := &Resolver{timeouts: DefaultTimeouts, log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Timeouts returns the budgets for a provider read. A nil Resolver reports
// [DefaultTimeouts].
func (r *Resolver) Timeouts() Timeouts {
	if r == nil {
		return DefaultTimeouts
	}
	return r.timeouts
}

type resolverKey struct{}

// ContextWithResolver installs r as the context's resolver.
func ContextWithResolver(ctx context.Context, r *Resolver) context.Context {
	return context.WithValue(ctx, resolverKey{}, r)
}

// ResolverFromContext returns the context's resolver, or nil when none was installed or
// ctx is nil. The nil result is usable; see [Resolver].
func ResolverFromContext(ctx context.Context) *Resolver {
	// ctx.Value on a nil interface panics, and this is reached from log handlers and
	// redaction helpers whose whole contract is that they degrade rather than fail.
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(resolverKey{}).(*Resolver)
	return r
}

// SetProviderName selects the provider later reads go through, built by the [Opener].
// Selecting a second one replaces the first. A nil Resolver ignores it.
func (r *Resolver) SetProviderName(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providerName = name
}

// ProviderName returns the selected provider's name, empty when the built-in applies.
func (r *Resolver) ProviderName() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providerName
}

// errNoResolver is the one message for "this call needs a resolver and there is none".
var errNoResolver = errors.New("secret: no resolver on this context")

// Read resolves ref through the selected provider and registers the result for redaction
// before returning it. The registration is the point: every later write the caller passes
// through [Resolver.Redact] masks the value.
//
// A value shorter than [MinMaskLen] is returned but NOT registered; the [WithShortSecret]
// notice is told about it.
//
// Concurrent reads of one reference share a single provider invocation, and a reference
// is fetched once per provider for the Resolver's life.
func (r *Resolver) Read(ctx context.Context, ref string) (Secret, error) {
	if r == nil {
		return Secret{}, errNoResolver
	}
	if ref == "" {
		return Secret{}, errors.New("secret: empty reference")
	}

	name := r.ProviderName()
	key := memoKey{provider: name, ref: ref}

	r.mu.RLock()
	v, hit := r.memo[key]
	r.mu.RUnlock()
	if hit {
		return v, nil
	}

	got, err, _ := r.group.Do(fmt.Sprintf("%s\x00%s", name, ref), func() (any, error) {
		// Re-checked INSIDE the flight. A caller that missed the memo just as a previous
		// flight was completing would otherwise start a second one and invoke the
		// provider again, the exact duplicate unlock prompt this exists to prevent.
		r.mu.RLock()
		v, hit := r.memo[key]
		r.mu.RUnlock()
		if hit {
			return v, nil
		}
		p, err := r.provider(ctx, name)
		if err != nil {
			return nil, err
		}
		val, err := p.Fetch(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", ref, err)
		}
		if val.IsZero() {
			return nil, fmt.Errorf("secret %q: provider resolved it to an empty value", ref)
		}
		if val.Len() < MinMaskLen && r.short != nil {
			r.short(ctx, ref)
		}
		r.record(key, val)
		return val, nil
	})
	if err != nil {
		return Secret{}, err
	}
	v, ok := got.(Secret)
	if !ok {
		// Unreachable: the flight returns only a Secret or an error. Checked rather than
		// asserted, so a future edit that returns another type fails here instead of
		// panicking inside a credential path.
		return Secret{}, fmt.Errorf("secret %q: provider returned %T, not a secret", ref, got)
	}
	return v, nil
}

// provider returns the backend for name, falling back to the built-in when none is
// selected, which keeps an unconfigured caller working off its environment.
func (r *Resolver) provider(ctx context.Context, name string) (Provider, error) { //nolint:ireturn // Provider: envProvider or the opened one
	if name == "" {
		return envProvider{}, nil
	}
	if r.open == nil {
		return nil, fmt.Errorf("secret: provider %q is selected and this resolver has no opener", name)
	}
	p, err := r.open(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("secret: open provider %q: %w", name, err)
	}
	return p, nil
}

// record memoizes a resolved value and, when long enough to mask safely, adds it to the
// redaction set.
//
// The common ENCODINGS are registered alongside the raw form, because redaction is
// literal substring matching and a credential a tool re-encodes otherwise passes
// straight through. It covers a value encoded WHOLE: base64 or hex of the secret by
// itself, or its percent-escaped form in a URL.
//
// What it does NOT cover: a secret base64'd as part of a LARGER string. base64 works in
// 3-byte groups, so `base64(user + ":" + secret)` contains `base64(secret)` only when
// the prefix length is a multiple of 3: an `Authorization: Basic` header is covered about
// one time in three. Closing that needs the value to mask itself before concatenation,
// which is what a [Secret] is for.
func (r *Resolver) record(key memoKey, v Secret) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.memo == nil {
		r.memo = make(map[memoKey]Secret)
	}
	r.memo[key] = v
	// Gated on the SOURCE length, not each encoded form. Encoding inflates: base64 of a
	// 1-character value is "MQ==" and hex of "ab" is "6162", both long enough to clear
	// MinMaskLen on their own, so a per-form check would register derivatives of exactly
	// the values this refuses to protect.
	if v.Len() < MinMaskLen {
		return
	}
	for _, form := range encodedForms(v) {
		r.addRedactable(form)
	}
}

// Register adds v and its encoded forms to the redaction set without memoizing it as a
// resolved reference: for a credential the caller mints or receives outside a provider (a
// token read from stdin). A value shorter than [MinMaskLen] and a nil Resolver are
// ignored.
func (r *Resolver) Register(v Secret) {
	if r == nil || v.Len() < MinMaskLen {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, form := range encodedForms(v) {
		r.addRedactable(form)
	}
}

// unregisterRedactable removes v and its encoded forms from the redaction set.
//
// The ONLY caller is a forwarder's shutdown: an endpoint's token and base URL are
// credential-equivalent while the listener is up and MEANINGLESS once it is down. A
// resolved credential is never removed, because it stays valid wherever it was sent.
// Without this the set would grow for the resolver's lifetime, and Redact scans it on
// every call.
func (r *Resolver) unregisterRedactable(v Secret) {
	if r == nil || v.Len() < MinMaskLen {
		return
	}
	drop := make(map[string]bool, len(encodedForms(v)))
	for _, form := range encodedForms(v) {
		drop[form.Reveal()] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.redactable[:0]
	for _, existing := range r.redactable {
		if !drop[existing.Reveal()] {
			kept = append(kept, existing)
		}
	}
	for i := len(kept); i < len(r.redactable); i++ {
		r.redactable[i] = Secret{}
	}
	r.redactable = kept
}

// encodedForms returns v plus the encodings of it worth matching. Duplicates are harmless
// (addRedactable dedupes) and expected: a value with no special characters percent-escapes
// to itself.
func encodedForms(v Secret) []Secret {
	plain := v.Reveal()
	return []Secret{
		v,
		New(base64.StdEncoding.EncodeToString([]byte(plain))),
		New(base64.RawStdEncoding.EncodeToString([]byte(plain))),
		New(base64.URLEncoding.EncodeToString([]byte(plain))),
		New(hex.EncodeToString([]byte(plain))),
		New(url.QueryEscape(plain)),
		New(url.PathEscape(plain)),
	}
}

// addRedactable inserts one value into the redaction set, keeping it ordered longest
// first. Callers hold r.mu.
func (r *Resolver) addRedactable(v Secret) {
	if v.Len() < MinMaskLen {
		return
	}
	for _, existing := range r.redactable {
		if existing.Reveal() == v.Reveal() {
			return
		}
	}
	r.redactable = append(r.redactable, v)
	for i := len(r.redactable) - 1; i > 0 && r.redactable[i].Len() > r.redactable[i-1].Len(); i-- {
		r.redactable[i], r.redactable[i-1] = r.redactable[i-1], r.redactable[i]
	}
}

// Redact replaces every value this resolver has registered with the mask. It returns p
// unchanged, without copying, when there is nothing to do: the overwhelmingly common
// case on an output-capture hot path. Callers must not retain p after a no-op return: it
// is the caller's own buffer, handed straight back.
//
// LIMITS, which callers must not overstate:
//   - Only values read or registered through this resolver are known.
//   - It is literal substring replacement. The base64, hex and percent-escaped forms are
//     registered alongside the raw value; ANY other transform (a different alphabet,
//     compression, splitting the value across a delimiter) defeats it.
//   - A secret straddling two calls is masked only if both halves land in one call.
func (r *Resolver) Redact(p []byte) []byte {
	if r == nil || len(p) == 0 {
		return p
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.redactable) == 0 {
		return p
	}
	s := string(p)
	out := s
	for _, v := range r.redactable {
		out = strings.ReplaceAll(out, v.Reveal(), mask)
	}
	if out == s {
		return p
	}
	return []byte(out)
}

// hasSecrets reports whether anything has been registered to redact. It lets a caller skip
// an expensive walk (rendering every attr of a log record) on the common run that read no
// secret at all.
func (r *Resolver) hasSecrets() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.redactable) > 0
}

// RedactString is [Resolver.Redact] for text already in string form.
func (r *Resolver) RedactString(s string) string {
	if r == nil || s == "" {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.redactable {
		s = strings.ReplaceAll(s, v.Reveal(), mask)
	}
	return s
}

// Redact redacts p using ctx's resolver, returning p unchanged (and UNCOPIED, still the
// caller's own buffer) when there is no resolver or nothing matched.
func Redact(ctx context.Context, p []byte) []byte { return ResolverFromContext(ctx).Redact(p) }

// RedactString redacts s using ctx's resolver.
func RedactString(ctx context.Context, s string) string {
	return ResolverFromContext(ctx).RedactString(s)
}
