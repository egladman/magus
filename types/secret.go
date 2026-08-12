package types

import (
	"net"
	"strings"
)

// SecretGrant declares one credential and the single destination it may be sent to. It
// is what [magus\secret.endpoint] takes: magus binds a loopback URL, a child process is
// pointed at that instead of the real API, and magus attaches the credential on the way
// upstream. The child never holds it.
//
// It exists for the case `magus\secret.read` cannot serve. A read hands the value to
// your magusfile, which is fine when the consumer is your own code - magus knows the
// value is a credential and masks it out of everything it writes. It is not fine when
// the consumer is a SUBPROCESS that decides at runtime what to do and can be induced to
// print its own environment. There is nothing to redact in another process.
//
// magus briefly attached these to its own http\* calls too. That was cut: the plaintext
// is in magus's memory either way (the resolver memoizes it, and the redaction set must
// retain it), so withholding it from a Buzz variable in the same process bought close to
// nothing, while the host matching and per-hop redirect re-checking it needed produced
// two credential-leak bugs. The host here is ROUTING, not matching - the forwarder has
// to know where to send.
//
// Named a grant rather than a binding because `binding` already means the Go/Buzz
// trampoline layer in this repo (internal/interp/bindings), and a second meaning on
// that word would collide at every call site a contributor reads.
//
// What it removes is the credential's presence in whatever consumes it. What it
// does NOT remove is that consumer's ability to SPEND the credential within this
// grant's scope: a child pointed at the loopback endpoint can issue any request the
// grant permits. It stops exfiltration, not use. Both sentences belong together
// wherever this is described, per docs/concepts/secrets.md.
//
// Always [SecretGrant.Normalize] a grant before storing or matching one. The zero
// comparison rules below assume canonical fields.
type SecretGrant struct {
	// Ref is the credential reference, resolved through the run's selected provider
	// exactly as magus\secret.read resolves one. magus never parses it: an
	// environment variable name under the built-in provider, an op:// path under a
	// 1Password spell. See docs/concepts/secrets.md for why there is no URI scheme.
	Ref string
	// Host is the only destination this credential may be attached to, matched
	// against a request URL's host. A port is part of the match when present, so
	// "localhost:8080" and "localhost" are different destinations.
	//
	// ASCII-lowercased by Normalize, and non-ASCII is REJECTED rather than folded.
	// Case-insensitive comparison via strings.EqualFold was a real hole: it applies
	// Unicode simple case folding, under which U+017F folds to "s" and U+212A to "k",
	// so "hookſ.slack.com" satisfied a grant for "hooks.slack.com". Punycode an
	// internationalized host yourself; magus will not guess an encoding for the one
	// field that decides where a credential may go.
	//
	// No wildcards, deliberately. A pattern is how the placeholder-matching designs
	// leak: "*.example.com" reads as a convenience until a subdomain someone else
	// controls satisfies it. Declaring a second grant is cheap and says what it means.
	Host string
	// Header names the request header the value is attached to, e.g. "Authorization"
	// or "X-API-Key".
	Header string
	// Prefix is written before the value in the header, e.g. "Bearer " or "token ".
	// Empty sends the value alone, which is what a bare X-API-Key wants.
	//
	// A prefix rather than a format template with a placeholder: Buzz interpolates
	// braces inside a string literal, so a "{}" placeholder would be a lexer hazard
	// for no reach a prefix does not already have. `Authorization: Basic` is NOT
	// expressible here and the docs say so rather than implying coverage.
	Prefix string
}

