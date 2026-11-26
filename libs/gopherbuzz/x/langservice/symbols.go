package langservice

import (
	"path"
	"strings"
)

// isIdentByte reports whether b can appear in a Buzz identifier. Any non-ASCII
// byte counts, so a UTF-8 identifier scans as one run. It needs no lexer, so it
// holds on half-typed source.
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

// scanSymbols returns the top-level declarations in src. It scans line by line,
// so a half-typed line the parser would reject hides nothing above it. A
// declaration keyword must start the trimmed line, which keeps most locals out.
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
			if name := leadingIdent(line[4:]); name != "" { // past the matched "var "/"mut " keyword (both 4 bytes)
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

// funSig cuts a declaration line after the parameter list's closing paren, so a
// detail reads "fun build(args: [str])" without the body. A header still being
// typed falls back to the text before "{", else the whole line.
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

// scanImports returns the imports in src with the name each binds. A flat import
// (`import "x" as _`) binds nothing and is skipped. It reads line by line, so a
// malformed line below does not hide the imports above it.
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
		name := moduleBase(p)
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

// moduleBase returns the name a module path binds: its last segment, without a
// URI-style scheme. Upstream's `buzz:` scheme names a stdlib module, and
// `import "buzz:std"` binds `std` exactly like `import "std"`.
func moduleBase(p string) string {
	if c := strings.IndexByte(p, ':'); c >= 0 {
		if slash := strings.IndexByte(p, '/'); slash < 0 || c < slash {
			p = p[c+1:]
		}
	}
	return path.Base(p)
}

// stripScheme drops the scheme moduleBase drops and keeps the whole path.
func stripScheme(p string) string {
	if c := strings.IndexByte(p, ':'); c >= 0 {
		if slash := strings.IndexByte(p, '/'); slash < 0 || c < slash {
			return p[c+1:]
		}
	}
	return p
}

// lineStart returns the index just past the last newline in s (0 if none), i.e.
// the start of the line the end of s sits on.
func lineStart(s string) int {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

// identSpan returns the byte range of the identifier straddling or ending at
// offset, expanding in both directions over identifier bytes. An empty range
// (start == end) means the cursor is not on an identifier.
func identSpan(src string, offset int) (start, end int) {
	start, end = offset, offset
	for start > 0 && isIdentByte(src[start-1]) {
		start--
	}
	for end < len(src) && isIdentByte(src[end]) {
		end++
	}
	return start, end
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func clampOffset(src string, offset int) int {
	return max(0, min(offset, len(src)))
}
