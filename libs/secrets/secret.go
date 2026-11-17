package secrets

// mask is what every rendering of a [Secret] prints and what [Resolver.Redact] writes in
// place of a secret. Fixed width, so the mask does not leak the length.
const mask = "***"

// Secret is a credential that redacts itself: fmt, slog and every text encoder print the
// mask, since redact-at-write sees what fmt renders, not what an encoder emits. Reveal is
// the only way out. No UnmarshalText or UnmarshalJSON, so decoding plaintext into one
// fails loudly; no exported field, so gob refuses it.
//
// The plaintext sits behind a pointer because fmt calls no method on an unexported field
// and prints a pointer there as an address. The same holds for the verbs String and
// GoString do not serve (%d, %p, %T and the rest): they print the struct, so an address.
// So == compares identity: compare by Reveal.
type Secret struct{ p *string }

// New wraps plaintext. A zero Secret is an empty credential.
func New(plaintext string) Secret { return Secret{p: &plaintext} }

// Reveal returns the plaintext. It is the only way out, so every place a credential stops
// protecting itself is one grep away.
func (v Secret) Reveal() string {
	if v.p == nil {
		return ""
	}
	return *v.p
}

// IsZero reports whether the value is empty, without revealing it.
func (v Secret) IsZero() bool { return v.Len() == 0 }

// Len is the plaintext length, for checks that reason about length without revealing.
func (v Secret) Len() int {
	if v.p == nil {
		return 0
	}
	return len(*v.p)
}

// String masks %v, %+v, %s, %q, %x and %X under any flag or width. %x and %X
// print the mask in hex.
func (Secret) String() string { return mask }

// GoString masks %#v, which String does not reach.
func (Secret) GoString() string { return mask }

// MarshalText masks every encoder that honors encoding.TextMarshaler: encoding/json,
// yaml.v3, go-toml and slog's JSON and text handlers all do.
func (Secret) MarshalText() ([]byte, error) { return []byte(mask), nil }
