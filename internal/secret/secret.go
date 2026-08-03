// Package secret resolves credential references through a workspace's selected secret
// provider, and remembers the values it handed out so magus can keep them out of
// everything it persists.
//
// WHERE a secret comes from is a spell's problem: a provider is an ordinary magus spell,
// so 1Password, Vault or AWS Secrets Manager are `os\exec` away and the engine grows no
// per-provider code. What only the engine can do is know that a value IS a secret, and a
// magusfile cannot express that at any level of cleverness - which is why this package
// exists rather than a magusfile helper that shells out to `op read`.
//
// A value is a secret because it was read through a [Resolver], never because its name
// looked credential-shaped. That removes the "forgot to classify it" failure mode and
// makes redaction exact over the bytes magus actually produced.
//
// State is per-WORKSPACE-OPEN, carried on the context, not per-process. That scope is a
// trade with a sharp edge worth stating: one magusfile evaluation happens during
// preload and another during the run, so a narrower per-run scope made a top-level read
// invoke the provider twice - two interactive unlock prompts for one command. Sharing
// across the Open makes the second a memo hit.
//
// The cost is that the daemon holds an Open for as long as it serves a workspace, so
// resolved plaintext lives that long rather than for one run. It is still not
// process-global: a second workspace gets a second Resolver, so one workspace's value can
// never satisfy another's reference, and the memo is keyed by provider as well.
package secret

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Provider fetches the value behind an opaque reference. The reference format is the
// provider's own business - an environment variable name here, a vault path elsewhere -
// and magus never parses it. See docs/concepts/secrets.md for why there is no URI scheme.
type Provider interface {
	// Fetch returns ref's value, or errors if it cannot. A backend FETCHES; the
	// [Resolver] READS - it selects the provider, memoizes, and registers the value for
	// redaction, which is the security-critical part. Two verbs so the two contracts
	// cannot be confused at a call site.
	Fetch(ctx context.Context, ref string) (string, error)
}

// minRedactLen is the shortest value [Resolver.Redact] will mask. Below it, masking does
// more harm than good: a two-character "secret" occurs constantly in ordinary output, and
// redacting every occurrence would shred the logs the user needs while protecting
// something that was never a credential.
const minRedactLen = 4

// mask replaces a secret in captured output. Fixed width regardless of the value's
// length, so the redaction does not leak how long the secret was.
const mask = "***"

// envProvider is magus's built-in provider: a reference is an environment variable name.
// It applies when a magusfile selects no provider spell.
//
// Go rather than a bundled spell for a concrete reason: a spell importing a host module
// (`os`, here) cannot be bare-compiled into the binary - see cmd/magus-utils/spells.go -
// so an env provider written as a spell could not ship inside magus at all.
type envProvider struct{}

// Fetch returns the environment variable named by ref. Unset and set-to-empty are both
// errors: returning "" would hand the caller a credential-shaped blank that fails later
// at whatever consumes it, with an error far from the cause.
func (envProvider) Fetch(_ context.Context, ref string) (string, error) {
	v, ok := os.LookupEnv(ref)
	if !ok {
		return "", fmt.Errorf("$%s is not set", ref)
	}
	if v == "" {
		return "", fmt.Errorf("$%s is set but empty", ref)
	}
	return v, nil
}

// providerOpener builds a Provider from a selected spell's name. It is an extension
// point, not a hard dependency: a spell-backed provider needs the Buzz VM, so it lives in
// the interp bindings layer and registers itself here at init. This package stays free of
// the VM, the same indirection cache.RegisterRemoteBackendOpener uses.
//
// Guarded by its own mutex rather than relying on an unstated init-before-use invariant,
// because RegisterProviderOpener is exported and nothing enforces when it is called.
var (
	openerMu       sync.RWMutex
	providerOpener func(ctx context.Context, name string) (Provider, error)
)

