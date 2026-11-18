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
	// [Resolver] READS: it selects the provider, keeps each answer, and registers the value for
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

// EnvProvider is a provider whose reference is an environment variable name. A caller
// that wants reads with no provider selected to work off its environment sets it as
// [Config.Fallback]; the Resolver never reaches for it unasked.
type EnvProvider struct{}

// Fetch returns the environment variable named by ref. Unset and set-to-empty are both
// errors: returning "" would hand the caller a credential-shaped blank that fails later
// at whatever consumes it, with an error far from the cause.
func (EnvProvider) Fetch(_ context.Context, ref string) (Secret, error) {
	v, ok := os.LookupEnv(ref)
	if !ok {
		return Secret{}, fmt.Errorf("$%s is not set", ref)
	}
	if v == "" {
		return Secret{}, fmt.Errorf("$%s is set but empty", ref)
	}
	return New(v), nil
}

// resolvedKey pairs a reference with the provider that resolved it. Keying on the reference
// alone would let a caller that reads before selecting a provider settle on the fallback
// and keep returning it under the declared provider: a silently wrong credential rather
// than an error.
type resolvedKey struct {
	provider string
	ref      string
}

// Resolver holds one scope's secret state: the values read so far, what each
// reference resolved to, and the endpoints opened for grants. A Resolver reads through
// one provider, fixed for the value; [Resolver.WithProvider] derives a Resolver that
// reads through another and shares this one's state. It is safe for concurrent use. A
// nil *Resolver reads, registers and redacts nothing: each of those is a
// *[NoResolverError].
type Resolver struct {
	// provider is the provider this Resolver reads through; empty means the fallback.
	provider string
	*state
}

// state is what every Resolver derived from one [NewResolver] shares, so a value read
// through any of them is masked by all of them.
type state struct {
	mu sync.RWMutex
	// resolved avoids re-invoking a provider that shells out (`op read`, `vault kv get`).
	resolved map[resolvedKey]Secret
	// redactable holds every distinct value read, longest first so [Resolver.Redact]
	// masks a containing secret before one nested inside it.
	redactable []Secret
	// group collapses concurrent reads of one key into a single provider invocation.
	// Without it, N readers of the same reference at once means N interactive unlock
	// prompts, which the kept answers alone do not prevent; the lookup and the fetch cannot
	// share a lock without serializing unrelated references too.
	group singleflight.Group

	// Set once at construction and never written again, so deliberately outside mu.
	timeouts Timeouts
	fallback Provider
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
	// distinct grant, scope and provider. Liveness-checked on lookup rather than assumed:
	// the resolver outlives a single scope. See endpoint.go.
	endpoints map[endpointKey]*forwarder
}

// Config is what a [Resolver] is built from. The zero value builds a Resolver with no
// fallback, which refuses every read until [Resolver.WithProvider] selects a provider.
type Config struct {
	// Fallback is the provider a read goes through when none is selected by
	// [Resolver.WithProvider]. Nil means none: such a read is a *[ReadError] wrapping
	// [ErrNoFallback], never a silent read of the environment.
	Fallback Provider
	// Timeouts bounds a provider read. A zero field keeps [DefaultTimeouts].
	Timeouts Timeouts
	// Open builds the provider a name selects. See [WithOpener].
	Open Opener
	// ShortSecret hears each resolved value shorter than [MinMaskLen]. See [WithShortSecret].
	ShortSecret func(ctx context.Context, ref string)
	// Log receives what the endpoint forwarder cannot put on the socket. Nil discards.
	Log *slog.Logger

	// errs are the problems [Option] values recorded, reported by [NewResolver].
	errs []error
}

// fail records a problem with an option for [NewResolver] to report.
func (c *Config) fail(option, problem string) {
	c.errs = append(c.errs, &OptionError{Option: option, Problem: problem})
}

