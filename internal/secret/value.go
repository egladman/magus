package secret

import (
	"fmt"
	"io"
	"log/slog"
)

// Value is a resolved credential. Every standard way of rendering it yields [mask], so
// the plaintext reaches output only where a caller explicitly asked for it by name.
//
// This is the second of the three mechanisms this package needs, and it exists because
// the first one cannot be finished. Redact-at-write intercepts BYTES on their way out
// (the capture tap, journal.Emit, the proc/run exec buffer, the slog handler) and that
// is correct for captured subprocess output, which really is bytes. It is structurally
// unable to cover VALUES: a formatting boundary compares against what fmt renders while
// the handler emits what its ENCODER produces, and those differ. The case that settled
// it, reproduced against the real handler:
//
//	slog.Any("b", []byte("ghp_probe_token_value"))
//	  fmt renders it -> "[103 104 112 ...]"          (the redactor sees no match)
//	  encoding/json  -> "Z2hwX3Byb2JlX3Rva2VuX3ZhbHVl" (decodes to the token)
//
// No amount of additional kind-handling closes that, because the interceptor cannot know
// what an arbitrary downstream encoder will do. The value has to redact itself.
//
// It does NOT replace redact-at-write. Both are required: this covers return values,
// structured log attributes and descriptor fields; the write interceptors cover bytes a
// child process printed, which magus never held as a Value.
//
// LIMITS, which must not be overstated:
//
//  1. It protects the Value, not a string a caller already unwrapped. Once [Value.Reveal]
//     is called the result is an ordinary string with nothing behind it.
//  2. It does not cover captured subprocess bytes. A child printing its own token
//     produces bytes magus never held; the capture tap remains the only thing that
//     catches those.
//  3. It does not survive os.Environ(). A child receiving the credential in its
//     environment can leak it however it likes. That is inherent, and it is the gap a
//     [SecretGrant] endpoint exists to close.
//
// No zeroing, deliberately: a Go string is immutable and non-addressable, and the
// redaction set must retain the plaintext for [Resolver.Redact] to match against at all.
// The retention is documented rather than pretended away.
type Value struct{ s string }

// NewValue wraps plaintext. Unexported plaintext with an exported constructor so the
// only way to build one is deliberate, and a zero Value is an empty credential rather
// than an accidental unwrap.
func NewValue(s string) Value { return Value{s: s} }

// Reveal returns the plaintext. The ONLY way out, and named so a reviewer can grep for
// every place a credential stops being self-protecting. If the count grows much past a
// handful, the boundary is in the wrong place and the type is being unwrapped too
// eagerly.
func (v Value) Reveal() string { return v.s }

// IsZero reports whether the value is empty, without revealing it. Lets a caller check
// for "nothing resolved" without reaching for Reveal.
func (v Value) IsZero() bool { return v.s == "" }

// Len is the plaintext length. Needed by the short-value warning, which must reason
// about the length without unwrapping.
func (v Value) Len() int { return len(v.s) }

// String masks. Covers fmt's %s and %v and anything calling String() directly.
func (Value) String() string { return mask }

// GoString masks %#v, which String does not cover.
func (Value) GoString() string { return mask }

// Format is the load-bearing one: implementing it makes EVERY fmt verb print the mask,
// including %q and %x, which String alone does not reach.
func (Value) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, mask) }

// LogValue masks in slog, before any handler formats it.
func (Value) LogValue() slog.Value { return slog.StringValue(mask) }

// MarshalText closes the encoder path that defeated the redacting handler: a type whose
// fmt form looks clean while its marshalled form carries the credential.
//
// This ALSO covers encoding/json, which falls back to TextMarshaler for a type that
// implements no json.Marshaler and quotes the result. A separate MarshalJSON was written
// first and then removed: no test could distinguish it from this one, which makes it
// exactly the speculative method that rots. If a future encoder needs a non-string JSON
// form, add it back with a test that fails without it.
func (Value) MarshalText() ([]byte, error) { return []byte(mask), nil }