// RegisterProviderOpener installs the opener [Resolver.Read] delegates to for a
// spell-backed provider. Meant to be called once, from the bindings layer's init; a
// second call panics rather than silently shadowing the first.
func RegisterProviderOpener(fn func(ctx context.Context, name string) (Provider, error)) {
	openerMu.Lock()
	defer openerMu.Unlock()
	if providerOpener != nil {
		panic("secret: provider opener already registered")
	}
	providerOpener = fn
}

func opener() func(context.Context, string) (Provider, error) {
	openerMu.RLock()
	defer openerMu.RUnlock()
	return providerOpener
}

// memoKey pairs a reference with the provider that resolved it. Keying on the reference
// alone was a real defect: a magusfile that reads before selecting a provider would
// memoize the built-in env fallback and then keep returning it under a declared
// 1Password provider - a silently wrong credential rather than an error.
type memoKey struct {
	provider string
	ref      string
}

// Resolver holds one run's secret state: which provider the magusfile selected, the
// values read so far, and the memo of what each reference resolved to.
//
// One per run, reached through the context, because magus's daemon outlives any single
// run. Process-global state here would mean plaintext resident for the daemon's lifetime
// and visible in a heap or core dump.
type Resolver struct {
	mu sync.RWMutex
	// providerName is the spell a magusfile selected; empty means the built-in.
	providerName string
	// memo avoids re-invoking a provider that shells out (`op read`, `vault kv get`).
	memo map[memoKey]string
	// redactable holds every distinct value read, longest first so [Resolver.Redact]
	// masks a containing secret before one nested inside it.
	redactable []string
	// timeouts bound a provider read, from magus.yaml. They ride the Resolver because it
	// is created where config is loaded, which keeps this package free of a config import
	// and of any global to reach around it.
	//
	// Set once at construction and never written again, so it is deliberately OUTSIDE
	// the mutex above: guarding it would put a lock acquisition on a hot-path-adjacent
	// struct to protect a field nothing mutates.
	timeouts Timeouts
	// group collapses concurrent reads of one key into a single provider invocation.
	// Without it, N targets resolving the same reference at once means N interactive
	// unlock prompts, which the memo alone does not prevent - the lookup and the fetch
	// cannot share a lock without serializing unrelated references too.
	group singleflight.Group
}

// Timeouts bounds how long a provider read may take. A struct rather than two duration
// arguments: they are the same type and adjacent, so a transposed pair compiles, runs, and
// shows no symptom beyond a build that waits six times too long or gives up six times too
// early.
type Timeouts struct {
	// Interactive applies when stdin is a terminal - long enough to find your phone for
	// a biometric.
	Interactive time.Duration
	// Unattended applies with no terminal to prompt on. Short, because a provider that
	// would prompt cannot, so waiting past the point a cached session would have answered
	// only delays a failure that is already certain.
	Unattended time.Duration
}

// DefaultTimeouts are the budgets used when magus.yaml sets none.
var DefaultTimeouts = Timeouts{Interactive: 60 * time.Second, Unattended: 10 * time.Second}

// Option configures a Resolver at construction. Options rather than post-construction
// setters because every field they touch is immutable afterwards, and a setter on a
// shared, mutex-guarded object invites callers to mutate it once it is in use.
type Option func(*Resolver)

// WithTimeouts sets the provider-read budgets. A zero field keeps the built-in default,
// so a partially-specified magus.yaml section still works.
func WithTimeouts(t Timeouts) Option {
	return func(r *Resolver) {
		if t.Interactive > 0 {
			r.timeouts.Interactive = t.Interactive
		}
		if t.Unattended > 0 {
			r.timeouts.Unattended = t.Unattended
		}
	}
}

