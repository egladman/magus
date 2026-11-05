package marker

import "strings"

// Compat opens the convention, with the space it always writes after the colon.
// What follows up to the closing paren is the retirement condition.
//
// Only InComment keeps this constant out of its own report. The trailing
// space cannot, because the constant carries it too.
const Compat = "compat(until: "

// Deprecated opens Go's deprecation paragraph.
const Deprecated = "Deprecated:"

// NextRelease is the condition of a development-only shim. Such a shim exists
// only while a breaking change lands, and a release refuses to ship it.
const NextRelease = "the next release"

// Found is one marker a line carries.
type Found struct {
	// Marker is Compat without its trailing space, or Deprecated.
	Marker string
	// Text is the retirement condition, or the deprecation's advice.
	Text string
}

// Parse returns the marker text carries in a comment. A compat marker with
// an empty condition is not one.
func Parse(text string) (Found, bool) {
	if idx := strings.Index(text, Compat); idx >= 0 && InComment(text[:idx]) {
		if until := Until(text[idx:]); until != "" {
			return Found{Marker: strings.TrimSpace(Compat), Text: until}, true
		}
	}
	if idx := strings.Index(text, Deprecated); idx >= 0 && InComment(text[:idx]) {
		return Found{Marker: Deprecated, Text: strings.TrimSpace(text[idx+len(Deprecated):])}, true
	}
	return Found{}, false
}

// DevOnly reports whether text carries a compat marker whose condition is
// exactly NextRelease.
func DevOnly(text string) bool {
	f, ok := Parse(text)
	return ok && f.Marker == strings.TrimSpace(Compat) && f.Text == NextRelease
}

// InComment reports whether the text before a marker opens a comment. A match
// outside a comment mentions the convention rather than uses it.
//
// It cannot tell a `//` inside a string from one that starts a comment. That
// noise is confined to files whose subject is this marker.
func InComment(before string) bool {
	if strings.Contains(before, "//") || strings.Contains(before, "#") || strings.Contains(before, "/*") {
		return true
	}
	// A block comment's continuation lines carry only a leading star.
	return strings.HasPrefix(strings.TrimLeft(before, " \t"), "*")
}

// Until extracts the condition from s, which starts at Compat. It returns
// empty when there is none.
//
// A condition that wraps past the line is truncated, not dropped. Half of it
// still says what kind of thing retires the code.
func Until(s string) string {
	rest := strings.TrimPrefix(s, Compat)
	if end := strings.Index(rest, ")"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}