// NewResolver returns an empty Resolver built from cfg with opts applied. It returns
// every problem the options recorded joined, including a negative [Config.Timeouts]
// field, each an *[OptionError].
func NewResolver(cfg Config, opts ...Option) (*Resolver, error) {
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.Timeouts.Interactive < 0 || cfg.Timeouts.Unattended < 0 {
		cfg.fail("Config.Timeouts", "a negative timeout")
	}
	if len(cfg.errs) > 0 {
		return nil, errors.Join(cfg.errs...)
	}
	timeouts := DefaultTimeouts()
	if cfg.Timeouts.Interactive > 0 {
		timeouts.Interactive = cfg.Timeouts.Interactive
	}
	if cfg.Timeouts.Unattended > 0 {
		timeouts.Unattended = cfg.Timeouts.Unattended
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Resolver{state: &state{
		timeouts: timeouts,
		fallback: cfg.Fallback,
		open:     cfg.Open,
		short:    cfg.ShortSecret,
		log:      log,
	}}, nil
}

// Timeouts returns the budgets for a provider read. A nil Resolver reports
// [DefaultTimeouts].
func (r *Resolver) Timeouts() Timeouts {
	if r == nil {
		return DefaultTimeouts()
	}
	return r.timeouts
}

type resolverKey struct{}

// WithResolver installs r as the context's resolver.
func WithResolver(ctx context.Context, r *Resolver) context.Context {
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

// WithProvider returns a Resolver that reads through the provider named name, built by
// the [Opener]; an empty name selects the [Config.Fallback]. It shares r's
// kept answers, redaction set and endpoints, so a value either reads is masked by both. r is
// unchanged. A nil Resolver returns nil.
func (r *Resolver) WithProvider(name string) *Resolver {
	if r == nil {
		return nil
	}
	derived := *r
	derived.provider = name
	return &derived
}

// ProviderName returns the name of the provider r reads through, empty when the
// fallback applies.
func (r *Resolver) ProviderName() string {
	if r == nil {
		return ""
	}
	return r.provider
}

// NoResolverError is what a call that needs a [Resolver] returns when it has none,
// usually because the context carried none.
type NoResolverError struct{}

func (*NoResolverError) Error() string { return "secrets: no resolver on this context" }

// ReadError is why [Resolver.Read] returned no value. Provider is empty under the
// [Config.Fallback]; Err is the cause, a canceled or expired context
// included.
type ReadError struct {
	Ref      string
	Provider string
	Err      error
}

func (e *ReadError) Error() string {
	if e.Ref == "" {
		return "secrets: read: " + e.Err.Error()
	}
	via := "the fallback provider"
	if e.Provider != "" {
		via = fmt.Sprintf("provider %q", e.Provider)
	}
	return fmt.Sprintf("secrets: read %q via %s: %v", e.Ref, via, e.Err)
}

func (e *ReadError) Unwrap() error { return e.Err }

// ErrNoFallback is why a read with no provider selected failed on a Resolver built
// without a [Config.Fallback]. It is the cause inside a *[ReadError].
var ErrNoFallback = errors.New("no provider is selected and this resolver has no fallback")

var (
	errEmptyRef   = errors.New("empty reference")
	errNoOpener   = errors.New("a provider is selected and this resolver has no opener")
	errEmptyValue = errors.New("provider resolved it to an empty value")
)

// Read resolves ref through r's provider and registers the result for redaction before
// returning it. The registration is the point: every later write the caller passes
// through [Resolver.Redact] masks the value. Every failure is a *[ReadError], or a
// *[NoResolverError] for a nil Resolver.
//
// A value shorter than [MinMaskLen] is returned but NOT registered, and the
// [WithShortSecret] notice is told about it; with no notice it is a *[ReadError] wrapping
// a *[ShortSecretError], because the caller would have no other way to learn that the
// value is unprotected.
//
// Concurrent reads of one reference share a single provider invocation, and a reference
// is fetched once per provider for the Resolver's life. The shared fetch runs under the
// first caller's context values but none of its cancellation, bounded by the longer of
// the [Timeouts]; a caller whose own context ends stops waiting and the fetch carries on
// for the rest.
func (r *Resolver) Read(ctx context.Context, ref string) (Secret, error) {
	if r == nil {
		return Secret{}, &NoResolverError{}
	}
	if ref == "" {
		return Secret{}, &ReadError{Provider: r.provider, Err: errEmptyRef}
	}

	key := resolvedKey{provider: r.provider, ref: ref}
	if v, hit := r.kept(key); hit {
		return v, nil
	}

	flight := r.group.DoChan(key.provider+"\x00"+key.ref, func() (any, error) {
		return r.fetch(ctx, key)
	})
	select {
	case <-ctx.Done():
		return Secret{}, &ReadError{Ref: ref, Provider: r.provider, Err: ctx.Err()}
	case res := <-flight:
		if res.Err != nil {
			return Secret{}, res.Err
		}
		v, ok := res.Val.(Secret)
		if !ok {
			// Unreachable: the flight returns only a Secret or an error. Checked rather
			// than asserted, so a future edit that returns another type fails here
			// instead of panicking inside a credential path.
			return Secret{}, &ReadError{Ref: ref, Provider: r.provider, Err: fmt.Errorf("provider returned %T, not a secret", res.Val)}
		}
		return v, nil
	}
}

// kept returns the value key already resolved to.
func (r *Resolver) kept(key resolvedKey) (Secret, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, hit := r.resolved[key]
	return v, hit
}

// fetch is one flight of [Resolver.Read]. Detached from the caller's cancellation
// because the flight is shared: the caller that happened to start it giving up must not
// fail every other waiter.
func (r *Resolver) fetch(callerCtx context.Context, key resolvedKey) (Secret, error) {
	// Re-checked INSIDE the flight. A caller that missed the kept answers just as a previous
	// flight was completing would otherwise start a second one and invoke the provider
	// again, the exact duplicate unlock prompt this exists to prevent.
	if v, hit := r.kept(key); hit {
		return v, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(callerCtx), r.timeouts.longest())
	defer cancel()

	val, err := r.askProvider(ctx, key)
	if err != nil {
		return Secret{}, &ReadError{Ref: key.ref, Provider: key.provider, Err: err}
	}
	if val.IsZero() {
		return Secret{}, &ReadError{Ref: key.ref, Provider: key.provider, Err: errEmptyValue}
	}
	if val.Len() < MinMaskLen {
		if r.short == nil {
			return Secret{}, &ReadError{Ref: key.ref, Provider: key.provider, Err: &ShortSecretError{Len: val.Len()}}
		}
		r.short(ctx, key.ref)
	}
	r.record(key, val)
	return val, nil
}

// askProvider asks key's provider for its reference. With none selected it reads through
// the fallback, and refuses when there is none rather than guessing at the environment.
func (r *Resolver) askProvider(ctx context.Context, key resolvedKey) (Secret, error) {
	if key.provider == "" {
		if r.fallback == nil {
			return Secret{}, ErrNoFallback
		}
		return r.fallback.Fetch(ctx, key.ref)
	}
	if r.open == nil {
		return Secret{}, errNoOpener
	}
	p, err := r.open(ctx, key.provider)
	if err != nil {
		return Secret{}, fmt.Errorf("open provider: %w", err)
	}
	return p.Fetch(ctx, key.ref)
}

// record keeps a resolved value and, when long enough to mask safely, adds it to the
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
func (r *Resolver) record(key resolvedKey, v Secret) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved == nil {
		r.resolved = make(map[resolvedKey]Secret)
	}
	r.resolved[key] = v
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

// Register adds v and its encoded forms to the redaction set without keeping it as a
// resolved reference: for a credential the caller mints or receives outside a provider (a
// token read from stdin). A value shorter than [MinMaskLen] cannot be masked and is a
// *[ShortSecretError], whatever [WithShortSecret] says, since the caller is handed nothing
// to warn about; a nil Resolver is a *[NoResolverError].
func (r *Resolver) Register(v Secret) error {
	if r == nil {
		return &NoResolverError{}
	}
	if v.Len() < MinMaskLen {
		return &ShortSecretError{Len: v.Len()}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, form := range encodedForms(v) {
		r.addRedactable(form)
	}
	return nil
}

// ShortSecretError is a value shorter than [MinMaskLen], which no redaction masks: from
// [Resolver.Register] always, and from [Resolver.Read] when no [WithShortSecret] notice
// hears it.
type ShortSecretError struct {
	Len int
}

func (e *ShortSecretError) Error() string {
	return fmt.Sprintf("the value is %d characters, below the %d redaction masks, so it would print in the clear", e.Len, MinMaskLen)
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
// is the caller's own buffer, handed straight back. A nil Resolver is a
// *[NoResolverError] and returns nothing, because nothing it could return is known
// to be masked.
//
// LIMITS, which callers must not overstate:
//   - Only values read or registered through this resolver are known.
//   - It is literal substring replacement. The base64, hex and percent-escaped forms are
//     registered alongside the raw value; ANY other transform (a different alphabet,
//     compression, splitting the value across a delimiter) defeats it.
//   - A secret straddling two calls is masked only if both halves land in one call.
func (r *Resolver) Redact(p []byte) ([]byte, error) {
	if r == nil {
		return nil, &NoResolverError{}
	}
	return r.redact(p), nil
}

func (r *Resolver) redact(p []byte) []byte {
	if len(p) == 0 {
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
func (r *Resolver) RedactString(s string) (string, error) {
	if r == nil {
		return "", &NoResolverError{}
	}
	return r.redactString(s), nil
}

func (r *Resolver) redactString(s string) string {
	if s == "" {
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
// caller's own buffer) when nothing matched. A ctx with no resolver is a
// *[NoResolverError].
func Redact(ctx context.Context, p []byte) ([]byte, error) {
	return ResolverFromContext(ctx).Redact(p)
}

// RedactString redacts s using ctx's resolver. A ctx with no resolver is a
// *[NoResolverError].
func RedactString(ctx context.Context, s string) (string, error) {
	return ResolverFromContext(ctx).RedactString(s)
}
