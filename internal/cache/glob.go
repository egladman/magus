package cache

import (
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
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
	// except are the exclusions of this glob's run (types.GlobRuns). Never cached with
	// the glob: one pattern appears in lists that narrow it differently.
	except []compiledGlob
}

// compiledGlobs caches compiled patterns once per process (bounded by spell count).
var compiledGlobs sync.Map // string → compiledGlob

// compileGlobs compiles a declared glob list into one matcher per glob, each carrying
// the exclusions of its run, so a path matches the list exactly when one of them matches
// it. An exclusion yields no matcher of its own.
func compileGlobs(globs []string) []compiledGlob {
	out := make([]compiledGlob, 0, len(globs))
	for run := range types.GlobRuns(globs) {
		var except []compiledGlob
		for _, g := range run.Exclusions {
			pattern, _ := types.CutExclusion(g)
			except = append(except, compileGlob(pattern))
		}
		for _, g := range run.Globs {
			cg := compileGlob(g)
			cg.except = except
			out = append(out, cg)
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
	const meta = "*?[{\\"
	if !strings.ContainsAny(pat, meta) {
		return compiledGlob{raw: pat, exact: true}
	}

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
