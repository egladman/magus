package langservice

import (
	"path"
	"strings"
)

// isIdentByte reports whether b can appear in a Buzz identifier. ASCII letters,
// digits, and underscore, plus any non-ASCII byte so UTF-8 identifiers scan as one
// run. This is a lexer-free approximation used by the cursor-context helpers, which
// must stay resilient on the half-typed source completion runs against.
func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b >= 0x80
}

// symbolKind classifies a top-level declaration found by scanning source.
type symbolKind string

const (
	symFunction symbolKind = "function"
	symConstant symbolKind = "constant"
	symType     symbolKind = "type"
)

// symbol is a top-level declaration discovered by scanSymbols.
type symbol struct {
	Name string
	Kind symbolKind
	// Sig is a best-effort signature for a function (e.g. "fun build(args: [str])"),
	// empty for other kinds. Recovered from the raw declaration line.
	Sig string
}

// scanSymbols returns the top-level declarations in src: functions, constants
// (var/final), and object/enum types. It scans line by line rather than parsing,
// so it still finds the declarations above a half-typed line the parser would
// reject - the normal state while completing. Only column-0-ish declarations are
// considered (leading whitespace is tolerated for indented magusfiles, but a
// declaration keyword must start the trimmed line), which keeps locals inside
// function bodies out of the top-level set.
func scanSymbols(src string) []symbol {
	var out []symbol
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimLeft(raw, " \t")
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimLeft(line, " \t")

		switch {
		case strings.HasPrefix(line, "fun "):
			if name := leadingIdent(line[len("fun "):]); name != "" {
				out = append(out, symbol{Name: name, Kind: symFunction, Sig: funSig(line)})
			}
		case strings.HasPrefix(line, "final "):
			if name := leadingIdent(line[len("final "):]); name != "" {
				out = append(out, symbol{Name: name, Kind: symConstant})
			}
		case strings.HasPrefix(line, "var "), strings.HasPrefix(line, "mut "):
			if name := leadingIdent(line[len("var "):]); name != "" {
				out = append(out, symbol{Name: name, Kind: symConstant})
			}
		case strings.HasPrefix(line, "object "):
			if name := leadingIdent(line[len("object "):]); name != "" {
				out = append(out, symbol{Name: name, Kind: symType})
			}
		case strings.HasPrefix(line, "enum "):
			if name := leadingIdent(line[len("enum "):]); name != "" {
				out = append(out, symbol{Name: name, Kind: symType})
			}
		}
	}
	return out
}

// funSig renders a compact function signature from a declaration line: everything
// from "fun" up to (and including) the parameter list's closing paren, so a hover
// or completion detail reads "fun build(args: [str])" without the body. Falls back
// to the whole trimmed line when no paren is present (a still-typed header).
func funSig(line string) string {
	if i := strings.IndexByte(line, ')'); i >= 0 {
		return strings.TrimSpace(line[:i+1])
	}
	if i := strings.IndexByte(line, '{'); i >= 0 {
		return strings.TrimSpace(line[:i])
	}
	return strings.TrimSpace(line)
}

// leadingIdent returns the identifier at the start of s (after optional spaces),
// or "" if s does not begin with one.
func leadingIdent(s string) string {
	s = strings.TrimLeft(s, " \t")
	i := 0
	for i < len(s) && isIdentByte(s[i]) {
		i++
	}
	return s[:i]
}

// importBinding is one `import` in the source: the module path and the name it
// binds (its alias, or the path's last segment).
type importBinding struct {
	Path string
	Name string
}

// scanImports returns the module imports in src, line by line, mapping each to the
// name it binds. Flat imports (`import "x" as _`) bind nothing and are skipped.
// Like scanSymbols this tolerates malformed lines below, so it keeps working while
// the file is being edited.
func scanImports(src string) []importBinding {
	var out []importBinding
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimLeft(raw, " \t")
		if !strings.HasPrefix(line, "import") {
			continue
		}
		rest := line[len("import"):]
		q := strings.IndexByte(rest, '"')
		if q < 0 {
			continue
		}
		rest = rest[q+1:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			continue // unterminated: still being typed
		}
		p := rest[:end]
		if p == "" {
			continue
		}
		name := path.Base(p)
		// An explicit alias (`import "x" as y`) rebinds the name; `_` means flat.
		if after := strings.TrimLeft(rest[end+1:], " \t"); strings.HasPrefix(after, "as ") {
			if alias := leadingIdent(after[len("as "):]); alias != "" {
				name = alias
			}
		}
		if name == "_" {
			continue
		}
		out = append(out, importBinding{Path: p, Name: name})
	}
	return out
}

// resolveModule maps a bound name at the cursor to a manifest module. It prefers an
// explicit import (so an alias resolves to the right module), then falls back to the
// bare module name, so `fs.` offers members even before the `import "fs"` is typed.
func resolveModule(base, src string) (Module, bool) {
	for _, imp := range scanImports(src) {
		if imp.Name == base {
			if m, ok := LookupModule(path.Base(imp.Path)); ok {
				return m, true
			}
		}
	}
	return LookupModule(base)
}
