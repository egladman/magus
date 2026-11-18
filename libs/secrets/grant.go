package secrets

import (
	"fmt"
	"net"
	"strings"
)

// Grant declares one credential and the single destination it may be sent to. It is what
// [Resolver.OpenEndpoint] takes: the resolver binds a loopback URL, a child process is
// pointed at that instead of the real API, and the forwarder attaches the credential on
// the way upstream. The child never holds it.
//
// It exists for the case [Resolver.Read] cannot serve. A read hands the value to the
// caller, which is fine when the consumer is the caller's own code: the value is known to
// be a credential and masked out of everything recorded. It is not fine when the consumer
// is a SUBPROCESS that decides at runtime what to do and can be induced to print its own
// environment. There is nothing to redact in another process.
//
// The host is ROUTING, not matching: the forwarder has to know where to send.
//
// What it removes is the credential's presence in whatever consumes it. What it does NOT
// remove is that consumer's ability to SPEND the credential within this grant's scope: a
// child pointed at the loopback endpoint can issue any request the grant permits. It stops
// exfiltration, not use. Both sentences belong together wherever this is described.
//
// Always [Grant.Normalize] a grant before storing or matching one. The comparison rules
// below assume canonical fields.
type Grant struct {
	// Ref is the credential reference, resolved through the resolver's selected provider
	// exactly as [Resolver.Read] resolves one. Never parsed: an environment variable name
	// under the built-in provider, a vault path under another.
	Ref string
	// Host is the only destination this credential may be attached to. A port is part of
	// the match when present, so "localhost:8080" and "localhost" are different
	// destinations.
	//
	// ASCII-lowercased by Normalize, and non-ASCII is REJECTED rather than folded.
	// strings.EqualFold applies Unicode simple case folding, under which U+017F folds to
	// "s" and U+212A to "k", so "hookſ.slack.com" would satisfy a grant for
	// "hooks.slack.com". Punycode an internationalized host yourself; nothing here guesses
	// an encoding for the one field that decides where a credential may go.
	//
	// No wildcards, deliberately: "*.example.com" reads as a convenience until a subdomain
	// someone else controls satisfies it. Declaring a second grant is cheap and says what
	// it means.
	Host string
	// Header names the request header the value is attached to, e.g. "Authorization"
	// or "X-API-Key".
	Header string
	// Prefix is written before the value in the header, e.g. "Bearer " or "token ".
	// Empty sends the value alone, which is what a bare X-API-Key wants.
	// `Authorization: Basic` is NOT expressible here.
	Prefix string
}

// GrantError is why [Grant.Normalize] refused a grant. Ref is the grant's reference after
// trimming, empty when the reference itself is what is missing; Reason names the field at
// fault.
type GrantError struct {
	Ref    string
	Reason string
}

func (e *GrantError) Error() string {
	if e.Ref == "" {
		return "secrets: grant: " + e.Reason
	}
	return fmt.Sprintf("secrets: grant %q: %s", e.Ref, e.Reason)
}

// Normalize validates g and returns it in canonical form. Every refusal is a *GrantError.
//
// One function rather than a Validate/canonicalize pair: a caller that cannot get the
// checked value back cannot use it. A separate Validate tested strings.TrimSpace(g.Host)
// while the caller stored the untrimmed value, so `host = " api.example.com"` passed and
// then matched nothing, sending every request unauthenticated with no error anywhere.
func (g Grant) Normalize() (Grant, error) {
	g.Ref = strings.TrimSpace(g.Ref)
	g.Host = canonicalHost(g.Host)
	g.Header = strings.TrimSpace(g.Header)

	refuse := func(format string, args ...any) (Grant, error) {
		return Grant{}, &GrantError{Ref: g.Ref, Reason: fmt.Sprintf(format, args...)}
	}
	switch {
	case g.Ref == "":
		return refuse("ref is required")
	case g.Host == "":
		return refuse("host is required, and names the only destination this credential may be sent to")
	case hostName(g.Host) == "":
		// A dial of ":8080" reaches this machine, which the grant never named.
		return refuse("host %q names a port and no host", g.Host)
	case g.Header == "":
		return refuse("header is required")
	case strings.ContainsAny(g.Host, "*?"):
		return refuse("host %q contains a wildcard; declare one grant per destination", g.Host)
	case strings.ContainsAny(g.Host, "/@"):
		return refuse("host %q must be a bare host[:port], not a URL or userinfo", g.Host)
	case !isASCII(g.Host):
		return refuse("host %q is not ASCII; punycode an internationalized host yourself, because case folding a non-ASCII host would let a lookalike name satisfy this grant", g.Host)
	case !validHeaderName(g.Header):
		return refuse("header %q is not a valid HTTP header name", g.Header)
	case strings.ContainsAny(g.Host, " \t\r\n"):
		return refuse("host %q must not contain whitespace", g.Host)
	case strings.ContainsAny(g.Ref, "\r\n"):
		return refuse("ref must not contain a newline")
	case strings.ContainsAny(g.Prefix, "\r\n"):
		return refuse("prefix must not contain a newline")
	}
	return g, nil
}

// canonicalHost folds the spellings of one destination onto a single form: ASCII
// lowercase, no trailing dot on the name, and no explicit :443.
//
// "api.example.com:443" and "api.example.com." are the SAME destination over https, and a
// grant for the bare name would match neither, sending every request out unauthenticated
// with no error anywhere.
//
// It does NOT widen the scope. A non-default port is still part of the match, so
// "api.example.com:8443" remains a different destination. Only :443 is folded, NOT :80: a
// credential may only travel over https (or loopback), so folding :80 would turn a grant
// pinned to "api.example.com:80" into one for a destination the author did not name.
//
// net.SplitHostPort rather than a cut at the first colon, which would turn "[::1]:443"
// into name "[" and port ":1]:443". An IPv6 literal keeps its brackets with or without a
// port, because the forwarder writes Host into a URL, where "::1" alone does not parse
// as a host.
func canonicalHost(host string) string {
	h := asciiLower(strings.TrimSpace(host))
	name, port, err := net.SplitHostPort(h)
	if err != nil {
		// No port (or unparsable): a bare name, "[::1]", or an unbracketed "::1".
		h = strings.TrimSuffix(h, ".")
		if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
			h = h[1 : len(h)-1]
		}
		return bracketIPv6(h)
	}
	name = strings.TrimSuffix(name, ".")
	if port == "443" {
		return bracketIPv6(name)
	}
	return net.JoinHostPort(name, port)
}

// hostName is the name part of a canonical host, without its port or brackets.
func hostName(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return strings.Trim(host, "[]")
}

// bracketIPv6 brackets name when it is an IPv6 literal, the one host form with a colon.
func bracketIPv6(name string) string {
	if strings.Contains(name, ":") {
		return "[" + name + "]"
	}
	return name
}

// asciiLower lowercases A-Z and leaves every other byte alone. Deliberately NOT
// strings.ToLower, which is Unicode-aware and therefore maps distinct codepoints
// onto the same result: the property that makes EqualFold unusable here.
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
// a failure a long way from the line that declared it.
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
