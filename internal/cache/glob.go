package cache

import (
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
)

// compiledGlob is a pre-processed types.Glob for zero-alloc repeated matching, giving
// the answers types.Glob.Match gives. Fast paths: literal (the path itself, or a
// directory it claims whole), extension glob ([prefix/]**/*.<ext> → HasSuffix+HasPrefix),
// complex (doublestar.Match).
type compiledGlob struct {
	raw    string // original pattern for doublestar fallback and diagnostics
	prefix string // path prefix before "**/" (may be empty)
	suffix string // ".ext" for extension-glob patterns; empty otherwise
	exact  bool   // true when raw contains no glob metacharacters
	// except are the glob's exclusions. Never cached with the pattern: one pattern
	// appears in declarations that narrow it differently.
	except []compiledGlob
}

// compiledGlobs caches compiled patterns once per process (bounded by spell count).
var compiledGlobs sync.Map // string → compiledGlob

// compileGlobs compiles declared globs into one matcher each, carrying its exclusions,
// so a path matches the list exactly when one of them matches it.
func compileGlobs(globs []types.Glob) []compiledGlob {
	out := make([]compiledGlob, len(globs))
	for i, g := range globs {
		out[i] = compileGlob(g.Pattern)
		for _, e := range g.Except {
			out[i].except = append(out[i].except, compileGlob(e))
		}
	}
	return out
}

// compileGlob returns the pre-compiled matcher for one pattern (cached by string).
func compileGlob(g string) compiledGlob {
	if v, ok := compiledGlobs.Load(g); ok {
		return v.(compiledGlob) //nolint:forcetypeassert // compiledGlobs only ever stores compiledGlob
	}
	cg := newCompiledGlob(g)
	compiledGlobs.Store(g, cg)
	return cg
}

// newCompiledGlob classifies pat as exact, extension-glob, or complex. A backslash
// escape (`\!` for a literal leading bang) is left to doublestar.
func newCompiledGlob(pat string) compiledGlob {
	if types.IsLiteralGlob(pat) {
		return compiledGlob{raw: pat, exact: true}
	}

	const meta = "*?[{\\"
	const dstarDot = "**/*."
	if idx := strings.Index(pat, dstarDot); idx != -1 {
		suffix := "." + pat[idx+len(dstarDot):]
		prefix := pat[:idx] // everything before "**/", may be ""
		if !strings.ContainsAny(suffix, meta) && !strings.ContainsAny(prefix, meta) {
			return compiledGlob{raw: pat, prefix: prefix, suffix: suffix}
		}
	}

	return compiledGlob{raw: pat}
}

// Match reports whether path matches the glob and none of its exclusions. Zero
// allocations on the exact and extension-glob paths.
func (g compiledGlob) Match(path string) bool {
	if !g.matchPattern(path) {
		return false
	}
	for _, e := range g.except {
		if e.matchPattern(path) {
			return false
		}
	}
	return true
}

func (g compiledGlob) matchPattern(path string) bool {
	if g.exact {
		rest, ok := strings.CutPrefix(path, g.raw)
		return ok && (rest == "" || rest[0] == '/')
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
