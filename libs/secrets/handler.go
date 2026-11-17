package secrets

import (
	"context"
	"encoding/json"
	"log/slog"
)

// redactingHandler wraps a slog.Handler so every record's message and attributes are
// redacted before the underlying handler formats them. See redactValue for which attr
// shapes are covered by construction and which are only best-effort.
//
// Redaction that lives in ONE handler's print path is a per-call-site rule: a caller that
// picks its handler by output format leaks through every format but the one that
// redacts. Wrapping the chosen handler covers every format by construction, including
// one added later.
type redactingHandler struct {
	inner slog.Handler
	// plain is inner with every pre op ALREADY applied, unredacted. It is the handler used
	// when the resolver holds nothing to redact, which is the overwhelmingly common case;
	// building it as the ops arrive keeps the no-secret log path free of a per-record
	// rebuild. It is never used once a secret exists; then pre is replayed instead.
	plain slog.Handler
	// pre records WithAttrs/WithGroup calls in order instead of forwarding them to inner
	// immediately. Those calls carry no context, so there is no resolver to redact
	// against at the time they are made; forwarding them meant a logger built with
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

// NewRedactingHandler wraps inner so every record's message and attribute values are
// masked against the resolver on the record's context before inner sees them. A record
// whose context has no resolver, or one holding nothing, passes through a chain built once
// at WithAttrs/WithGroup time, at the cost of one length check.
func NewRedactingHandler(inner slog.Handler) slog.Handler {
	return redactingHandler{inner: inner, plain: inner}
}

func (h redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h redactingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	h.pre = append(append([]handlerOp(nil), h.pre...), handlerOp{attrs: as})
	h.plain = h.plain.WithAttrs(as)
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
	h.plain = h.plain.WithGroup(n)
	return h
}

// redactValue returns v with any secret occurrence masked, preserving the value's kind
// wherever it can.
//
// A string attr is not the only carrier: an argv logged as a []string is KindAny to slog,
// so a string-only filter would let a JSON handler print a subprocess command line
// verbatim. []string and []byte are handled by type so the
// redacted attr keeps its JSON shape rather than collapsing to a rendered string.
//
// KNOWN LIMIT, not full coverage: the final arm compares against what fmt renders, but
// the handler emits what its encoder produces. A json.Marshaler, or a struct holding a
// credential in a field String() hides, passes untouched. Closing that needs the value to
// redact ITSELF at every formatting entry point.
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
		// Two renderings, because a handler does not necessarily print the one fmt
		// produces. A type whose String() conceals a credential while its exported fields
		// still marshal (`func (c creds) String() string { return "creds{redacted}" }`)
		// passes an fmt comparison and then encoding/json emits {"Token":"ghp_..."}.
		// Checking the JSON form as well closes that without asking every value to
		// cooperate. Reached only when the resolver holds something, so the marshal is off
		// the ordinary path entirely.
		rendered := v.String()
		if red := r.RedactString(rendered); red != rendered {
			return slog.StringValue(red)
		}
		if enc, err := json.Marshal(v.Any()); err == nil {
			if red := r.RedactString(string(enc)); red != string(enc) {
				// The value is unsafe in its own encoding, so hand the handler text it
				// cannot re-expand: the redacted JSON, as a string.
				return slog.StringValue(red)
			}
		}
		return v
	default:
		return v
	}
}

// redactAttrs returns as with every VALUE redacted. Keys are deliberately left alone.
//
// An attr key is conventionally a compile-time constant naming a field, and a renderer may
// read keys back by exact name. Masking a key cannot fail loudly; it silently drops the
// field the renderer looked for. A call site that builds a key from untrusted data
// redacts it there, through [RedactString].
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

	// Nothing to redact: hand the record to the chain WithAttrs/WithGroup already built.
	// Replaying h.pre here would rebuild the whole handler chain once per log line for
	// the common run that never read a secret.
	if !res.hasSecrets() {
		return h.plain.Handle(ctx, r)
	}

	// Something to redact, so the preset attrs have to be rebuilt: they were captured by
	// WithAttrs, which carries no context and therefore had no resolver to redact against.
	inner := h.inner
	for _, op := range h.pre {
		if op.group != "" {
			inner = inner.WithGroup(op.group)
			continue
		}
		inner = inner.WithAttrs(redactAttrs(res, op.attrs))
	}

	out := slog.NewRecord(r.Time, r.Level, res.RedactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		a.Value = redactValue(res, a.Value)
		out.AddAttrs(a)
		return true
	})
	return inner.Handle(ctx, out)
}