// New returns an empty Resolver for one run.
func New(opts ...Option) *Resolver {
	r := &Resolver{timeouts: DefaultTimeouts}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Timeouts returns the budgets for a provider read. Unlocked: the field is written only
// at construction.
func (r *Resolver) Timeouts() Timeouts {
	if r == nil {
		return DefaultTimeouts
	}
	return r.timeouts
}

type resolverKey struct{}

// ContextWithResolver installs r as the run's resolver.
func ContextWithResolver(ctx context.Context, r *Resolver) context.Context {
	return context.WithValue(ctx, resolverKey{}, r)
}

// ResolverFromContext returns the run's resolver, or nil when none was installed. A nil
// Resolver is usable: every method on it degrades safely, so describe/parse paths that
// never set one up do not need to special-case it.
func ResolverFromContext(ctx context.Context) *Resolver {
	// The nil check is load-bearing: ctx.Value on a nil interface panics, and this is
	// reached from log handlers and redaction helpers whose whole contract is that they
	// degrade rather than fail.
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(resolverKey{}).(*Resolver)
	return r
}

// SetProviderName records the spell a magusfile selected. Selecting a second one replaces
// the first: last call wins, matching magus\ci.provider and magus\cache.remote.
func (r *Resolver) SetProviderName(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providerName = name
}

// ProviderName returns the selected spell's name, empty when the built-in applies.
func (r *Resolver) ProviderName() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providerName
}

// Read resolves ref through the selected provider and registers the result for redaction
// before returning it. The registration is the point: every later write of captured
// output passes through [Resolver.Redact], so a value that reaches this method is one
// magus can recognize on the way out.
//
// A value shorter than minRedactLen is returned but NOT registered - see that constant.
// Callers that care must treat such a value as unprotected; nothing reports it.
func (r *Resolver) Read(ctx context.Context, ref string) (string, error) {
	if r == nil {
		return "", errors.New("secret: no resolver on this context")
	}
	if ref == "" {
		return "", errors.New("secret: empty reference")
	}

	name := r.ProviderName()
	key := memoKey{provider: name, ref: ref}

	r.mu.RLock()
	v, hit := r.memo[key]
	r.mu.RUnlock()
	if hit {
		return v, nil
	}

	// singleflight rather than a lock held across the fetch: two targets reading the
	// same reference share one provider invocation, while two targets reading DIFFERENT
	// references still proceed in parallel.
	got, err, _ := r.group.Do(name+"\x00"+ref, func() (any, error) {
		// Re-checked INSIDE the flight. A caller that missed the memo just as a previous
		// flight was completing would otherwise start a second one and invoke the
		// provider again - the exact duplicate-unlock-prompt this exists to prevent.
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
		v, err = p.Fetch(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", ref, err)
		}
		if v == "" {
			return nil, fmt.Errorf("secret %q: provider resolved it to an empty value", ref)
		}
		r.record(key, v)
		return v, nil
	})
	if err != nil {
		return "", err
	}
	v, ok := got.(string)
	if !ok {
		// Unreachable: the flight function above returns only a string or an error.
		// Checked anyway rather than asserting, so a future edit that returns another
		// type fails loudly here instead of panicking inside a credential path.
		return "", fmt.Errorf("secret %q: provider returned %T, not a string", ref, got)
	}
	return v, nil
}

// provider returns the backend for name, falling back to the built-in when a magusfile
// selected none. Falling back rather than erroring is what makes CI zero-configuration:
// a workflow's env block is the only thing that can read a repository secret.
func (r *Resolver) provider(ctx context.Context, name string) (Provider, error) {
	if name == "" {
		return envProvider{}, nil
	}
	open := opener()
	if open == nil {
		return nil, errors.New("secret: no provider opener registered in this binary")
	}
	p, err := open(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("secret: open provider %q: %w", name, err)
	}
	return p, nil
}