// Normalize validates g and returns it in canonical form, with the message naming
// the field at fault.
//
// Every failure carries [SecretGrantInvalid]. A malformed grant is a magusfile authoring
// mistake with a documented resolution, and it is the one declaration that decides where
// a credential may go - so it gets a lookupable code rather than a bare string, and a
// caller can branch on `e.code == "MGS1027"`. One code for every clause: the resolution
// is the same in each case (fix the declaration the message names), and the specifics
// are in the message.
//
// One function rather than a Validate/canonicalize pair because splitting them is
// what produced a live defect: the old Validate tested strings.TrimSpace(g.Host) and
// then stored the UNTRIMMED value, so `host = " api.example.com"` passed and then
// matched nothing forever, sending every request unauthenticated with no error
// anywhere. A caller that cannot get the checked value back cannot use it.
func (g SecretGrant) Normalize() (SecretGrant, error) {
	g.Ref = strings.TrimSpace(g.Ref)
	g.Host = canonicalHost(g.Host)
	g.Header = strings.TrimSpace(g.Header)

	switch {
	case g.Ref == "":
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant: ref is required")
	case g.Host == "":
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: host is required, and names the only destination this credential may be sent to", g.Ref)
	case g.Header == "":
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: header is required", g.Ref)
	case strings.ContainsAny(g.Host, "*?"):
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: host %q contains a wildcard; declare one grant per destination", g.Ref, g.Host)
	case strings.ContainsAny(g.Host, "/@"):
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: host %q must be a bare host[:port], not a URL or userinfo", g.Ref, g.Host)
	case !isASCII(g.Host):
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: host %q is not ASCII; punycode an internationalized host yourself, because case folding a non-ASCII host would let a lookalike name satisfy this grant", g.Ref, g.Host)
	case !validHeaderName(g.Header):
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: header %q is not a valid HTTP header name", g.Ref, g.Header)
	case strings.ContainsAny(g.Host, " \t\r\n"):
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: host %q must not contain whitespace", g.Ref, g.Host)
	case strings.ContainsAny(g.Ref, "\r\n"):
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: ref must not contain a newline", g.Ref)
	case strings.ContainsAny(g.Prefix, "\r\n"):
		return SecretGrant{}, DiagnosticErrorf(SecretGrantInvalid, "secret grant %q: prefix must not contain a newline", g.Ref)
	}
	return g, nil
}

// canonicalHost folds the spellings of one destination onto a single form: ASCII
// lowercase, no trailing dot on the name, and no explicit :443.
//
// Fixing a silent failure, not adding leniency. "api.example.com:443" and
// "api.example.com." are the SAME destination over https, and a grant for the bare name
// matched neither - so every request went out unauthenticated, forever, with no error
// anywhere. That is exactly the failure mode Normalize exists to rule out.
//
// It does NOT widen the scope. A non-default port is still part of the match, so
// "api.example.com:8443" remains a different destination.
//
// Only :443 is folded, NOT :80. An earlier version stripped both, which quietly turned a
// grant explicitly pinned to "api.example.com:80" into one for the bare name - and since
// a credential may only travel over https (or loopback), port 80 on a routable host is
// never the destination the author meant. Folding it made two different destinations one.
//
// Uses net.SplitHostPort rather than strings.Cut, which mangled IPv6: cutting at the
// FIRST colon turned "[::1]:443" into name "[" and port ":1]:443", so neither the port
// fold nor the trailing-dot trim did anything sane. loopbackHost already had this right.
func canonicalHost(host string) string {
	h := asciiLower(strings.TrimSpace(host))
	name, port, err := net.SplitHostPort(h)
	if err != nil {
		// No port (or unparseable). Strip IPv6 brackets so the bare form agrees with what
		// the ported branch produces - SplitHostPort removes them, so leaving them here
		// made "[::1]" and "[::1]:443" two different destinations.
		return strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	}
	name = strings.TrimSuffix(name, ".")
	if port == "443" {
		return name
	}
	return net.JoinHostPort(name, port)
}

// asciiLower lowercases A-Z and leaves every other byte alone. Deliberately NOT
// strings.ToLower, which is Unicode-aware and therefore maps distinct codepoints
// onto the same result - the property that made EqualFold unusable here.
func asciiLower(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b == nil {
				b = []byte(s)
			}
			b[i] = c + ('a' - 'A')
		}
	}
	if b == nil {
		return s
	}
	return string(b)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// validHeaderName reports whether s is an RFC 7230 field-name. Checked here rather
// than left to the transport: net/http rejects a bad name at request time, which is
// a failure a long way from the magusfile line that declared it.
func validHeaderName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return s != ""
}
