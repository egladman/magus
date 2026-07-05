package langservice

import "strings"

// Signature is call-signature help: the callee's rendered signature and its doc.
type Signature struct {
	Label string `json:"label"`
	Doc   string `json:"doc,omitempty"`
}

// SignatureAt returns signature help for the call whose argument list encloses
// offset, or nil when the cursor is not inside such a call or the callee is not a
// known module method, in-file function, or builtin. It finds the innermost
// unclosed "(" before the cursor, reads the callee in front of it, and resolves it
// the same way completion and hover do - so it works on the half-typed source an
// editor asks about.
func SignatureAt(src string, offset int) *Signature {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}

	// Walk back to the "(" this cursor sits inside, balancing nested parens and
	// stopping at a statement boundary (we are not in a call then).
	open := -1
	depth := 0
scan:
	for i := offset - 1; i >= 0; i-- {
		switch src[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				open = i
				break scan
			}
			depth--
		case ';', '{', '}':
			break scan
		}
	}
	if open < 0 {
		return nil
	}

	// The callee is the identifier (or module.member) immediately before "(".
	j := open
	for j > 0 && isSpace(src[j-1]) {
		j--
	}
	end := j
	for j > 0 && (isIdentByte(src[j-1]) || src[j-1] == '.') {
		j--
	}
	callee := src[j:end]
	if callee == "" {
		return nil
	}

	if dot := strings.LastIndexByte(callee, '.'); dot >= 0 {
		base, member := callee[:dot], callee[dot+1:]
		if mod, ok := resolveModule(base, src); ok {
			for _, m := range mod.Methods {
				if m.Name == member {
					return &Signature{Label: m.Sig, Doc: m.Doc}
				}
			}
		}
		return nil
	}

	for _, s := range scanSymbols(src) {
		if s.Name == callee && s.Kind == symFunction {
			return &Signature{Label: s.Sig}
		}
	}
	for _, b := range builtins {
		if b == callee {
			return &Signature{Label: callee + "(...)"}
		}
	}
	return nil
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
