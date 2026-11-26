package langservice

import (
	"slices"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// MemberKind says how a script reaches a member: by calling it or by reading it.
type MemberKind string

const (
	MemberFunction MemberKind = "function"
	MemberValue    MemberKind = "value"
)

// Member is one exported function or value of a module. Signature is the
// declaration's text from the name on, without the keyword or body. It reads
// `glob(pattern: str) > [str]` for a function and `sep: str` for a value. Doc is
// the comment block directly above the declaration, without its markers.
type Member struct {
	Name      string     `json:"name"`
	Signature string     `json:"signature"`
	Doc       string     `json:"doc,omitempty"`
	Kind      MemberKind `json:"kind"`
}

// parseMembers reads the exported top-level functions and values of a module.
func parseMembers(src string) ([]Member, error) {
	prog, err := buzz.Parse(src)
	if err != nil {
		return nil, err
	}
	lines := newLineIndex(src)
	var out []Member
	for _, n := range prog.Stmts {
		switch d := n.(type) {
		case *ast.FunDecl:
			if !d.IsExported {
				continue
			}
			out = append(out, Member{
				Name:      d.Name,
				Signature: funSignature(src, lines, d),
				Doc:       lines.docAbove(d.Line),
				Kind:      MemberFunction,
			})
		case *ast.DeclStmt:
			if !d.IsExported {
				continue
			}
			out = append(out, Member{
				Name:      d.Name,
				Signature: valueSignature(src, lines, d),
				Doc:       lines.docAbove(d.Line),
				Kind:      MemberValue,
			})
		}
	}
	return out, nil
}

// funSignature slices a function's header from its name to its body's brace,
// or to the `;` that ends an extern.
func funSignature(src string, lines lineIndex, d *ast.FunDecl) string {
	start := identAfter(src, lines.offset(d.Pos), d.Name)
	if start < 0 {
		return d.Name
	}
	end := len(src)
	if d.Body != nil {
		end = lines.offset(d.Body.Pos)
	} else if semi := topLevelByte(src, start, ';'); semi >= 0 {
		end = semi
	}
	if end < start {
		return d.Name
	}
	return collapse(src[start:end])
}

// valueSignature slices a value's name and type, up to its `=` or `;`.
func valueSignature(src string, lines lineIndex, d *ast.DeclStmt) string {
	start := identAfter(src, lines.offset(d.Pos), d.Name)
	if start < 0 {
		return d.Name
	}
	end := len(src)
	for i := start; i < len(src); i++ {
		if src[i] == '=' || src[i] == ';' || src[i] == '\n' {
			end = i
			break
		}
	}
	return collapse(src[start:end])
}

// identAfter returns the offset of the first whole-word name at or after from,
// or -1.
func identAfter(src string, from int, name string) int {
	for i := from; i >= 0 && i < len(src); {
		j := strings.Index(src[i:], name)
		if j < 0 {
			return -1
		}
		at := i + j
		end := at + len(name)
		if (at == 0 || !isIdentByte(src[at-1])) && (end == len(src) || !isIdentByte(src[end])) {
			return at
		}
		i = end
	}
	return -1
}

// topLevelByte returns the offset of the first b at or after from that sits
// outside brackets and string literals, or -1. A default value may hold b.
func topLevelByte(src string, from int, b byte) int {
	depth := 0
	for i := from; i < len(src); i++ {
		switch c := src[i]; {
		case c == '"':
			for i++; i < len(src) && src[i] != '"'; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == b && depth == 0:
			return i
		}
	}
	return -1
}

// collapse joins a header written over several lines into one.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// lineIndex maps the parser's 1-based line and byte column onto offsets.
type lineIndex struct {
	src    string
	starts []int
}

func newLineIndex(src string) lineIndex {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return lineIndex{src: src, starts: starts}
}

func (l lineIndex) offset(p ast.Pos) int {
	if p.Line < 1 || p.Line > len(l.starts) {
		return len(l.src)
	}
	return min(l.starts[p.Line-1]+max(p.Col-1, 0), len(l.src))
}

func (l lineIndex) line(n int) string {
	end := len(l.src)
	if n < len(l.starts) {
		end = l.starts[n] - 1
	}
	return strings.TrimRight(l.src[l.starts[n-1]:end], "\r")
}

// docAbove returns the `//` or `///` block ending on the line above line, each
// line without its marker and one space. A blank line ends the block, so a file
// header above a gap is no member's doc.
func (l lineIndex) docAbove(line int) string {
	var doc []string
	for n := line - 1; n >= 1 && n <= len(l.starts); n-- {
		text := strings.TrimLeft(l.line(n), " \t")
		if !strings.HasPrefix(text, "//") {
			break
		}
		text = strings.TrimPrefix(strings.TrimPrefix(text, "//"), "/")
		doc = append(doc, strings.TrimPrefix(text, " "))
	}
	slices.Reverse(doc)
	return strings.Join(doc, "\n")
}