// record memoizes a resolved value and, when long enough to mask safely, adds it to the
// redaction set ordered longest first. Longest-first matters: when one secret contains
// another, masking the shorter first would leave the longer one's remaining bytes in the
// output.
func (r *Resolver) record(key memoKey, v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.memo == nil {
		r.memo = make(map[memoKey]string)
	}
	r.memo[key] = v
	if len(v) < minRedactLen {
		return
	}
	for _, existing := range r.redactable {
		if existing == v {
			return
		}
	}
	r.redactable = append(r.redactable, v)
	for i := len(r.redactable) - 1; i > 0 && len(r.redactable[i]) > len(r.redactable[i-1]); i-- {
		r.redactable[i], r.redactable[i-1] = r.redactable[i-1], r.redactable[i]
	}
}

// Redact replaces every value this resolver has read with [mask]. It returns p unchanged,
// without copying, when there is nothing to do - the overwhelmingly common case, and this
// sits on the output-capture hot path.
//
// Callers must not retain p after a no-op return: it is the caller's own buffer, handed
// straight back.
//
// LIMITS, which callers must not overstate to users:
//   - Only values read through this resolver are known. A credential a magusfile read
//     with os\env, bypassing the provider, is invisible here.
//   - It is literal substring replacement. A child that base64-encodes, URL-escapes or
//     splits the value defeats it.
//   - A secret straddling two writes is masked only if both halves land in one call.
//     Captured output is redacted whole, so this limit applies to the live stream only.
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
		out = strings.ReplaceAll(out, v, mask)
	}
	if out == s {
		return p
	}
	return []byte(out)
}

// hasSecrets reports whether anything has been registered to redact. It lets a caller skip
// an expensive walk (rendering every attr of a log record) on the overwhelmingly common run
// that read no secret at all, where every comparison would provably find nothing.
func (r *Resolver) hasSecrets() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.redactable) > 0
}

