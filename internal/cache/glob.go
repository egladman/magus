package cache

import (
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
)

// compiledGlob is a pre-processed glob for zero-alloc repeated matching.
// Fast paths: exact path (==), extension glob ([prefix/]**/*.<ext> → HasSuffix+HasPrefix),
// complex (doublestar.Match). Source matching; output matching uses doublestar.Glob —
// keep semantics in sync.
type compiledGlob struct {
	raw    string // original pattern for doublestar fallback and diagnostics
	prefix string // path prefix before "**/" (may be empty)
	suffix string // ".ext" for extension-glob patterns; empty otherwise
	exact  bool   // true when raw contains no glob metacharacters
}

// compiledGlobs caches compiled patterns once per process (bounded by spell count).
var compiledGlobs sync.Map // string → compiledGlob

// compileGlobs returns pre-compiled matchers for each glob (cached by string).
func compileGlobs(globs []string) []compiledGlob {
	out := make([]compiledGlob, len(globs))
	for i, g := range globs {
		if v, ok := compiledGlobs.Load(g); ok {
			out[i] = v.(compiledGlob) //nolint:forcetypeassert // compiledGlobs only ever stores compiledGlob
			continue
		}
		cg := newCompiledGlob(g)
		compiledGlobs.Store(g, cg)
		out[i] = cg
	}
	return out
}

// newCompiledGlob classifies pat as exact, extension-glob, or complex.
func newCompiledGlob(pat string) compiledGlob {
	const meta = "*?[{"
	if !strings.ContainsAny(pat, meta) {
		return compiledGlob{raw: pat, exact: true}
	}

	const dstarDot = "**/*."
	if idx := strings.Index(pat, dstarDot); idx != -1 {
		suffix := "." + pat[idx+len(dstarDot):]
		if !strings.ContainsAny(suffix, meta) {
			prefix := pat[:idx] // everything before "**/", may be ""
			return compiledGlob{raw: pat, prefix: prefix, suffix: suffix}
		}
	}

	return compiledGlob{raw: pat}
}

// Match reports whether path matches. Zero allocations on the exact and extension-glob paths.
func (g compiledGlob) Match(path string) bool {
	if g.exact {
		return path == g.raw
	}
	if g.suffix != "" {
		if !strings.HasSuffix(path, g.suffix) {
			return false
		}
		if g.prefix != "" {
			return strings.HasPrefix(path, g.prefix)
		}
		return true
	}
	ok, _ := doublestar.Match(g.raw, path)
	return ok
}
