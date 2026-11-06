package marker

import "strings"

// Compat opens the convention, with the space it always writes after the colon.
// What follows up to the closing paren is the retirement condition.
//
// Only inComment keeps this constant out of its own report. The trailing
// space cannot, because the constant carries it too.
const Compat = "compat(until: "

// deprecated opens Go's deprecation paragraph.
const deprecated = "Deprecated:"

// NextRelease is the condition of a development-only shim. Such a shim exists
// only while a breaking change lands, and a release refuses to ship it.
const NextRelease = "the next release"

// Found is one marker a line carries.
type Found struct {
	// Marker is Compat without its trailing space, or "Deprecated:".
	Marker string
	// Text is the retirement condition, or the deprecation's advice.
	Text string
}

// Parse returns the marker text carries in a comment. A compat marker with
// an empty condition is not one.
func Parse(text string) (Found, bool) {
	if idx := strings.Index(text, Compat); idx >= 0 && inComment(text[:idx]) {
		if cond := until(text[idx:]); cond != "" {
			return Found{Marker: strings.TrimSpace(Compat), Text: cond}, true
		}
	}
	if idx := strings.Index(text, deprecated); idx >= 0 && inComment(text[:idx]) {
		return Found{Marker: deprecated, Text: strings.TrimSpace(text[idx+len(deprecated):])}, true
	}
	return Found{}, false
}

// DevOnly reports whether text carries a compat marker whose condition is
// exactly NextRelease.
func DevOnly(text string) bool {
	cond, ok := compatCondition(text)
	return ok && cond == NextRelease
}

// NearDevOnly reports whether text's compat condition reads as NextRelease
// once case, spacing and trailing punctuation are ignored, but is not it. A
// release refuses one: it means the dev-only shim and would ship unnoticed.
func NearDevOnly(text string) bool {
	cond, ok := compatCondition(text)
	if !ok || cond == NextRelease {
		return false
	}
	folded := strings.Join(strings.Fields(strings.ToLower(cond)), " ")
	return strings.TrimRight(folded, ".,;:!") == NextRelease
}

// compatCondition is the condition of text's compat marker, if it has one.
func compatCondition(text string) (string, bool) {
	f, ok := Parse(text)
	if !ok || f.Marker != strings.TrimSpace(Compat) {
		return "", false
	}
	return f.Text, true
}

// inComment reports whether the text before a marker opens a comment. It
// cannot tell a `//` inside a string from one that starts a comment. That
// noise stays in files whose subject is this marker.
func inComment(before string) bool {
	if strings.Contains(before, "//") || strings.Contains(before, "#") || strings.Contains(before, "/*") {
		return true
	}
	// A block comment's continuation lines carry only a leading star.
	return strings.HasPrefix(strings.TrimLeft(before, " \t"), "*")
}

// until extracts the condition from s, which starts at Compat, or empty. A
// condition that wraps past the line is truncated, not dropped: half of it
// still says what kind of thing retires the code.
func until(s string) string {
	rest := strings.TrimPrefix(s, Compat)
	if end := strings.Index(rest, ")"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}