// RedactString is [Resolver.Redact] for text already in string form. Same convention as
// the stdlib's regexp.Match / regexp.MatchString pair.
func (r *Resolver) RedactString(s string) string {
	if r == nil || s == "" {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.redactable {
		s = strings.ReplaceAll(s, v, mask)
	}
	return s
}

// Redact redacts p using the run's resolver, returning p unchanged - and UNCOPIED, still
// the caller's own buffer - when there is no resolver or nothing matched.
func Redact(ctx context.Context, p []byte) []byte { return ResolverFromContext(ctx).Redact(p) }

// RedactString redacts s using the run's resolver.
func RedactString(ctx context.Context, s string) string {
	return ResolverFromContext(ctx).RedactString(s)
}

// RedactingHandler wraps a slog.Handler so every record's message and attributes are
// redacted before the underlying handler formats them. See redactValue for which attr
// shapes are covered by construction and which are only best-effort.
//
// This exists because redaction that lives in ONE handler's print path is the same
// per-call-site mistake it was meant to end: magus picks a handler by `log.format`, and
// `--log-format=json` sent `run.exec`'s argv - documented as able to carry a credential
// when a magusfile passes one as an argument - straight to stderr unredacted. Wrapping the
// chosen handler covers every format by construction, including one added later.
type redactingHandler struct {
	inner slog.Handler
	// pre records WithAttrs/WithGroup calls in order instead of forwarding them to inner
	// immediately. Those calls carry no context, so there is no resolver to redact
	// against at the time they are made - forwarding them meant a logger built with
	// `slog.With("token", value)` bound the credential into the handler and printed it on
	// every later record, untouched. Replaying them in Handle, where a context exists, is
	// what closes that. Order is preserved because a group changes the nesting of the
	// attrs after it.
	pre []handlerOp
}

// handlerOp is one deferred WithAttrs or WithGroup call. Exactly one field is set.
type handlerOp struct {
	attrs []slog.Attr
	group string
}

// NewRedactingHandler wraps inner. Wrapping is unconditional and cheap: a run that read no
// secret - which is nearly all of them, and every run before the first read - short-circuits
// on hasSecrets before any attr is walked, so it costs one atomic-free length check.
func NewRedactingHandler(inner slog.Handler) slog.Handler { return redactingHandler{inner: inner} }

func (h redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h redactingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	h.pre = append(append([]handlerOp(nil), h.pre...), handlerOp{attrs: as})
	return h
}

func (h redactingHandler) WithGroup(n string) slog.Handler {
	// The slog.Handler contract requires an empty name to return the receiver unchanged.
	// Recording it would also be ambiguous on replay: an op with no group is how an attrs
	// op is spelled, so an empty group would replay as WithAttrs(nil).
	if n == "" {
		return h
	}
	h.pre = append(append([]handlerOp(nil), h.pre...), handlerOp{group: n})
	return h
}

// redactValue returns v with any secret occurrence masked, preserving the value's kind
// wherever it can.
//
// A string attr is not the only carrier: run.exec logs its argv as `"args", args` with a
// []string, which slog classifies as KindAny, and the earlier string-only filter walked
// straight past it - so `--log-format=json` printed a subprocess command line verbatim to
// stdout. []string and []byte are handled by type so the redacted attr keeps its JSON shape
// (an argv stays an array) rather than collapsing to a rendered string.
//
// KNOWN LIMIT, do not read this as full coverage: the final arm compares against what fmt
// renders, but the handler emits what its encoder produces. A type whose serialized form
// differs from its fmt form - a json.Marshaler, or any struct holding a credential in a
// field String() hides - passes this untouched. Closing that needs the secret value to
// redact ITSELF at every formatting entry point, which is what a secret.Value type is for;
// a handler cannot guess an arbitrary encoder's output. This arm is best-effort on top of
// the shapes above, not a guarantee.
func redactValue(r *Resolver, v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(r.RedactString(v.String()))
	case slog.KindGroup:
		as := v.Group()
		out := make([]slog.Attr, len(as))
		for i, a := range as {
			a.Value = redactValue(r, a.Value)
			out[i] = a
		}
		return slog.GroupValue(out...)
	case slog.KindLogValuer:
		return redactValue(r, v.Resolve())
	case slog.KindAny:
		switch t := v.Any().(type) {
		case []string:
			out := make([]string, len(t))
			for i, s := range t {
				out[i] = r.RedactString(s)
			}
			return slog.AnyValue(out)
		case []byte:
			// Rendered by fmt as decimal bytes but base64'd by encoding/json, so the
			// fallback below cannot see the secret in either form the reader gets.
			return slog.AnyValue(r.Redact(t))
		case error:
			if red := r.RedactString(t.Error()); red != t.Error() {
				return slog.StringValue(red)
			}
			return v
		}
		rendered := v.String()
		if red := r.RedactString(rendered); red != rendered {
			return slog.StringValue(red)
		}
		return v
	default:
		return v
	}
}

// redactAttrs returns as with every value redacted.
func redactAttrs(r *Resolver, as []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		a.Value = redactValue(r, a.Value)
		out[i] = a
	}
	return out
}

// Handle redacts the message and every attribute, then delegates.
func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	res := ResolverFromContext(ctx)

	// Replay the deferred WithAttrs/WithGroup calls now that a resolver is reachable.
	inner := h.inner
	for _, op := range h.pre {
		if op.group != "" {
			inner = inner.WithGroup(op.group)
			continue
		}
		attrs := op.attrs
		if res.hasSecrets() {
			attrs = redactAttrs(res, attrs)
		}
		inner = inner.WithAttrs(attrs)
	}
	// A Resolver is installed for every workspace Open, so `res != nil` is the common case
	// and would put a full render of every attr on the log path of runs that never read a
	// secret. hasSecrets is what keeps the wrapper as cheap as its doc claims.
	if !res.hasSecrets() {
		return inner.Handle(ctx, r)
	}

	out := slog.NewRecord(r.Time, r.Level, res.RedactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		a.Value = redactValue(res, a.Value)
		out.AddAttrs(a)
		return true
	})
	return inner.Handle(ctx, out)
}
